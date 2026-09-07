package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gke-labs/k8s-ai-bench/pkg/model"
)

func TestAgentCommandAdapters(t *testing.T) {
	x := &TaskExecution{
		kubeConfig: "/tmp/kubeconfig",
		agentConfig: AgentConfig{
			ID:      "kubectl-ai",
			Bin:     "/bin/kubectl-ai",
			Adapter: "kubectl-ai",
		},
		llmConfig: model.LLMConfig{
			ProviderID:        "gemini",
			ModelID:           "gemini-2.5-pro",
			EnableToolUseShim: true,
			Quiet:             true,
			McpClient:         true,
		},
	}

	bin, args, err := x.agentCommand("/tmp/trace.yaml")
	if err != nil {
		t.Fatalf("agentCommand returned error: %v", err)
	}
	if bin != "/bin/kubectl-ai" {
		t.Fatalf("bin = %q, want /bin/kubectl-ai", bin)
	}
	if !argvContainsAll(args, []string{"--kubeconfig", "/tmp/kubeconfig", "--model", "gemini-2.5-pro", "--mcp-client"}) {
		t.Fatalf("kubectl-ai args missing expected values: %#v", args)
	}

	x.agentConfig = AgentConfig{
		ID:      "generic",
		Bin:     "/bin/agent",
		Adapter: "generic-stdin",
		Args:    []string{"run", "--quiet"},
	}
	bin, args, err = x.agentCommand("/tmp/trace.yaml")
	if err != nil {
		t.Fatalf("generic agentCommand returned error: %v", err)
	}
	if bin != "/bin/agent" || !argvContainsAll(args, []string{"run", "--quiet", "--llm-provider", "gemini", "--model", "gemini-2.5-pro"}) {
		t.Fatalf("generic command = %q %#v, want provider/model args", bin, args)
	}

	x.agentConfig = AgentConfig{
		ID:      "dce",
		Bin:     "/bin/dce",
		Adapter: "direct-cli",
		Args:    []string{"container-management"},
	}
	bin, args, err = x.agentCommand("/tmp/trace.yaml")
	if err != nil {
		t.Fatalf("direct-cli agentCommand returned error: %v", err)
	}
	if bin != "/bin/dce" || len(args) != 1 || args[0] != "container-management" {
		t.Fatalf("direct-cli command = %q %#v, want /bin/dce [container-management]", bin, args)
	}
}

func TestComposePromptInjectsSkillAndCLI(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "kube-debug")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Use kube-inspector for pod diagnosis."), 0644); err != nil {
		t.Fatal(err)
	}

	x := &TaskExecution{
		skillsDir: filepath.Join(dir, "skills"),
		task: &Task{
			Skills: []string{"kube-debug/SKILL.md"},
			CLIs:   []CLIRef{{Name: "kube-inspector", Path: "kube-inspector"}},
		},
	}

	prompt, err := x.composePrompt("Diagnose pod app.")
	if err != nil {
		t.Fatalf("composePrompt returned error: %v", err)
	}
	for _, want := range []string{"<skill name=\"kube-debug\">", "Use kube-inspector", "Available CLI tools:", "- kube-inspector", "Task:\nDiagnose pod app."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCLIWrapperAuditsCalls(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is required for CLI wrapper test")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for CLI wrapper test")
	}

	dir := t.TempDir()
	realCLI := filepath.Join(dir, "clis", "kube-inspector")
	if err := os.MkdirAll(filepath.Dir(realCLI), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realCLI, []byte("#!/usr/bin/env bash\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	x := &TaskExecution{
		clisDir:       filepath.Join(dir, "clis"),
		taskOutputDir: filepath.Join(dir, "out"),
		task: &Task{
			CLIs: []CLIRef{{Name: "kube-inspector", Path: "kube-inspector"}},
		},
	}
	wrapperDir := filepath.Join(dir, "wrappers")
	auditPath := filepath.Join(dir, "audit.jsonl")
	if err := x.createCLIWrappers(wrapperDir, auditPath); err != nil {
		t.Fatalf("createCLIWrappers returned error: %v", err)
	}

	cmd := exec.CommandContext(context.Background(), filepath.Join(wrapperDir, "kube-inspector"), "inspect", "pod", "app")
	cmd.Env = append(os.Environ(), "K8S_AI_BENCH_CLI_AUDIT="+auditPath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("wrapper command failed: %v", err)
	}

	calls, err := readCLIAudit(auditPath)
	if err != nil {
		t.Fatalf("readCLIAudit returned error: %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "kube-inspector" || !argvContainsAll(calls[0].Argv, []string{"inspect", "pod", "app"}) {
		t.Fatalf("unexpected audit calls: %#v", calls)
	}
}

func TestEvaluateTaskRequiresCLIsDirForLocalCLIs(t *testing.T) {
	config := EvalConfig{
		MatrixFile: "eval-matrix.yaml",
		Agents: map[string]AgentConfig{
			"generic": {
				ID:      "generic",
				Bin:     "/bin/true",
				Adapter: "generic-stdin",
			},
		},
		OutputDir: t.TempDir(),
	}
	task := Task{
		Agent: "generic",
		CLIs:  []CLIRef{{Name: "kube-inspector", Path: "kube-inspector"}},
		Script: []ScriptStep{{
			Prompt: "Diagnose pod app.",
		}},
	}

	result := evaluateTask(context.Background(), config, "local-cli-task", task, model.LLMConfig{ID: "test"}, 1, nil, nil)
	if result.Result != "error" {
		t.Fatalf("result = %q, want error", result.Result)
	}
	if !strings.Contains(result.Error, "declares local CLIs but matrix clisDir is not set") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

func TestEvaluateTaskRequiresSkillsDirForLocalSkills(t *testing.T) {
	config := EvalConfig{
		MatrixFile: "eval-matrix.yaml",
		Agents: map[string]AgentConfig{
			"generic": {
				ID:      "generic",
				Bin:     "/bin/true",
				Adapter: "generic-stdin",
			},
		},
		OutputDir: t.TempDir(),
	}
	task := Task{
		Agent:  "generic",
		Skills: []string{"kube-debug/SKILL.md"},
		Script: []ScriptStep{{
			Prompt: "Diagnose pod app.",
		}},
	}

	result := evaluateTask(context.Background(), config, "local-skill-task", task, model.LLMConfig{ID: "test"}, 1, nil, nil)
	if result.Result != "error" {
		t.Fatalf("result = %q, want error", result.Result)
	}
	if !strings.Contains(result.Error, "declares local skills but matrix skillsDir is not set") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

func TestEvaluateCLIExpectationsRequiredFailure(t *testing.T) {
	result := &model.TaskResult{}
	x := &TaskExecution{
		result:        result,
		taskOutputDir: t.TempDir(),
		task: &Task{
			CLIExpect: []CLIExpectation{{
				Name:         "kube-inspector",
				Required:     true,
				ArgvContains: []string{"inspect"},
			}},
		},
	}

	failures := x.evaluateCLIExpectations()
	if len(failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(failures))
	}
	if len(result.CLIResults) != 1 || result.CLIResults[0].Called {
		t.Fatalf("unexpected CLIResults: %#v", result.CLIResults)
	}
}

func TestAppendCLIAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "cli-audit.jsonl")
	if err := appendCLIAudit(auditPath, model.CLICall{
		Name:     "dce",
		Argv:     []string{"container-management", "cluster", "get-cluster"},
		ExitCode: 0,
	}); err != nil {
		t.Fatalf("appendCLIAudit returned error: %v", err)
	}
	calls, err := readCLIAudit(auditPath)
	if err != nil {
		t.Fatalf("readCLIAudit returned error: %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "dce" || !argvContainsAll(calls[0].Argv, []string{"container-management", "get-cluster"}) {
		t.Fatalf("unexpected calls: %#v", calls)
	}
}
