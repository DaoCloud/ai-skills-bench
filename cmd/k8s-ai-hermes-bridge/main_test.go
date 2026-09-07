package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRunMapsBenchArgsToHermesRequest(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")
	t.Setenv("COPILOT_HERMES_MODEL", "hermes-agent")

	var gotAuth string
	var gotPath string
	var gotRequest chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"BENCH_OK"}}]}`)
	}))
	defer server.Close()
	t.Setenv("COPILOT_HERMES_BASE_URL", server.URL+"/v1")

	tracePath := filepath.Join(t.TempDir(), "trace.json")
	var out strings.Builder
	err := run(context.Background(), []string{
		"--kubeconfig", "/tmp/kubeconfig.yaml",
		"--llm-provider", "openai",
		"--model", "qwen/qwen3-coder",
		"--trace-path", tracePath,
		"--quiet=true",
		"--enable-tool-use-shim=false",
		"--skip-permissions",
		"--show-tool-output",
		"--mcp-client=false",
	}, strings.NewReader("first prompt\nsecond prompt\n"), &out, server.Client())
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-test-key" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotRequest.Model != "hermes-agent" {
		t.Fatalf("request model = %q", gotRequest.Model)
	}
	if gotRequest.Messages[1].Content != "first prompt\n\nsecond prompt" {
		t.Fatalf("user content = %q", gotRequest.Messages[1].Content)
	}
	if !strings.Contains(gotRequest.Messages[0].Content, "benchmark_model: qwen/qwen3-coder") {
		t.Fatalf("system prompt missing benchmark model: %s", gotRequest.Messages[0].Content)
	}
	if strings.TrimSpace(out.String()) != "BENCH_OK" {
		t.Fatalf("stdout = %q", out.String())
	}

	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	traceText := string(traceData)
	if strings.Contains(traceText, "secret-test-key") {
		t.Fatalf("trace leaked API key: %s", traceText)
	}
	if !strings.Contains(traceText, `"backend": "hermes-api"`) {
		t.Fatalf("trace missing backend: %s", traceText)
	}
	if !strings.Contains(traceText, `"requestModel": "hermes-agent"`) {
		t.Fatalf("trace missing request model: %s", traceText)
	}
}

func TestRunRetriesTransientConnectionErrors(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")
	t.Setenv("COPILOT_HERMES_BASE_URL", "http://hermes.example:30145/v1")
	t.Setenv("COPILOT_HERMES_RETRY_ATTEMPTS", "3")
	t.Setenv("COPILOT_HERMES_RETRY_DELAY", "0s")

	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("dial tcp 10.6.216.81:30145: connect: connection refused")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK_AFTER_RETRY"}}]}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	var out strings.Builder
	err := run(context.Background(), []string{
		"--llm-provider", "openai",
		"--model", "bench/model",
	}, strings.NewReader("prompt\n"), &out, client)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if strings.TrimSpace(out.String()) != "OK_AFTER_RETRY" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunReturnsLastConnectionErrorAfterRetries(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")
	t.Setenv("COPILOT_HERMES_BASE_URL", "http://hermes.example:30145/v1")
	t.Setenv("COPILOT_HERMES_RETRY_ATTEMPTS", "2")
	t.Setenv("COPILOT_HERMES_RETRY_DELAY", "0s")

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp 10.6.216.81:30145: connect: connection refused")
		}),
	}

	err := run(context.Background(), []string{
		"--llm-provider", "openai",
		"--model", "bench/model",
	}, strings.NewReader("prompt\n"), io.Discard, client)
	if err == nil {
		t.Fatal("expected retry error")
	}
	if !strings.Contains(err.Error(), "failed after 2 attempt") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCanUseBenchmarkModelWhenEnabled(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")
	t.Setenv("COPILOT_HERMES_USE_BENCH_MODEL", "true")

	var gotRequest chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer server.Close()
	t.Setenv("COPILOT_HERMES_BASE_URL", server.URL+"/v1")

	err := run(context.Background(), []string{
		"--llm-provider", "openai",
		"--model", "bench/model",
	}, strings.NewReader("prompt\n"), io.Discard, server.Client())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotRequest.Model != "bench/model" {
		t.Fatalf("request model = %q", gotRequest.Model)
	}
}

func TestRunWritesDefaultTraceInTaskOutputDir(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")
	taskOutputDir := t.TempDir()
	t.Setenv("K8S_AI_BENCH_TASK_OUTPUT_DIR", taskOutputDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer server.Close()
	t.Setenv("COPILOT_HERMES_BASE_URL", server.URL)

	err := run(context.Background(), []string{
		"--llm-provider", "openai",
		"--model", "bench/model",
	}, strings.NewReader("prompt\n"), io.Discard, server.Client())
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	traceData, err := os.ReadFile(filepath.Join(taskOutputDir, "hermes-trace.json"))
	if err != nil {
		t.Fatalf("read default trace: %v", err)
	}
	traceText := string(traceData)
	if !strings.Contains(traceText, `"backend": "hermes-api"`) {
		t.Fatalf("trace missing backend: %s", traceText)
	}
	if !strings.Contains(traceText, `"apiUrl": "`+server.URL+`/v1/chat/completions"`) {
		t.Fatalf("trace missing normalized API URL: %s", traceText)
	}
}

func TestRunReturnsErrorOnHTTPFailure(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer server.Close()
	t.Setenv("COPILOT_HERMES_BASE_URL", server.URL+"/v1")

	err := run(context.Background(), []string{
		"--llm-provider", "openai",
		"--model", "bench/model",
	}, strings.NewReader("prompt\n"), io.Discard, server.Client())
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected HTTP 401 error, got %v", err)
	}
}

func TestRunReturnsHTTPFailureForNonJSONError(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "404: page not found")
	}))
	defer server.Close()
	t.Setenv("COPILOT_HERMES_BASE_URL", server.URL+"/v1")

	err := run(context.Background(), []string{
		"--llm-provider", "openai",
		"--model", "bench/model",
	}, strings.NewReader("prompt\n"), io.Discard, server.Client())
	if err == nil || !strings.Contains(err.Error(), "Hermes API HTTP 404") {
		t.Fatalf("expected HTTP 404 error, got %v", err)
	}
	if strings.Contains(err.Error(), "invalid Hermes response JSON") {
		t.Fatalf("HTTP error should not be masked by JSON parsing: %v", err)
	}
}

func TestRunAcceptsFullChatCompletionsEndpoint(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "secret-test-key")

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer server.Close()
	t.Setenv("COPILOT_HERMES_BASE_URL", server.URL+"/v1/chat/completions")

	err := run(context.Background(), []string{
		"--llm-provider", "openai",
		"--model", "bench/model",
	}, strings.NewReader("prompt\n"), io.Discard, server.Client())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestHermesChatCompletionsURLAcceptsBaseOrEndpoint(t *testing.T) {
	tests := map[string]string{
		"http://127.0.0.1:8642":                            "http://127.0.0.1:8642/v1/chat/completions",
		"http://127.0.0.1:8642/v1":                         "http://127.0.0.1:8642/v1/chat/completions",
		"http://127.0.0.1:8642/v1/":                        "http://127.0.0.1:8642/v1/chat/completions",
		"http://hermes.example:31273":                      "http://hermes.example:31273/v1/chat/completions",
		"http://hermes.example:31273/v1/chat/completions":  "http://hermes.example:31273/v1/chat/completions",
		"http://hermes.example:31273/v1/chat/completions/": "http://hermes.example:31273/v1/chat/completions",
	}

	for input, want := range tests {
		if got := hermesChatCompletionsURL(input); got != want {
			t.Fatalf("hermesChatCompletionsURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLiveHermesEnvironment(t *testing.T) {
	if os.Getenv("COPILOT_HERMES_LIVE_TEST") == "" {
		t.Skip("set COPILOT_HERMES_LIVE_TEST=1 with COPILOT_HERMES_BASE_URL, COPILOT_HERMES_API_KEY, and COPILOT_HERMES_MODEL to run")
	}
	if os.Getenv("COPILOT_HERMES_BASE_URL") == "" {
		t.Fatal("COPILOT_HERMES_BASE_URL is required for live test")
	}
	if os.Getenv("COPILOT_HERMES_API_KEY") == "" {
		t.Fatal("COPILOT_HERMES_API_KEY is required for live test")
	}
	if os.Getenv("COPILOT_HERMES_MODEL") == "" {
		t.Fatal("COPILOT_HERMES_MODEL is required for live test")
	}
	t.Setenv("COPILOT_HERMES_TIMEOUT", "120s")

	tracePath := filepath.Join(t.TempDir(), "live-trace.json")
	var out strings.Builder
	err := run(context.Background(), []string{
		"--kubeconfig", "/tmp/k8s-ai-hermes-bridge-live-kubeconfig.yaml",
		"--llm-provider", "openai",
		"--model", os.Getenv("COPILOT_HERMES_MODEL"),
		"--trace-path", tracePath,
		"--quiet=true",
		"--enable-tool-use-shim=false",
		"--skip-permissions",
		"--show-tool-output",
		"--mcp-client=false",
	}, strings.NewReader("请只回复 K8S_AI_HERMES_BRIDGE_LIVE_OK，不要输出其他内容。\n"), &out, http.DefaultClient)
	if err != nil {
		t.Fatalf("live run: %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("live response is empty")
	}

	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read live trace: %v", err)
	}
	traceText := string(traceData)
	if strings.Contains(traceText, os.Getenv("COPILOT_HERMES_API_KEY")) {
		t.Fatal("live trace leaked COPILOT_HERMES_API_KEY")
	}
	if !strings.Contains(traceText, `"backend": "hermes-api"`) {
		t.Fatalf("live trace missing backend: %s", traceText)
	}
}

func TestParseConfigRequiresAPIKey(t *testing.T) {
	t.Setenv("COPILOT_HERMES_API_KEY", "")
	_, err := parseConfig([]string{"--model", "bench/model"})
	if err == nil || !strings.Contains(err.Error(), "COPILOT_HERMES_API_KEY") {
		t.Fatalf("expected API key error, got %v", err)
	}
}
