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
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Minute

type config struct {
	Agent                string
	Provider             string
	BenchmarkModel       string
	Timeout              time.Duration
	CodexBin             string
	CodexModel           string
	ClaudeBin            string
	ClaudeModel          string
	OpenClawBaseURL      string
	OpenClawGatewayToken string
	OpenClawAgentTarget  string
	OpenClawInsecureTLS  bool
	HermesBaseURL        string
	HermesGatewayToken   string
	HermesAgentTarget    string
	HermesInsecureTLS    bool
}

type gatewayConfig struct {
	Name         string
	BaseURL      string
	GatewayToken string
	AgentTarget  string
	InsecureTLS  bool
	SystemPrompt string
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, http.DefaultClient); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, client *http.Client) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	switch cfg.Agent {
	case "codex":
		return runCLI(callCtx, cfg.CodexBin, codexArgs(cfg.CodexModel), stdin, stdout, stderr, "CODEX_BIN", "codex")
	case "claude":
		return runCLI(callCtx, cfg.ClaudeBin, claudeArgs(cfg.ClaudeModel), stdin, stdout, stderr, "CLAUDE_BIN", "claude")
	case "openclaw":
		return runGateway(callCtx, gatewayConfig{
			Name:         "OpenClaw",
			BaseURL:      cfg.OpenClawBaseURL,
			GatewayToken: cfg.OpenClawGatewayToken,
			AgentTarget:  cfg.OpenClawAgentTarget,
			InsecureTLS:  cfg.OpenClawInsecureTLS,
		}, stdin, stdout, client)
	case "hermes":
		return runGateway(callCtx, gatewayConfig{
			Name:         "Hermes",
			BaseURL:      cfg.HermesBaseURL,
			GatewayToken: cfg.HermesGatewayToken,
			AgentTarget:  cfg.HermesAgentTarget,
			InsecureTLS:  cfg.HermesInsecureTLS,
			SystemPrompt: hermesSystemPrompt,
		}, stdin, stdout, client)
	default:
		return fmt.Errorf("unsupported agent %q", cfg.Agent)
	}
}

func parseConfig(args []string) (config, error) {
	cfg := config{
		CodexBin:             os.Getenv("CODEX_BIN"),
		CodexModel:           os.Getenv("CODEX_MODEL"),
		ClaudeBin:            os.Getenv("CLAUDE_BIN"),
		ClaudeModel:          os.Getenv("CLAUDE_MODEL"),
		OpenClawBaseURL:      firstNonEmpty(os.Getenv("OPENCLAW_BASE_URL"), os.Getenv("OPENCLAW_API_URL")),
		OpenClawGatewayToken: firstNonEmpty(os.Getenv("OPENCLAW_GATEWAY_TOKEN"), os.Getenv("OPENCLAW_API_KEY")),
		OpenClawAgentTarget:  firstNonEmpty(os.Getenv("OPENCLAW_AGENT_TARGET"), os.Getenv("OPENCLAW_SESSION_ID"), os.Getenv("OPENCLAW_MODEL")),
		HermesBaseURL:        firstNonEmpty(os.Getenv("HERMES_BASE_URL"), os.Getenv("COPILOT_HERMES_BASE_URL")),
		HermesGatewayToken:   firstNonEmpty(os.Getenv("HERMES_GATEWAY_TOKEN"), os.Getenv("COPILOT_HERMES_API_KEY")),
		HermesAgentTarget:    firstNonEmpty(os.Getenv("HERMES_AGENT_TARGET"), os.Getenv("COPILOT_HERMES_MODEL"), "hermes-agent"),
		Timeout:              defaultTimeout,
	}
	var err error
	if cfg.OpenClawInsecureTLS, err = parseBoolEnv("OPENCLAW_INSECURE_SKIP_VERIFY"); err != nil {
		return cfg, err
	}
	if cfg.HermesInsecureTLS, err = parseBoolEnv("HERMES_INSECURE_SKIP_VERIFY"); err != nil {
		return cfg, err
	}

	fs := flag.NewFlagSet("k8s-ai-agent-bridge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Agent, "agent", "", "agent connector: codex, claude, openclaw, or hermes")
	fs.StringVar(&cfg.Provider, "llm-provider", "", "benchmark provider metadata")
	fs.StringVar(&cfg.BenchmarkModel, "model", "", "benchmark model metadata")
	fs.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "maximum connector runtime")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	cfg.Agent = normalizeAgent(cfg.Agent)
	if cfg.Agent == "" {
		return cfg, errors.New("--agent is required")
	}
	if cfg.Timeout <= 0 {
		return cfg, errors.New("--timeout must be greater than zero")
	}
	switch cfg.Agent {
	case "openclaw":
		if cfg.OpenClawBaseURL == "" {
			return cfg, errors.New("OPENCLAW_BASE_URL or OPENCLAW_API_URL is required for openclaw")
		}
		if cfg.OpenClawAgentTarget == "" {
			cfg.OpenClawAgentTarget = cfg.BenchmarkModel
		}
		if cfg.OpenClawAgentTarget == "" {
			return cfg, errors.New("OPENCLAW_AGENT_TARGET or --model is required for openclaw")
		}
	case "hermes":
		if cfg.HermesBaseURL == "" {
			return cfg, errors.New("HERMES_BASE_URL or COPILOT_HERMES_BASE_URL is required for hermes")
		}
		if cfg.HermesGatewayToken == "" {
			return cfg, errors.New("HERMES_GATEWAY_TOKEN or COPILOT_HERMES_API_KEY is required for hermes")
		}
	}
	return cfg, nil
}

func normalizeAgent(agent string) string {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "codex", "codex-cli":
		return "codex"
	case "claude", "claude-code", "claude-cli":
		return "claude"
	case "openclaw", "openclaw-gateway":
		return "openclaw"
	case "hermes", "hermes-gateway":
		return "hermes"
	default:
		return strings.ToLower(strings.TrimSpace(agent))
	}
}

func codexArgs(model string) []string {
	args := []string{"exec", "--ephemeral"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return append(args, "-")
}

func claudeArgs(model string) []string {
	args := []string{"-p", "--output-format", "text"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

func runCLI(ctx context.Context, configuredBin string, args []string, stdin io.Reader, stdout, stderr io.Writer, envName, defaultName string) error {
	bin, err := resolveBinary(configuredBin, envName, defaultName)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s connector timed out: %w", defaultName, ctx.Err())
		}
		return fmt.Errorf("running %s CLI: %w", defaultName, err)
	}
	return nil
}

func resolveBinary(configuredBin, envName, defaultName string) (string, error) {
	candidate := strings.TrimSpace(configuredBin)
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv(envName))
	}
	if candidate == "" {
		candidate = defaultName
	}

	bin, err := exec.LookPath(candidate)
	if err != nil {
		return "", fmt.Errorf("%s CLI %q not found (set %s or add it to PATH): %w", defaultName, candidate, envName, err)
	}
	return bin, nil
}

const hermesSystemPrompt = `You are running an end-to-end benchmark through the Hermes Gateway.
Skills and CLI tools are already loaded by the server-side Hermes Agent.
Use the server-side Hermes Agent environment; do not assume the runner process can provide local skills, local CLI tools, or local kubeconfig files.`

func runGateway(ctx context.Context, cfg gatewayConfig, stdin io.Reader, stdout io.Writer, client *http.Client) error {
	prompt, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("reading %s prompt: %w", cfg.Name, err)
	}
	if strings.TrimSpace(string(prompt)) == "" {
		return fmt.Errorf("%s prompt is empty", cfg.Name)
	}

	messages := []chatMessage{{
		Role:    "user",
		Content: string(prompt),
	}}
	if cfg.SystemPrompt != "" {
		messages = append([]chatMessage{{Role: "system", Content: cfg.SystemPrompt}}, messages...)
	}
	body, err := json.Marshal(chatRequest{
		Model:       cfg.AgentTarget,
		Messages:    messages,
		Temperature: 0,
	})
	if err != nil {
		return fmt.Errorf("encoding %s request: %w", cfg.Name, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL(cfg.BaseURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating %s request: %w", cfg.Name, err)
	}
	request.Header.Set("Content-Type", "application/json")
	if cfg.GatewayToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.GatewayToken)
	}

	response, err := openClawHTTPClient(client, cfg.InsecureTLS).Do(request)
	if err != nil {
		return fmt.Errorf("calling %s gateway: %w", cfg.Name, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024))
	if err != nil {
		return fmt.Errorf("reading OpenClaw response: %w", err)
	}

	var parsed chatResponse
	_ = json.Unmarshal(data, &parsed)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if parsed.Error != nil && parsed.Error.Message != "" {
			message = parsed.Error.Message
		}
		return fmt.Errorf("%s gateway returned HTTP %d: %s", cfg.Name, response.StatusCode, message)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return fmt.Errorf("%s gateway error: %s", cfg.Name, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return fmt.Errorf("%s response has no assistant message", cfg.Name)
	}
	_, err = fmt.Fprintln(stdout, strings.TrimSpace(parsed.Choices[0].Message.Content))
	return err
}

func parseBoolEnv(name string) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false, nil
	}
	insecure, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return insecure, nil
}

func openClawHTTPClient(client *http.Client, insecureTLS bool) *http.Client {
	if !insecureTLS || client == nil {
		return client
	}

	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	transport, ok := baseTransport.(*http.Transport)
	if !ok {
		return client
	}
	transport = transport.Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.InsecureSkipVerify = true // #nosec G402 -- explicitly opted in for an internal gateway.
	transport.TLSClientConfig = tlsConfig

	configuredClient := *client
	configuredClient.Transport = transport
	return &configuredClient
}

func chatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/v1/chat/completions"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
