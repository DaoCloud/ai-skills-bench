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
	"fmt"
	"os"

	"github.com/gke-labs/k8s-ai-bench/pkg/model"
	"sigs.k8s.io/yaml"
)

type AgentConfig struct {
	ID      string            `json:"id"`
	Bin     string            `json:"bin"`
	Adapter string            `json:"adapter"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type MatrixModel struct {
	ID                string            `json:"id"`
	Provider          string            `json:"provider"`
	Model             string            `json:"model"`
	EnableToolUseShim bool              `json:"enableToolUseShim,omitempty"`
	Quiet             *bool             `json:"quiet,omitempty"`
	McpClient         bool              `json:"mcpClient,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
}

type CLIRef struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Required bool   `json:"required,omitempty"`
}

type MatrixRuns struct {
	Iterations  int    `json:"iterations,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
	TaskPattern string `json:"taskPattern,omitempty"`
	Agent       string `json:"agent,omitempty"`
}

type MatrixConfig struct {
	SkillsDir             string        `json:"skillsDir,omitempty"`
	CLIsDir               string        `json:"clisDir,omitempty"`
	TasksDir              string        `json:"tasksDir,omitempty"`
	OutputDir             string        `json:"outputDir,omitempty"`
	ClusterCreationPolicy string        `json:"clusterCreationPolicy,omitempty"`
	Agents                []AgentConfig `json:"agents,omitempty"`
	Models                []MatrixModel `json:"models,omitempty"`
	Runs                  MatrixRuns    `json:"runs,omitempty"`
}

func loadMatrixConfig(path string) (MatrixConfig, error) {
	expanded, err := expandPath(path)
	if err != nil {
		return MatrixConfig{}, fmt.Errorf("expanding matrix file path %q: %w", path, err)
	}
	data, err := os.ReadFile(expanded)
	if err != nil {
		return MatrixConfig{}, fmt.Errorf("reading matrix file %q: %w", expanded, err)
	}
	var matrix MatrixConfig
	if err := yaml.Unmarshal(data, &matrix); err != nil {
		return MatrixConfig{}, fmt.Errorf("parsing matrix file %q: %w", expanded, err)
	}
	return matrix, nil
}

func applyMatrixConfig(config *EvalConfig, matrix MatrixConfig, defaultQuiet bool) error {
	if matrix.TasksDir == "" {
		return fmt.Errorf("matrix must define tasksDir")
	}
	if matrix.OutputDir == "" {
		return fmt.Errorf("matrix must define outputDir")
	}
	if matrix.ClusterCreationPolicy == "" {
		return fmt.Errorf("matrix must define clusterCreationPolicy")
	}
	config.SkillsDir = matrix.SkillsDir
	config.CLIsDir = matrix.CLIsDir
	config.TasksDir = matrix.TasksDir
	config.OutputDir = matrix.OutputDir
	config.ClusterCreationPolicy = ClusterCreationPolicy(matrix.ClusterCreationPolicy)

	if len(matrix.Agents) == 0 {
		return fmt.Errorf("matrix must define at least one agent")
	}
	if len(matrix.Models) == 0 {
		return fmt.Errorf("matrix must define at least one model")
	}

	config.Agents = make(map[string]AgentConfig, len(matrix.Agents))
	for _, agent := range matrix.Agents {
		if agent.ID == "" {
			return fmt.Errorf("matrix agent is missing id")
		}
		if agent.Bin == "" {
			return fmt.Errorf("matrix agent %q is missing bin", agent.ID)
		}
		if agent.Adapter == "" {
			return fmt.Errorf("matrix agent %q is missing adapter", agent.ID)
		}
		if agent.Adapter != "kubectl-ai" && agent.Adapter != "generic-stdin" && agent.Adapter != "direct-cli" {
			return fmt.Errorf("matrix agent %q has unsupported adapter %q", agent.ID, agent.Adapter)
		}
		if _, exists := config.Agents[agent.ID]; exists {
			return fmt.Errorf("duplicate matrix agent id %q", agent.ID)
		}
		config.Agents[agent.ID] = agent
	}
	if matrix.Runs.Agent != "" {
		if _, ok := config.Agents[matrix.Runs.Agent]; !ok {
			return fmt.Errorf("run agent %q is not configured", matrix.Runs.Agent)
		}
	}

	for _, matrixModel := range matrix.Models {
		if matrixModel.ID == "" {
			return fmt.Errorf("matrix model is missing id")
		}
		if matrixModel.Provider == "" {
			return fmt.Errorf("matrix model %q is missing provider", matrixModel.ID)
		}
		if matrixModel.Model == "" {
			return fmt.Errorf("matrix model %q is missing model", matrixModel.ID)
		}
		quiet := defaultQuiet
		if matrixModel.Quiet != nil {
			quiet = *matrixModel.Quiet
		}
		config.LLMConfigs = append(config.LLMConfigs, model.LLMConfig{
			ID:                matrixModel.ID,
			ProviderID:        matrixModel.Provider,
			ModelID:           matrixModel.Model,
			EnableToolUseShim: matrixModel.EnableToolUseShim,
			Quiet:             quiet,
			McpClient:         matrixModel.McpClient,
			Env:               matrixModel.Env,
		})
	}

	if matrix.Runs.Iterations > 0 {
		config.Iterations = matrix.Runs.Iterations
	}
	if matrix.Runs.Concurrency > 0 {
		config.Concurrency = matrix.Runs.Concurrency
	}
	if matrix.Runs.TaskPattern != "" {
		config.TaskPattern = matrix.Runs.TaskPattern
	}
	if matrix.Runs.Agent != "" {
		config.Agent = matrix.Runs.Agent
	}

	return nil
}
