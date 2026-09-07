// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gke-labs/k8s-ai-bench/pkg/model"
)

func (x *TaskExecution) runDirectCLI(ctx context.Context, bin string, baseArgs []string, env []string, auditPath string) (string, error) {
	var stdoutBuffer bytes.Buffer
	for _, step := range x.task.Script {
		prompt, err := step.ResolvePrompt(x.taskDir)
		if err != nil {
			return stdoutBuffer.String(), err
		}
		prompt, err = x.composePrompt(prompt)
		if err != nil {
			return stdoutBuffer.String(), err
		}
		if err := os.WriteFile(filepath.Join(x.taskOutputDir, "prompt.txt"), []byte(prompt), 0644); err != nil {
			return stdoutBuffer.String(), err
		}

		args := append(append([]string{}, baseArgs...), step.Args...)
		startedAt := time.Now().UTC().Format(time.RFC3339)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if x.log != nil {
			cmd.Stdout = io.MultiWriter(cmd.Stdout, x.log, &stdoutBuffer)
			cmd.Stderr = io.MultiWriter(cmd.Stderr, x.log)
		} else {
			cmd.Stdout = io.MultiWriter(cmd.Stdout, &stdoutBuffer)
		}
		err = cmd.Run()
		exitCode := 0
		if err != nil {
			exitCode = 1
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		endedAt := time.Now().UTC().Format(time.RFC3339)
		cwd, _ := os.Getwd()
		call := model.CLICall{
			Name:      filepath.Base(bin),
			Argv:      args,
			Cwd:       cwd,
			ExitCode:  exitCode,
			StartedAt: startedAt,
			EndedAt:   endedAt,
		}
		if err := appendCLIAudit(auditPath, call); err != nil {
			return stdoutBuffer.String(), err
		}
		if err != nil {
			return stdoutBuffer.String(), err
		}
	}
	return stdoutBuffer.String(), nil
}

func (x *TaskExecution) agentCommand(tracePath string) (string, []string, error) {
	bin := x.agentConfig.Bin
	if bin == "" {
		bin = x.AgentBin
	}
	if bin == "" {
		return "", nil, fmt.Errorf("agent %q has no binary configured", x.agentConfig.ID)
	}

	switch x.agentConfig.Adapter {
	case "kubectl-ai":
		args := []string{
			"--kubeconfig", x.kubeConfig,
			"--llm-provider", x.llmConfig.ProviderID,
			fmt.Sprintf("--enable-tool-use-shim=%t", x.llmConfig.EnableToolUseShim),
			fmt.Sprintf("--quiet=%t", x.llmConfig.Quiet),
			"--model", x.llmConfig.ModelID,
			"--trace-path", tracePath,
			"--skip-permissions",
			"--show-tool-output",
		}
		if x.llmConfig.McpClient {
			args = append(args, "--mcp-client")
		}
		return bin, args, nil
	case "generic-stdin":
		args := append([]string{}, x.agentConfig.Args...)
		args = append(args,
			"--llm-provider", x.llmConfig.ProviderID,
			"--model", x.llmConfig.ModelID,
		)
		return bin, args, nil
	case "direct-cli":
		return bin, append([]string{}, x.agentConfig.Args...), nil
	default:
		return "", nil, fmt.Errorf("unsupported agent adapter %q", x.agentConfig.Adapter)
	}
}

func (x *TaskExecution) agentEnv(wrapperDir, auditPath string) ([]string, error) {
	if len(x.task.CLIs) > 0 {
		if err := x.createCLIWrappers(wrapperDir, auditPath); err != nil {
			return nil, err
		}
	}

	envMap := envSliceToMap(os.Environ())
	for k, v := range x.agentConfig.Env {
		envMap[k] = os.ExpandEnv(v)
	}
	for k, v := range x.llmConfig.Env {
		envMap[k] = os.ExpandEnv(v)
	}

	path := envMap["PATH"]
	if len(x.task.CLIs) > 0 {
		path = wrapperDir + string(os.PathListSeparator) + path
	}
	envMap["PATH"] = path
	envMap["KUBECONFIG"] = x.kubeConfig
	envMap["K8S_AI_BENCH_CLI_AUDIT"] = auditPath
	envMap["K8S_AI_BENCH_TASK_OUTPUT_DIR"] = x.taskOutputDir

	return envMapToSlice(envMap), nil
}

func envSliceToMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func envMapToSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}
