package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"google.golang.org/genai"
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type llmClient interface {
	Generate(ctx context.Context, messages []message) (string, error)
}

type geminiClient struct {
	client *genai.Client
	model  string
}

func newGeminiClient(ctx context.Context, model string) (*geminiClient, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  os.Getenv("GEMINI_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}
	return &geminiClient{client: client, model: model}, nil
}

func (c *geminiClient) Generate(ctx context.Context, messages []message) (string, error) {
	var prompt strings.Builder
	for _, msg := range messages {
		prompt.WriteString(strings.ToUpper(msg.Role))
		prompt.WriteString(":\n")
		prompt.WriteString(msg.Content)
		prompt.WriteString("\n\n")
	}
	result, err := c.client.Models.GenerateContent(ctx, c.model, genai.Text(prompt.String()), nil)
	if err != nil {
		return "", err
	}
	if result == nil || len(result.Candidates) == 0 {
		return "", errors.New("empty response from Gemini")
	}
	content := result.Candidates[0].Content
	if content == nil || len(content.Parts) == 0 {
		return "", errors.New("empty response from Gemini")
	}
	text := strings.TrimSpace(content.Parts[0].Text)
	if text == "" {
		return "", errors.New("empty response from Gemini")
	}
	return text, nil
}

type openAIClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
	model      string
}

func newOpenAIClient(model string) *openAIClient {
	baseURL := os.Getenv("OPENAI_API_BASE")
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &openAIClient{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		apiKey:     os.Getenv("OPENAI_API_KEY"),
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
	}
}

func (c *openAIClient) Generate(ctx context.Context, messages []message) (string, error) {
	body, err := json.Marshal(openAIRequest{
		Model:    c.model,
		Messages: messages,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai-compatible request failed with %s: %s", resp.Status, string(data))
	}
	var parsed openAIResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil {
		return "", errors.New(parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("openai-compatible response contained no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func main() {
	ctx := context.Background()
	provider := flag.String("llm-provider", "gemini", "LLM provider: gemini or openai")
	modelName := flag.String("model", "gemini-2.5-pro", "model name")
	maxIterations := flag.Int("max-iterations", 8, "maximum command/final reasoning iterations")
	commandTimeout := flag.Duration("command-timeout", 2*time.Minute, "timeout for each command")
	flag.Parse()

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
		os.Exit(1)
	}

	client, err := buildClient(ctx, *provider, *modelName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating LLM client: %v\n", err)
		os.Exit(1)
	}

	system := `You are a command-line agent.
Use the user's task and available skill instructions to decide which CLI commands to run.
When you need to run a command, output exactly one command block:

<command>
command and arguments here
</command>

When the task is complete, output exactly one final block:

<final>
concise final answer here
</final>

Do not use markdown fences for command blocks. Prefer machine-readable command output flags when the skill asks for them.`

	messages := []message{
		{Role: "system", Content: system},
		{Role: "user", Content: string(input)},
	}

	for i := 0; i < *maxIterations; i++ {
		response, err := client.Generate(ctx, messages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "LLM call failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(response)

		if final := extractTag(response, "final"); final != "" {
			fmt.Println(final)
			return
		}

		command := extractTag(response, "command")
		if command == "" {
			messages = append(messages,
				message{Role: "assistant", Content: response},
				message{Role: "user", Content: "Respond with either a <command> block to inspect further or a <final> block to finish."},
			)
			continue
		}

		result := runCommand(ctx, command, *commandTimeout)
		fmt.Print(result)
		messages = append(messages,
			message{Role: "assistant", Content: response},
			message{Role: "user", Content: result},
		)
	}

	fmt.Fprintf(os.Stderr, "exceeded max iterations %d\n", *maxIterations)
	os.Exit(1)
}

func buildClient(ctx context.Context, provider, modelName string) (llmClient, error) {
	switch provider {
	case "gemini":
		return newGeminiClient(ctx, modelName)
	case "openai":
		return newOpenAIClient(modelName), nil
	default:
		return nil, fmt.Errorf("unsupported --llm-provider %q", provider)
	}
}

func extractTag(s, tag string) string {
	re := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(tag) + `>\s*(.*?)\s*</` + regexp.QuoteMeta(tag) + `>`)
	match := re.FindStringSubmatch(s)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func runCommand(ctx context.Context, command string, timeout time.Duration) string {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "bash", "-lc", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if commandCtx.Err() == context.DeadlineExceeded {
		exitCode = 124
	}

	return fmt.Sprintf(`<command_result>
command: %s
exit_code: %d
stdout:
%s
stderr:
%s
</command_result>
`, command, exitCode, stdout.String(), stderr.String())
}
