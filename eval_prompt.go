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
	"path/filepath"
	"strings"
)

func (x *TaskExecution) composePrompt(taskPrompt string) (string, error) {
	if len(x.task.Skills) == 0 && len(x.task.CLIs) == 0 {
		return taskPrompt, nil
	}

	var b strings.Builder
	b.WriteString("You have access to the following benchmark-provided skills.\n\n")
	for _, skillPath := range x.task.Skills {
		resolved, err := resolveRelativePath(x.skillsDir, skillPath)
		if err != nil {
			return "", fmt.Errorf("resolving skill %q: %w", skillPath, err)
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return "", fmt.Errorf("reading skill %q: %w", resolved, err)
		}
		name := strings.TrimSuffix(filepath.Base(filepath.Dir(resolved)), string(filepath.Separator))
		if filepath.Base(resolved) != "SKILL.md" {
			name = strings.TrimSuffix(filepath.Base(resolved), filepath.Ext(resolved))
		}
		b.WriteString(fmt.Sprintf("<skill name=%q>\n%s\n</skill>\n\n", name, strings.TrimSpace(string(content))))
	}

	if len(x.task.CLIs) > 0 {
		b.WriteString("Available CLI tools:\n")
		for _, cli := range x.task.CLIs {
			b.WriteString(fmt.Sprintf("- %s\n", cli.Name))
		}
		b.WriteString("\n")
	}

	b.WriteString("Task:\n")
	b.WriteString(taskPrompt)
	return b.String(), nil
}
