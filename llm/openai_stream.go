package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatCompletionsStreamRequest struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Tools         []ToolDef      `json:"tools,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
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

// ChatStream implements Streamer for OpenAI-compatible SSE endpoints.
func (o *OpenAI) ChatStream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	model := req.Model
	if model == "" {
		model = o.Model
	}
	if model == "" {
		return Response{}, fmt.Errorf("llm: model is required")
	}

	body, err := json.Marshal(chatCompletionsStreamRequest{
		Model:         model,
		Messages:      req.Messages,
		Tools:         req.Tools,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	})
	if err != nil {
		return Response{}, fmt.Errorf("llm: marshal request: %w", err)
	}

	attempts := 1 + o.retryBudget()
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return Response{}, lastErr
			}
			return Response{}, fmt.Errorf("llm: %w", err)
		}
		resp, retryable, err := o.doStream(ctx, body, onDelta)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable || attempt == attempts-1 {
			return Response{}, err
		}
		if err := ctx.Err(); err != nil {
			return Response{}, fmt.Errorf("llm: %w", err)
		}
		o.sleep(backoff(attempt))
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("llm: exhausted retries")
	}
	return Response{}, lastErr
}

func (o *OpenAI) doStream(ctx context.Context, body []byte, onDelta func(Delta)) (Response, bool, error) {
	url := o.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	httpResp, err := o.HTTPClient.Do(httpReq)
	if err != nil {
		retryable := ctx.Err() == nil
		return Response{}, retryable, fmt.Errorf("llm: http: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		raw, _ := io.ReadAll(httpResp.Body)
		return Response{}, isRetryableStatus(httpResp.StatusCode), httpStatusError(httpResp.StatusCode, raw)
	}

	msg, usage, err := readSSE(httpResp.Body, onDelta)
	if err != nil {
		// Body already started: do not retry (would duplicate side effects / tokens).
		return Response{}, false, err
	}
	return Response{Message: msg, Usage: usage}, false, nil
}

func readSSE(r io.Reader, onDelta func(Delta)) (Message, Usage, error) {
	acc := newStreamAcc()
	sc := bufio.NewScanner(r)
	// Tool-call argument streams can be large.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return Message{}, Usage{}, fmt.Errorf("llm: stream unmarshal: %w", err)
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return Message{}, Usage{}, fmt.Errorf("llm: api error: %s", chunk.Error.Message)
		}
		acc.apply(chunk, onDelta)
	}
	if err := sc.Err(); err != nil {
		return Message{}, Usage{}, fmt.Errorf("llm: stream: %w", err)
	}
	return acc.message(), acc.usage, nil
}

type streamAcc struct {
	role    string
	content strings.Builder
	tools   map[int]*ToolCall
	usage   Usage
}

func newStreamAcc() *streamAcc {
	return &streamAcc{tools: map[int]*ToolCall{}}
}

func (a *streamAcc) apply(chunk streamChunk, onDelta func(Delta)) {
	if chunk.Usage != nil {
		a.usage = Usage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
			Calls:            1,
		}
		if a.usage.TotalTokens == 0 && (a.usage.PromptTokens > 0 || a.usage.CompletionTokens > 0) {
			a.usage.TotalTokens = a.usage.PromptTokens + a.usage.CompletionTokens
		}
	}
	if len(chunk.Choices) == 0 {
		return
	}
	d := chunk.Choices[0].Delta
	if d.Role != "" {
		a.role = d.Role
	}
	if d.Content != "" {
		a.content.WriteString(d.Content)
		if onDelta != nil {
			onDelta(Delta{Content: d.Content})
		}
	}
	for _, tc := range d.ToolCalls {
		cur, ok := a.tools[tc.Index]
		if !ok {
			cur = &ToolCall{Type: "function"}
			a.tools[tc.Index] = cur
		}
		if tc.ID != "" {
			cur.ID = tc.ID
		}
		if tc.Type != "" {
			cur.Type = tc.Type
		}
		if tc.Function.Name != "" {
			cur.Function.Name = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			cur.Function.Arguments += tc.Function.Arguments
		}
	}
}

func (a *streamAcc) message() Message {
	role := Role(a.role)
	if role == "" {
		role = RoleAssistant
	}
	msg := Message{Role: role, Content: a.content.String()}
	if len(a.tools) == 0 {
		return msg
	}
	max := -1
	for i := range a.tools {
		if i > max {
			max = i
		}
	}
	msg.ToolCalls = make([]ToolCall, 0, max+1)
	for i := 0; i <= max; i++ {
		if tc, ok := a.tools[i]; ok {
			if tc.Type == "" {
				tc.Type = "function"
			}
			msg.ToolCalls = append(msg.ToolCalls, *tc)
		}
	}
	return msg
}
