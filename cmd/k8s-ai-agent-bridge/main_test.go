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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCodexForwardsPromptToConfiguredBinary(t *testing.T) {
	bin := writeFakeAgent(t, "codex", `#!/bin/sh
printf 'argv=%s\n' "$*"
cat
`)
	t.Setenv("CODEX_BIN", bin)
	t.Setenv("CODEX_MODEL", "codex-cli-model")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--agent", "codex",
		"--model", "benchmark-metadata-model",
	}, strings.NewReader("inspect the cluster\n"), &stdout, &stderr, http.DefaultClient)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "inspect the cluster") {
		t.Fatalf("stdout = %q, want forwarded prompt", got)
	}
	if !strings.Contains(stdout.String(), "exec") || !strings.Contains(stdout.String(), "codex-cli-model") || strings.Contains(stdout.String(), "benchmark-metadata-model") {
		t.Fatalf("stdout = %q, want headless Codex arguments", stdout.String())
	}
}

func TestRunClaudeUsesConfiguredBinaryAndHeadlessMode(t *testing.T) {
	bin := writeFakeAgent(t, "claude", `#!/bin/sh
printf 'argv=%s\n' "$*"
cat
	`)
	t.Setenv("CLAUDE_BIN", bin)
	t.Setenv("CLAUDE_MODEL", "claude-cli-model")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--agent", "claude",
		"--model", "benchmark-metadata-model",
	}, strings.NewReader("inspect the cluster\n"), &stdout, &stderr, http.DefaultClient)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "inspect the cluster") {
		t.Fatalf("stdout = %q, want forwarded prompt", stdout.String())
	}
	if !strings.Contains(stdout.String(), "-p") || !strings.Contains(stdout.String(), "--output-format text") || !strings.Contains(stdout.String(), "claude-cli-model") || strings.Contains(stdout.String(), "benchmark-metadata-model") {
		t.Fatalf("stdout = %q, want Claude print-mode arguments", stdout.String())
	}
}

func TestRunCLIReportsMissingBinary(t *testing.T) {
	t.Setenv("CODEX_BIN", "")
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--agent", "codex"}, strings.NewReader("prompt"), &stdout, &stderr, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("run error = %v, want missing codex error", err)
	}
}

func TestRunOpenClawUsesOpenAICompatibleGateway(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"cluster is healthy"}}]}`)
	}))
	defer server.Close()
	t.Setenv("OPENCLAW_BASE_URL", server.URL+"/v1")
	t.Setenv("OPENCLAW_GATEWAY_TOKEN", "test-gateway-token")
	t.Setenv("OPENCLAW_AGENT_TARGET", "openclaw-agent-target")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--agent", "openclaw",
		"--model", "benchmark-model",
	}, strings.NewReader("inspect the cluster"), &stdout, &stderr, server.Client())
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" {
		t.Fatalf("request = %s %s, want POST /v1/chat/completions", gotMethod, gotPath)
	}
	if gotAuth != "Bearer test-gateway-token" {
		t.Fatalf("authorization = %q, want bearer token", gotAuth)
	}
	if gotBody["model"] != "openclaw-agent-target" {
		t.Fatalf("request model = %#v, want openclaw-agent-target", gotBody["model"])
	}
	if !strings.Contains(stdout.String(), "cluster is healthy") {
		t.Fatalf("stdout = %q, want gateway response", stdout.String())
	}
}

func TestRunHermesUsesUnifiedGatewayEnvironment(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hermes response"}}]}`)
	}))
	defer server.Close()
	t.Setenv("HERMES_BASE_URL", server.URL+"/v1")
	t.Setenv("HERMES_GATEWAY_TOKEN", "hermes-gateway-token")
	t.Setenv("HERMES_AGENT_TARGET", "hermes-agent-target")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--agent", "hermes",
		"--model", "benchmark-model",
	}, strings.NewReader("inspect the cluster"), &stdout, &stderr, server.Client())
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" {
		t.Fatalf("request = %s %s, want POST /v1/chat/completions", gotMethod, gotPath)
	}
	if gotAuth != "Bearer hermes-gateway-token" {
		t.Fatalf("authorization = %q, want bearer token", gotAuth)
	}
	if gotBody["model"] != "hermes-agent-target" {
		t.Fatalf("request model = %#v, want hermes-agent-target", gotBody["model"])
	}
	messages, ok := gotBody["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v, want system and user messages", gotBody["messages"])
	}
	if !strings.Contains(stdout.String(), "hermes response") {
		t.Fatalf("stdout = %q, want gateway response", stdout.String())
	}
}

func TestRunHermesSupportsLegacyEnvironmentNames(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()
	t.Setenv("COPILOT_HERMES_BASE_URL", server.URL)
	t.Setenv("COPILOT_HERMES_API_KEY", "legacy-token")
	t.Setenv("COPILOT_HERMES_MODEL", "legacy-agent-target")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--agent", "hermes"}, strings.NewReader("prompt"), &stdout, &stderr, server.Client())
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if gotBody["model"] != "legacy-agent-target" {
		t.Fatalf("request model = %#v, want legacy-agent-target", gotBody["model"])
	}
}

func TestRunOpenClawPrefersNewEnvironmentNames(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()
	t.Setenv("OPENCLAW_BASE_URL", server.URL)
	t.Setenv("OPENCLAW_GATEWAY_TOKEN", "new-token")
	t.Setenv("OPENCLAW_AGENT_TARGET", "new-agent-target")
	t.Setenv("OPENCLAW_API_KEY", "legacy-token")
	t.Setenv("OPENCLAW_MODEL", "legacy-session")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--agent", "openclaw"}, strings.NewReader("prompt"), &stdout, &stderr, server.Client())
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if gotAuth != "Bearer new-token" || gotBody["model"] != "new-agent-target" {
		t.Fatalf("request auth/model = %q/%#v, want new values", gotAuth, gotBody["model"])
	}
}

func TestRunOpenClawSupportsLegacyEnvironmentNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()
	t.Setenv("OPENCLAW_BASE_URL", server.URL)
	t.Setenv("OPENCLAW_GATEWAY_TOKEN", "")
	t.Setenv("OPENCLAW_AGENT_TARGET", "")
	t.Setenv("OPENCLAW_API_KEY", "legacy-token")
	t.Setenv("OPENCLAW_MODEL", "legacy-session")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--agent", "openclaw"}, strings.NewReader("prompt"), &stdout, &stderr, server.Client())
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunOpenClawSupportsInsecureTLSForInternalGateway(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"tls ok"}}]}`)
	}))
	defer server.Close()
	t.Setenv("OPENCLAW_BASE_URL", server.URL)
	t.Setenv("OPENCLAW_GATEWAY_TOKEN", "gateway-token")
	t.Setenv("OPENCLAW_AGENT_TARGET", "openclaw-agent-target")
	t.Setenv("OPENCLAW_INSECURE_SKIP_VERIFY", "true")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--agent", "openclaw"}, strings.NewReader("prompt"), &stdout, &stderr, http.DefaultClient)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "tls ok") {
		t.Fatalf("stdout = %q, want TLS gateway response", stdout.String())
	}
}

func TestRunOpenClawPropagatesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"gateway unavailable"}}`, http.StatusBadGateway)
	}))
	defer server.Close()
	t.Setenv("OPENCLAW_BASE_URL", server.URL)
	t.Setenv("OPENCLAW_GATEWAY_TOKEN", "")
	t.Setenv("OPENCLAW_AGENT_TARGET", "")
	t.Setenv("OPENCLAW_MODEL", "openclaw-agent")

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--agent", "openclaw"}, strings.NewReader("prompt"), &stdout, &stderr, server.Client())
	if err == nil || !strings.Contains(err.Error(), "gateway unavailable") {
		t.Fatalf("run error = %v, want gateway error", err)
	}
}

func writeFakeAgent(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
