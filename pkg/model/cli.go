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

package model

type CLIResult struct {
	Name     string    `json:"name"`
	Required bool      `json:"required"`
	Called   bool      `json:"called"`
	Matched  bool      `json:"matched"`
	Calls    []CLICall `json:"calls,omitempty"`
}

type CLICall struct {
	Name      string   `json:"name"`
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd,omitempty"`
	ExitCode  int      `json:"exitCode"`
	StartedAt string   `json:"startedAt,omitempty"`
	EndedAt   string   `json:"endedAt,omitempty"`
}
