package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type requestKey struct{}
type runIDKey struct{}

// IDFrom returns the request id stored on ctx, or "".
func IDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(requestKey{}).(string)
	return s
}

// RunIDFrom returns the run id stored on ctx, or "".
func RunIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(runIDKey{}).(string)
	return s
}

// WithRunID returns ctx carrying a run id.
func WithRunID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, id)
}

// WithID returns ctx carrying id (generated if empty).
func WithID(ctx context.Context, id string) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		id = newRequestID()
	}
	return context.WithValue(ctx, requestKey{}, id), id
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte("fallback0"))
	}
	return hex.EncodeToString(b[:])
}
