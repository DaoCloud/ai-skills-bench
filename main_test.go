// Copyright 2026 Google LLC
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
	"strings"
	"testing"
)

func TestApplyMatrixConfig(t *testing.T) {
	quiet := false
	matrix := MatrixConfig{
		SkillsDir:             "./skills",
		CLIsDir:               "./clis",
		TasksDir:              "./tasks/skill-cli",
		OutputDir:             ".build/skill-cli-bench",
		ClusterCreationPolicy: "DoNotCreate",
		Agents: []AgentConfig{{
			ID:      "generic",
			Bin:     "./agent",
			Adapter: "generic-stdin",
			Args:    []string{"run"},
		}},
		Models: []MatrixModel{{
			ID:       "qwen",
			Provider: "openai",
			Model:    "Qwen/Qwen3-Coder",
			Env:      map[string]string{"OPENAI_API_BASE": "http://localhost:8000/v1"},
		}},
		Runs: MatrixRuns{
			Iterations:  3,
			Concurrency: 2,
			TaskPattern: "debug",
			Agent:       "generic",
		},
	}

	var config EvalConfig
	if err := applyMatrixConfig(&config, matrix, quiet); err != nil {
		t.Fatalf("applyMatrixConfig returned error: %v", err)
	}
	if config.Agents["generic"].Adapter != "generic-stdin" {
		t.Fatalf("agent not applied: %#v", config.Agents)
	}
	if len(config.LLMConfigs) != 1 || config.LLMConfigs[0].ID != "qwen" || config.LLMConfigs[0].Env["OPENAI_API_BASE"] == "" {
		t.Fatalf("models not applied: %#v", config.LLMConfigs)
	}
	if config.SkillsDir != "./skills" || config.CLIsDir != "./clis" {
		t.Fatalf("dirs not applied: skills=%q clis=%q", config.SkillsDir, config.CLIsDir)
	}
	if config.TasksDir != "./tasks/skill-cli" || config.OutputDir != ".build/skill-cli-bench" || config.ClusterCreationPolicy != DoNotCreate {
		t.Fatalf("run config not applied: tasks=%q output=%q policy=%q", config.TasksDir, config.OutputDir, config.ClusterCreationPolicy)
	}
	if config.Iterations != 3 || config.Concurrency != 2 || config.TaskPattern != "debug" {
		t.Fatalf("runs not applied: %#v", config)
	}
	if config.Agent != "generic" {
		t.Fatalf("run agent not applied: %q", config.Agent)
	}
}

func TestApplyMatrixConfigRejectsUnknownRunAgent(t *testing.T) {
	matrix := MatrixConfig{
		TasksDir:              "./tasks",
		OutputDir:             ".build/test",
		ClusterCreationPolicy: "DoNotCreate",
		Agents: []AgentConfig{{
			ID: "codex", Bin: "./bridge", Adapter: "generic-stdin",
		}},
		Models: []MatrixModel{{ID: "model", Provider: "openai", Model: "model"}},
		Runs:   MatrixRuns{Agent: "claude"},
	}

	var config EvalConfig
	err := applyMatrixConfig(&config, matrix, true)
	if err == nil || !strings.Contains(err.Error(), `run agent "claude" is not configured`) {
		t.Fatalf("applyMatrixConfig error = %v, want unknown run agent error", err)
	}
}

func TestApplyMatrixConfigAllowsMissingSkillsAndCLIsDirs(t *testing.T) {
	quiet := false
	matrix := MatrixConfig{
		TasksDir:              "./tasks/skill-cli-hermes",
		OutputDir:             ".build/hermes-bench",
		ClusterCreationPolicy: "DoNotCreate",
		Agents: []AgentConfig{{
			ID:      "hermes",
			Bin:     "./k8s-ai-hermes-bridge",
			Adapter: "generic-stdin",
		}},
		Models: []MatrixModel{{
			ID:       "hermes-agent",
			Provider: "openai",
			Model:    "hermes-agent",
		}},
	}

	var config EvalConfig
	if err := applyMatrixConfig(&config, matrix, quiet); err != nil {
		t.Fatalf("applyMatrixConfig returned error: %v", err)
	}
	if config.SkillsDir != "" {
		t.Fatalf("skillsDir should remain empty when omitted, got %q", config.SkillsDir)
	}
	if config.CLIsDir != "" {
		t.Fatalf("clisDir should remain empty when omitted, got %q", config.CLIsDir)
	}
}
