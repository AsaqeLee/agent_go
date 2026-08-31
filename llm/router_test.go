package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type slot struct {
	name string
	err  error
	hits atomic.Int32
}

func (s *slot) Chat(_ context.Context, req Request) (Response, error) {
	s.hits.Add(1)
	if s.err != nil {
		return Response{}, s.err
	}
	return Response{Message: Message{Role: RoleAssistant, Content: s.name + ":" + string(req.Purpose)}}, nil
}

func TestRouterFallbackOn503(t *testing.T) {
	p := &slot{err: errors.New("llm: status 503: busy")}
	f := &slot{name: "fb"}
	r := &Router{Primary: p, Fallback: f}
	resp, err := r.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "fb:" {
		t.Fatalf("%q", resp.Message.Content)
	}
	if p.hits.Load() != 1 || f.hits.Load() != 1 {
		t.Fatalf("hits p=%d f=%d", p.hits.Load(), f.hits.Load())
	}
}

func TestRouterSummarySlot(t *testing.T) {
	p := &slot{name: "chat"}
	s := &slot{name: "sum"}
	r := &Router{Primary: p, Summary: s}
	resp, err := r.Chat(context.Background(), Request{Purpose: PurposeSummary})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "sum:summary" {
		t.Fatalf("%q", resp.Message.Content)
	}
	if p.hits.Load() != 0 {
		t.Fatal("primary should not be used for summary")
	}
}

func TestRouterDoesNotFallbackOn400(t *testing.T) {
	p := &slot{err: errors.New("llm: status 400: bad request")}
	f := &slot{name: "fb"}
	r := &Router{Primary: p, Fallback: f}
	_, err := r.Chat(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err=%v", err)
	}
	if f.hits.Load() != 0 {
		t.Fatal("fallback should not run on 400")
	}
}

func TestRouterFallbackRealHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()
	primary := NewOpenAI(srv.URL, "", "m1")
	primary.MaxRetries = -1
	fallback := NewOpenAI(srv.URL, "", "m2")
	fallback.MaxRetries = -1
	r := &Router{Primary: primary, Fallback: fallback}
	resp, err := r.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("%q", resp.Message.Content)
	}
}
