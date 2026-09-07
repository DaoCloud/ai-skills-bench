package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHermesBaseURL = "http://127.0.0.1:8642/v1"
	defaultHermesModel   = "hermes-agent"
	defaultTimeout       = 30 * time.Minute
	defaultRetryAttempts = 5
	defaultRetryDelay    = 2 * time.Second
)

type config struct {
	Kubeconfig        string
	Provider          string
	BenchmarkModel    string
	TracePath         string
	Quiet             bool
	EnableToolShim    bool
	SkipPermissions   bool
	ShowToolOutput    bool
	MCPClient         bool
	HermesBaseURL     string
	HermesAPIKey      string
	HermesModel       string
	Timeout           time.Duration
	RetryAttempts     int
	RetryDelay        time.Duration
	UseBenchmarkModel bool
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
	} `json:"error,omitempty"`
}

type traceFile struct {
	Backend             string   `json:"backend"`
	APIBaseURL          string   `json:"apiBaseUrl"`
	APIURL              string   `json:"apiUrl"`
	RequestModel        string   `json:"requestModel"`
	BenchmarkProvider   string   `json:"benchmarkProvider"`
	BenchmarkModel      string   `json:"benchmarkModel"`
	UseBenchmarkModel   bool     `json:"useBenchmarkModel"`
	KubeconfigPath      string   `json:"kubeconfigPath"`
	PromptSteps         int      `json:"promptSteps"`
	PromptStepExcerpts  []string `json:"promptStepExcerpts,omitempty"`
	SystemPromptExcerpt string   `json:"systemPromptExcerpt,omitempty"`
	ResponseExcerpt     string   `json:"responseExcerpt,omitempty"`
	Error               string   `json:"error,omitempty"`
	Quiet               bool     `json:"quiet"`
	EnableToolShim      bool     `json:"enableToolUseShim"`
	SkipPermissions     bool     `json:"skipPermissions"`
	ShowToolOutput      bool     `json:"showToolOutput"`
	MCPClient           bool     `json:"mcpClient"`
	StartedAt           string   `json:"startedAt"`
	FinishedAt          string   `json:"finishedAt"`
	DurationMillis      int64    `json:"durationMillis"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, http.DefaultClient); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, client *http.Client) error {
	started := time.Now()

	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}

	steps, err := readPromptSteps(stdin)
	if err != nil {
		return err
	}

	requestModel := cfg.HermesModel
	if cfg.UseBenchmarkModel {
		requestModel = cfg.BenchmarkModel
	}

	systemPrompt := buildSystemPrompt(cfg)
	reqBody := chatRequest{
		Model: requestModel,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: strings.Join(steps, "\n\n")},
		},
		Temperature: 0,
	}

	trace := traceFile{
		Backend:             "hermes-api",
		APIBaseURL:          cfg.HermesBaseURL,
		APIURL:              hermesChatCompletionsURL(cfg.HermesBaseURL),
		RequestModel:        requestModel,
		BenchmarkProvider:   cfg.Provider,
		BenchmarkModel:      cfg.BenchmarkModel,
		UseBenchmarkModel:   cfg.UseBenchmarkModel,
		KubeconfigPath:      cfg.Kubeconfig,
		PromptSteps:         len(steps),
		PromptStepExcerpts:  excerpts(steps, 240),
		SystemPromptExcerpt: excerpt(systemPrompt, 300),
		Quiet:               cfg.Quiet,
		EnableToolShim:      cfg.EnableToolShim,
		SkipPermissions:     cfg.SkipPermissions,
		ShowToolOutput:      cfg.ShowToolOutput,
		MCPClient:           cfg.MCPClient,
		StartedAt:           started.UTC().Format(time.RFC3339Nano),
	}

	callCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	answer, err := callHermes(callCtx, client, cfg, reqBody)
	finished := time.Now()
	trace.FinishedAt = finished.UTC().Format(time.RFC3339Nano)
	trace.DurationMillis = finished.Sub(started).Milliseconds()

	if err != nil {
		trace.Error = err.Error()
		_ = writeTrace(cfg.TracePath, trace)
		return err
	}

	trace.ResponseExcerpt = excerpt(answer, 1000)
	if err := writeTrace(cfg.TracePath, trace); err != nil {
		return err
	}

	_, err = fmt.Fprintln(stdout, answer)
	return err
}

func parseConfig(args []string) (config, error) {
	cfg := config{
		HermesBaseURL: envOrDefault("COPILOT_HERMES_BASE_URL", defaultHermesBaseURL),
		HermesAPIKey:  os.Getenv("COPILOT_HERMES_API_KEY"),
		HermesModel:   envOrDefault("COPILOT_HERMES_MODEL", defaultHermesModel),
		Timeout:       defaultTimeout,
		RetryAttempts: defaultRetryAttempts,
		RetryDelay:    defaultRetryDelay,
	}

	if timeoutRaw := os.Getenv("COPILOT_HERMES_TIMEOUT"); timeoutRaw != "" {
		timeout, err := time.ParseDuration(timeoutRaw)
		if err != nil {
			return cfg, fmt.Errorf("invalid COPILOT_HERMES_TIMEOUT: %w", err)
		}
		cfg.Timeout = timeout
	}
	if attemptsRaw := os.Getenv("COPILOT_HERMES_RETRY_ATTEMPTS"); attemptsRaw != "" {
		attempts, err := strconv.Atoi(attemptsRaw)
		if err != nil {
			return cfg, fmt.Errorf("invalid COPILOT_HERMES_RETRY_ATTEMPTS: %w", err)
		}
		cfg.RetryAttempts = attempts
	}
	if retryDelayRaw := os.Getenv("COPILOT_HERMES_RETRY_DELAY"); retryDelayRaw != "" {
		retryDelay, err := time.ParseDuration(retryDelayRaw)
		if err != nil {
			return cfg, fmt.Errorf("invalid COPILOT_HERMES_RETRY_DELAY: %w", err)
		}
		cfg.RetryDelay = retryDelay
	}
	cfg.UseBenchmarkModel = parseBoolEnv("COPILOT_HERMES_USE_BENCH_MODEL")

	fs := flag.NewFlagSet("k8s-ai-hermes-bridge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Kubeconfig, "kubeconfig", "", "kubeconfig path passed by k8s-ai-bench; metadata only")
	fs.StringVar(&cfg.Provider, "llm-provider", "", "benchmark provider metadata")
	fs.StringVar(&cfg.BenchmarkModel, "model", "", "benchmark model metadata")
	fs.StringVar(&cfg.TracePath, "trace-path", "", "trace output path")
	fs.BoolVar(&cfg.Quiet, "quiet", false, "compatibility flag")
	fs.BoolVar(&cfg.EnableToolShim, "enable-tool-use-shim", false, "compatibility flag")
	fs.BoolVar(&cfg.SkipPermissions, "skip-permissions", false, "compatibility flag")
	fs.BoolVar(&cfg.ShowToolOutput, "show-tool-output", false, "compatibility flag")
	fs.BoolVar(&cfg.MCPClient, "mcp-client", false, "compatibility flag")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	if cfg.HermesAPIKey == "" {
		return cfg, errors.New("COPILOT_HERMES_API_KEY is required")
	}
	if cfg.BenchmarkModel == "" {
		return cfg, errors.New("--model is required")
	}
	if cfg.RetryAttempts < 1 {
		return cfg, errors.New("COPILOT_HERMES_RETRY_ATTEMPTS must be at least 1")
	}
	if cfg.RetryDelay < 0 {
		return cfg, errors.New("COPILOT_HERMES_RETRY_DELAY must be non-negative")
	}
	if cfg.Provider == "" {
		cfg.Provider = "unknown"
	}

	cfg.HermesBaseURL = strings.TrimRight(cfg.HermesBaseURL, "/")
	if cfg.TracePath == "" {
		if taskOutputDir := os.Getenv("K8S_AI_BENCH_TASK_OUTPUT_DIR"); taskOutputDir != "" {
			cfg.TracePath = filepath.Join(taskOutputDir, "hermes-trace.json")
		}
	}
	return cfg, nil
}

func readPromptSteps(r io.Reader) ([]string, error) {
	var steps []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		step := strings.TrimSpace(scanner.Text())
		if step != "" {
			steps = append(steps, step)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, errors.New("no prompt steps received on stdin")
	}
	return steps, nil
}

func buildSystemPrompt(cfg config) string {
	return fmt.Sprintf(`You are running token-factory-copilot e2e benchmark.
Skills and CLI tools are already loaded by Hermes Agent through the token-factory-copilot Helm chart.
Use the server-side Hermes Agent environment. Do not assume the runner process can provide local skills, local CLI tools, or local kubeconfig files.
Benchmark metadata:
- provider: %s
- benchmark_model: %s
- kubeconfig: %s (metadata only; the file is not bridged into Hermes)`, cfg.Provider, cfg.BenchmarkModel, cfg.Kubeconfig)
}

func callHermes(ctx context.Context, client *http.Client, cfg config, body chatRequest) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	url := hermesChatCompletionsURL(cfg.HermesBaseURL)
	var lastErr error
	for attempt := 1; attempt <= cfg.RetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+cfg.HermesAPIKey)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt == cfg.RetryAttempts {
				break
			}
			if err := sleepBeforeRetry(ctx, cfg.RetryDelay, attempt); err != nil {
				return "", err
			}
			continue
		}
		return readHermesResponse(resp)
	}

	return "", fmt.Errorf("Hermes API request failed after %d attempt(s): %w", cfg.RetryAttempts, lastErr)
}

func sleepBeforeRetry(ctx context.Context, baseDelay time.Duration, attempt int) error {
	if baseDelay == 0 {
		return nil
	}
	delay := baseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readHermesResponse(resp *http.Response) (string, error) {
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		var parsed chatResponse
		_ = json.Unmarshal(data, &parsed)
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return "", fmt.Errorf("Hermes API HTTP %d: %s", resp.StatusCode, excerpt(msg, 500))
	}

	var parsed chatResponse
	if len(data) > 0 {
		if err := json.Unmarshal(data, &parsed); err != nil {
			return "", fmt.Errorf("invalid Hermes response JSON: %w; body: %s", err, excerpt(string(data), 500))
		}
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("Hermes API error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("Hermes API response has no choices")
	}
	answer := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if answer == "" {
		return "", errors.New("Hermes API response choice has empty message content")
	}
	return answer, nil
}

func hermesChatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	return baseURL + "/chat/completions"
}

func writeTrace(path string, trace traceFile) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func parseBoolEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func excerpts(values []string, limit int) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, excerpt(value, limit))
	}
	return result
}

func excerpt(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
