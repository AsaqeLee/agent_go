package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var req chatCompletionsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("request json: %v", err)
		}
		if req.Model != "demo-model" {
			t.Errorf("model=%q", req.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"hi"}}]
		}`)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL+"/v1", "test-key", "demo-model")
	resp, err := p.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "hi" {
		t.Fatalf("content=%q", resp.Message.Content)
	}
}

func TestChatParsesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}
		}`)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "k", "m")
	resp, err := p.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 18 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"model not found"}}`)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "", "missing")
	p.MaxRetries = -1
	_, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("err=%v", err)
	}
}

func TestChatEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "", "m")
	p.MaxRetries = -1
	_, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "empty choices") {
		t.Fatalf("err=%v", err)
	}
}

func TestChatRequiresModel(t *testing.T) {
	p := NewOpenAI("http://127.0.0.1:1", "", "")
	_, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("err=%v", err)
	}
}

func TestChatRetriesOn429ThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"after-retry"}}]
		}`)
	}))
	defer srv.Close()

	var sleeps []time.Duration
	p := NewOpenAI(srv.URL, "", "m")
	p.MaxRetries = 2
	p.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

	resp, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "after-retry" {
		t.Fatalf("content=%q", resp.Message.Content)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits=%d want 2", hits.Load())
	}
	if len(sleeps) != 1 {
		t.Fatalf("sleeps=%v", sleeps)
	}
}

func TestChatDoesNotRetryOn400(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad request"}}`)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "", "m")
	p.MaxRetries = 3
	p.Sleep = func(time.Duration) { t.Fatal("should not sleep on 400") }

	_, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("err=%v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d", hits.Load())
	}
}

func TestChatStopsRetryWhenContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	p := NewOpenAI(srv.URL, "", "m")
	p.MaxRetries = 5
	p.Sleep = func(time.Duration) { cancel() }

	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context") && ctx.Err() == nil {
		t.Fatalf("err=%v ctx=%v", err, ctx.Err())
	}
}

func TestChatNonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "error code: 1033")
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "", "m")
	p.MaxRetries = -1
	_, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "status 403") || !strings.Contains(err.Error(), "1033") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("should not wrap non-JSON HTTP error as unmarshal: %v", err)
	}
}

func TestChatFillsEmptyToolCallType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","function":{"name":"get_time","arguments":"{}"}}]}}]
		}`)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "", "m")
	resp, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "time"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Type != "function" {
		t.Fatalf("tool_calls=%+v", resp.Message.ToolCalls)
	}
}
