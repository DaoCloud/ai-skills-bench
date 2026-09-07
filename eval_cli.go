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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gke-labs/k8s-ai-bench/pkg/model"
)

func (x *TaskExecution) createCLIWrappers(wrapperDir, auditPath string) error {
	if err := os.MkdirAll(wrapperDir, 0755); err != nil {
		return fmt.Errorf("creating CLI wrapper directory: %w", err)
	}
	for _, cli := range x.task.CLIs {
		if isReservedCommandName(cli.Name) {
			return fmt.Errorf("CLI %q is reserved and cannot be shadowed by benchmark wrapper", cli.Name)
		}
		realPath, err := resolveRelativePath(x.clisDir, cli.Path)
		if err != nil {
			return fmt.Errorf("resolving CLI %q path: %w", cli.Name, err)
		}
		if _, err := os.Stat(realPath); err != nil {
			return fmt.Errorf("checking CLI %q at %q: %w", cli.Name, realPath, err)
		}
		wrapperPath := filepath.Join(wrapperDir, cli.Name)
		script := fmt.Sprintf(`#!/usr/bin/env bash
set +e
started_at="$(date -u +"%%Y-%%m-%%dT%%H:%%M:%%SZ")"
cwd="$(pwd)"
%s "$@"
exit_code=$?
ended_at="$(date -u +"%%Y-%%m-%%dT%%H:%%M:%%SZ")"
AUDIT_PATH="${K8S_AI_BENCH_CLI_AUDIT:-}"
if [ -z "$AUDIT_PATH" ]; then
  AUDIT_PATH=%s
fi
CLI_NAME=%s STARTED_AT="$started_at" ENDED_AT="$ended_at" CWD="$cwd" EXIT_CODE="$exit_code" python3 - "$@" <<'PY' >> "$AUDIT_PATH"
import json, os, sys
print(json.dumps({
    "name": os.environ["CLI_NAME"],
    "argv": sys.argv[1:],
    "cwd": os.environ.get("CWD", ""),
    "exitCode": int(os.environ.get("EXIT_CODE", "0")),
    "startedAt": os.environ.get("STARTED_AT", ""),
    "endedAt": os.environ.get("ENDED_AT", ""),
}))
PY
exit "$exit_code"
`, shellQuote(realPath), shellQuote(auditPath), shellQuote(cli.Name))
		if err := os.WriteFile(wrapperPath, []byte(script), 0755); err != nil {
			return fmt.Errorf("writing CLI wrapper %q: %w", wrapperPath, err)
		}
	}
	return nil
}

func (x *TaskExecution) evaluateCLIExpectations() []model.Failure {
	if len(x.task.CLIExpect) == 0 {
		return nil
	}

	calls, err := readCLIAudit(filepath.Join(x.taskOutputDir, "cli-audit.jsonl"))
	if err != nil {
		return []model.Failure{{Message: fmt.Sprintf("failed to read CLI audit: %v", err)}}
	}

	var failures []model.Failure
	for _, expect := range x.task.CLIExpect {
		result := model.CLIResult{
			Name:     expect.Name,
			Required: expect.Required,
		}
		for _, call := range calls {
			if call.Name != expect.Name {
				continue
			}
			result.Called = true
			result.Calls = append(result.Calls, call)
			if argvContainsAll(call.Argv, expect.ArgvContains) {
				result.Matched = true
			}
		}
		x.result.CLIResults = append(x.result.CLIResults, result)
		if expect.Required && !result.Called {
			failures = append(failures, model.Failure{Message: fmt.Sprintf("required CLI %q was not called", expect.Name)})
			continue
		}
		if expect.Required && !result.Matched {
			failures = append(failures, model.Failure{Message: fmt.Sprintf("required CLI %q was called but did not match argvContains %v", expect.Name, expect.ArgvContains)})
		}
	}

	return failures
}

func readCLIAudit(path string) ([]model.CLICall, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var calls []model.CLICall
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var call model.CLICall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			return nil, fmt.Errorf("parsing %s line %d: %w", path, lineNo+1, err)
		}
		calls = append(calls, call)
	}
	return calls, nil
}

func appendCLIAudit(path string, call model.CLICall) error {
	data, err := json.Marshal(call)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func argvContainsAll(argv []string, needles []string) bool {
	if len(needles) == 0 {
		return true
	}
	joined := strings.Join(argv, " ")
	for _, needle := range needles {
		found := false
		for _, arg := range argv {
			if strings.Contains(arg, needle) {
				found = true
				break
			}
		}
		if !found && strings.Contains(joined, needle) {
			found = true
		}
		if !found {
			return false
		}
	}
	return true
}

func isReservedCommandName(name string) bool {
	switch name {
	case "bash", "sh", "zsh", "python", "python3", "node", "kubectl", "helm", "go":
		return true
	default:
		return false
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
