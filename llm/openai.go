package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI is an OpenAI-compatible chat provider.
// Works with OpenAI, Ollama, DeepSeek, vLLM, and any service that implements
// POST /v1/chat/completions.
type OpenAI struct {
	BaseURL    string // e.g. https://api.openai.com/v1 or http://localhost:11434/v1
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// NewOpenAI creates a provider with sensible defaults.
// baseURL empty → OpenAI official endpoint. apiKey may be empty for local Ollama.
func NewOpenAI(baseURL, apiKey, model string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAI{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type chatCompletionsRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
}

type chatCompletionsResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat implements Provider.
func (o *OpenAI) Chat(ctx context.Context, req Request) (Response, error) {
	model := req.Model
	if model == "" {
		model = o.Model
	}
	if model == "" {
		return Response{}, fmt.Errorf("llm: model is required")
	}

	body, err := json.Marshal(chatCompletionsRequest{
		Model:    model,
		Messages: req.Messages,
		Tools:    req.Tools,
	})
	if err != nil {
		return Response{}, fmt.Errorf("llm: marshal request: %w", err)
	}

	url := o.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	httpResp, err := o.HTTPClient.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("llm: http: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("llm: read body: %w", err)
	}

	var parsed chatCompletionsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("llm: unmarshal: %w\nbody: %s", err, truncate(string(raw), 500))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Response{}, fmt.Errorf("llm: api error: %s", parsed.Error.Message)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("llm: status %d: %s", httpResp.StatusCode, truncate(string(raw), 500))
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("llm: empty choices")
	}

	msg := parsed.Choices[0].Message
	for i := range msg.ToolCalls {
		if msg.ToolCalls[i].Type == "" {
			msg.ToolCalls[i].Type = "function"
		}
	}
	return Response{Message: msg}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
