package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatStreamTokensAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Errorf("expected stream=true in %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "", "m")
	var got []string
	resp, err := p.ChatStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, func(d Delta) { got = append(got, d.Content) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "Hello" {
		t.Fatalf("content=%q", resp.Message.Content)
	}
	if strings.Join(got, "") != "Hello" {
		t.Fatalf("deltas=%v", got)
	}
	if resp.Usage.TotalTokens != 5 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

func TestChatStreamAssemblesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"calculator","arguments":""}}]}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"expression\":\"2+3\"}"}}]}}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "", "m")
	resp, err := p.ChatStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "2+3"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls=%+v", resp.Message.ToolCalls)
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "c1" || tc.Function.Name != "calculator" || tc.Function.Arguments != `{"expression":"2+3"}` {
		t.Fatalf("%+v", tc)
	}
}
