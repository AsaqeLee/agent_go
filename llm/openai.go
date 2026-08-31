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

// DefaultMaxRetries is used when OpenAI.MaxRetries is 0 (unset).
// Negative MaxRetries disables retries.
const DefaultMaxRetries = 2

// OpenAI is an OpenAI-compatible chat provider.
// Works with OpenAI, Ollama, DeepSeek, vLLM, and any service that implements
// POST /v1/chat/completions.
type OpenAI struct {
	BaseURL string // e.g. https://api.openai.com/v1 or http://localhost:11434/v1
	APIKey  string
	Model   string
	// EmbedModel is used by Embed(). Empty → text-embedding-3-small.
	EmbedModel string
	HTTPClient *http.Client
	// MaxRetries is extra attempts after the first try for 429 / 5xx / transport errors.
	// 0 → DefaultMaxRetries; negative → no retries.
	MaxRetries int
	// Sleep waits between retries. Nil uses time.Sleep (inject in tests).
	Sleep func(time.Duration)
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
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
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

	attempts := 1 + o.retryBudget()
	var lastErr error
	var lastResp Response
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return Response{}, lastErr
			}
			return Response{}, fmt.Errorf("llm: %w", err)
		}
		resp, retryable, err := o.doChat(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		lastResp = resp
		if !retryable || attempt == attempts-1 {
			return lastResp, err
		}
		if err := ctx.Err(); err != nil {
			return lastResp, fmt.Errorf("llm: %w", err)
		}
		o.sleep(backoff(attempt))
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("llm: exhausted retries")
	}
	return lastResp, lastErr
}

func (o *OpenAI) retryBudget() int {
	if o.MaxRetries < 0 {
		return 0
	}
	if o.MaxRetries == 0 {
		return DefaultMaxRetries
	}
	return o.MaxRetries
}

func (o *OpenAI) sleep(d time.Duration) {
	if o.Sleep != nil {
		o.Sleep(d)
		return
	}
	time.Sleep(d)
}

func backoff(attempt int) time.Duration {
	// 200ms, 400ms, 800ms… capped at 2s. Tests inject Sleep so this stays cheap.
	d := 200 * time.Millisecond
	for i := 0; i < attempt && d < 2*time.Second; i++ {
		d *= 2
	}
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	return d
}

func (o *OpenAI) doChat(ctx context.Context, body []byte) (Response, bool, error) {
	url := o.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	httpResp, err := o.HTTPClient.Do(httpReq)
	if err != nil {
		retryable := ctx.Err() == nil
		return Response{}, retryable, fmt.Errorf("llm: http: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		retryable := ctx.Err() == nil && isRetryableStatus(httpResp.StatusCode)
		return Response{}, retryable, fmt.Errorf("llm: read body: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		var parsed chatCompletionsResponse
		_ = json.Unmarshal(raw, &parsed)
		u := usageFrom(&parsed)
		return Response{Usage: u}, isRetryableStatus(httpResp.StatusCode), httpStatusError(httpResp.StatusCode, raw)
	}

	var parsed chatCompletionsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, false, fmt.Errorf("llm: unmarshal: %w\nbody: %s", err, truncate(string(raw), 500))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Response{Usage: usageFrom(&parsed)}, false, fmt.Errorf("llm: api error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Response{Usage: usageFrom(&parsed)}, false, fmt.Errorf("llm: empty choices")
	}

	msg := parsed.Choices[0].Message
	for i := range msg.ToolCalls {
		if msg.ToolCalls[i].Type == "" {
			msg.ToolCalls[i].Type = "function"
		}
	}
	return Response{Message: msg, Usage: usageFrom(&parsed)}, false, nil
}

func usageFrom(parsed *chatCompletionsResponse) Usage {
	if parsed == nil || parsed.Usage == nil {
		return Usage{}
	}
	u := Usage{
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		TotalTokens:      parsed.Usage.TotalTokens,
		Calls:            1,
	}
	if u.TotalTokens == 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	return u
}

func httpStatusError(code int, raw []byte) error {
	msg := ""
	var parsed chatCompletionsResponse
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Error != nil && parsed.Error.Message != "" {
		msg = parsed.Error.Message
	} else {
		msg = truncate(string(raw), 500)
	}
	return &StatusError{Status: code, Msg: msg}
}

func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
