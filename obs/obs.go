// Package obs is a tiny OpenTelemetry-shaped tracer (JSONL / nop).
// The agent loop depends only on Tracer/Span; exporters vary.
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

type ctxKey struct{}

// Span is one timed operation.
type Span interface {
	End()
	Set(key string, val any)
	RecordError(error)
}

// Tracer starts spans. Nil-safe wrappers live on Agent.
type Tracer interface {
	Start(ctx context.Context, name string) (context.Context, Span)
}

type nopSpan struct{}

func (nopSpan) End()              {}
func (nopSpan) Set(string, any)   {}
func (nopSpan) RecordError(error) {}

type nopTracer struct{}

func (nopTracer) Start(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, nopSpan{}
}

// Nop returns a tracer that records nothing.
func Nop() Tracer { return nopTracer{} }

// JSONL writes one JSON object per finished span (OTLP-inspired fields).
type JSONL struct {
	mu     sync.Mutex
	out    io.Writer
	closer io.Closer
}

// NewJSONL traces to w. Each line is one span.
func NewJSONL(w io.Writer) *JSONL { return &JSONL{out: w} }

// File opens path append-only for JSONL traces. Caller may Close.
func File(path string) (*JSONL, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &JSONL{out: f, closer: f}, nil
}

// Close closes the underlying file when opened via File.
func (t *JSONL) Close() error {
	if t == nil || t.closer == nil {
		return nil
	}
	return t.closer.Close()
}

type span struct {
	tracer   *JSONL
	traceID  string
	spanID   string
	parentID string
	name     string
	start    time.Time
	attrs    map[string]any
	err      string
	ended    bool
	mu       sync.Mutex
}

func (t *JSONL) Start(ctx context.Context, name string) (context.Context, Span) {
	if t == nil {
		return ctx, nopSpan{}
	}
	parent, _ := ctx.Value(ctxKey{}).(*span)
	sp := &span{
		tracer:  t,
		traceID: newID(16),
		spanID:  newID(8),
		name:    name,
		start:   time.Now().UTC(),
		attrs:   map[string]any{},
	}
	if parent != nil {
		sp.traceID = parent.traceID
		sp.parentID = parent.spanID
	} else if rid := IDFrom(ctx); rid != "" {
		sp.traceID = rid
		sp.attrs["request_id"] = rid
	}
	if rid := IDFrom(ctx); rid != "" {
		sp.attrs["request_id"] = rid
	}
	if runID := RunIDFrom(ctx); runID != "" {
		sp.attrs["run_id"] = runID
	}
	ctx = context.WithValue(ctx, ctxKey{}, sp)
	return ctx, sp
}

func (s *span) Set(key string, val any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.attrs[key] = val
	s.mu.Unlock()
}

func (s *span) RecordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.err = err.Error()
	s.mu.Unlock()
}

func (s *span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	rec := map[string]any{
		"trace_id":       s.traceID,
		"span_id":        s.spanID,
		"parent_span_id": s.parentID,
		"name":           s.name,
		"start":          s.start.Format(time.RFC3339Nano),
		"end":            time.Now().UTC().Format(time.RFC3339Nano),
		"duration_ms":    time.Since(s.start).Milliseconds(),
		"attrs":          s.attrs,
	}
	if s.err != "" {
		rec["error"] = s.err
	}
	s.mu.Unlock()
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	data = append(data, '\n')
	s.tracer.mu.Lock()
	_, _ = s.tracer.out.Write(data)
	s.tracer.mu.Unlock()
}

func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString(make([]byte, n))
	}
	return hex.EncodeToString(b)
}
