package llm

import (
	"context"
	"errors"
	"strings"
)

// Purpose selects which model slot a Request should use.
type Purpose string

const (
	PurposeChat    Purpose = ""
	PurposeSummary Purpose = "summary"
)

// Router implements Provider (and Streamer when the primary does).
type Router struct {
	Primary  Provider
	Fallback Provider
	Summary  Provider
}

// Chat sends to Summary when Purpose is summary; otherwise Primary with one
// retryable fallback onto Fallback.
func (r *Router) Chat(ctx context.Context, req Request) (Response, error) {
	if r == nil || r.Primary == nil {
		return Response{}, errors.New("llm: router primary is nil")
	}
	if req.Purpose == PurposeSummary && r.Summary != nil {
		return r.Summary.Chat(ctx, req)
	}
	resp, err := r.Primary.Chat(ctx, req)
	if err != nil && r.Fallback != nil && isRetryableChatErr(err) {
		return r.Fallback.Chat(ctx, req)
	}
	return resp, err
}

// ChatStream uses Primary's Streamer when available; retryable failures fall
// back to Fallback.Chat (no token deltas).
func (r *Router) ChatStream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	if r == nil || r.Primary == nil {
		return Response{}, errors.New("llm: router primary is nil")
	}
	if req.Purpose == PurposeSummary && r.Summary != nil {
		return r.Summary.Chat(ctx, req)
	}
	if s, ok := r.Primary.(Streamer); ok {
		resp, err := s.ChatStream(ctx, req, onDelta)
		if err != nil && r.Fallback != nil && isRetryableChatErr(err) {
			return r.Fallback.Chat(ctx, req)
		}
		return resp, err
	}
	return r.Chat(ctx, req)
}

func isRetryableChatErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Retryable()
	}
	s := err.Error()
	if strings.Contains(s, "status 429") || strings.Contains(s, "llm: http:") {
		return true
	}
	for _, code := range []string{"status 500", "status 502", "status 503", "status 504"} {
		if strings.Contains(s, code) {
			return true
		}
	}
	return false
}
