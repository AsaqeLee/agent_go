// Package run tracks one Agent.Run as a first-class job: id, session mutex, cancel.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/obs"
)

// Status is the Run state machine.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

const defaultMaxRecords = 512

// Record is a snapshot of one Run.
type Record struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	RequestID  string    `json:"request_id,omitempty"`
	Input      string    `json:"input,omitempty"`
	Output     string    `json:"output,omitempty"`
	Err        string    `json:"error,omitempty"`
	Status     Status    `json:"status"`
	ChatError  bool      `json:"chat_error,omitempty"`
	ChannelErr string    `json:"channel_error,omitempty"`
	Usage      llm.Usage `json:"usage"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// ErrConflict means the session already has a running Run.
var ErrConflict = errors.New("run: session busy")

// ConflictError includes the blocking run id.
type ConflictError struct {
	SessionID string
	RunID     string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("run: session %q busy (run %s)", e.SessionID, e.RunID)
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// Runner executes one user input to completion. rec is the registered snapshot (id assigned).
type Runner func(ctx context.Context, rec Record) (output string, usage llm.Usage, err error)

// Registry stores live and finished runs in process memory.
type Registry struct {
	mu         sync.Mutex
	byID       map[string]*live
	bySession  map[string]string // session → running id
	Timeout    time.Duration     // 0 = no extra deadline
	MaxRecords int               // 0 = defaultMaxRecords; finished runs beyond this are dropped
	// OnDone is invoked after a Run reaches a terminal status (optional).
	OnDone func(Record)
}

type live struct {
	rec    Record
	cancel context.CancelFunc
	done   chan struct{}
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:      map[string]*live{},
		bySession: map[string]string{},
	}
}

func (g *Registry) maxRecords() int {
	if g.MaxRecords <= 0 {
		return defaultMaxRecords
	}
	return g.MaxRecords
}

// Start registers a Run. If wait is true it blocks until terminal status.
// A second Start for the same session while one is running returns ConflictError.
func (g *Registry) Start(ctx context.Context, sessionID, input string, wait bool, fn Runner) (Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = "s_" + strings.TrimPrefix(newID(), "r_")
	}
	if fn == nil {
		return Record{}, fmt.Errorf("run: nil runner")
	}

	parent := ctx
	var cancel context.CancelFunc
	var runCtx context.Context
	if wait {
		runCtx, cancel = context.WithCancel(parent)
	} else {
		// Keep request_id / approval notify; do not cancel when the HTTP handler returns.
		runCtx, cancel = context.WithCancel(context.WithoutCancel(parent))
	}
	if g.Timeout > 0 {
		var tc context.CancelFunc
		runCtx, tc = context.WithTimeout(runCtx, g.Timeout)
		prev := cancel
		cancel = func() {
			tc()
			prev()
		}
	}

	id := newID()
	runCtx = obs.WithRunID(runCtx, id)
	now := time.Now().UTC()
	rec := Record{
		ID:        id,
		SessionID: sessionID,
		RequestID: obs.IDFrom(runCtx),
		Input:     input,
		Status:    StatusRunning,
		StartedAt: now,
	}

	done := make(chan struct{})
	g.mu.Lock()
	if existing, ok := g.bySession[sessionID]; ok {
		g.mu.Unlock()
		cancel()
		return Record{}, &ConflictError{SessionID: sessionID, RunID: existing}
	}
	g.byID[id] = &live{rec: rec, cancel: cancel, done: done}
	g.bySession[sessionID] = id
	g.mu.Unlock()

	go func() {
		defer close(done)
		out, usage, err := fn(runCtx, rec)
		g.finish(id, sessionID, out, usage, err, runCtx)
	}()

	if !wait {
		return rec, nil
	}
	select {
	case <-done:
	case <-parent.Done():
		cancel()
		<-done
	}
	got, ok := g.Get(id)
	if !ok {
		return rec, fmt.Errorf("run: vanished %s", id)
	}
	return got, nil
}

// Get returns a copy of the record.
func (g *Registry) Get(id string) (Record, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	lv, ok := g.byID[id]
	if !ok {
		return Record{}, false
	}
	return lv.rec, true
}

// Cancel requests cancellation and waits until the Run is terminal (or 2s).
func (g *Registry) Cancel(id string) (Record, error) {
	g.mu.Lock()
	lv, ok := g.byID[id]
	if !ok {
		g.mu.Unlock()
		return Record{}, fmt.Errorf("run: not found: %s", id)
	}
	st := lv.rec.Status
	cancel := lv.cancel
	done := lv.done
	snap := lv.rec
	g.mu.Unlock()
	if st != StatusRunning {
		return snap, fmt.Errorf("run: already finished (%s)", st)
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	got, _ := g.Get(id)
	return got, nil
}

// Annotate mutates a stored record (e.g. channel delivery failure after success).
func (g *Registry) Annotate(id string, fn func(*Record)) {
	if g == nil || fn == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	lv, ok := g.byID[id]
	if !ok {
		return
	}
	fn(&lv.rec)
}

// InFlight is the number of runs in StatusRunning.
func (g *Registry) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.bySession)
}

func (g *Registry) finish(id, sessionID, out string, usage llm.Usage, err error, runCtx context.Context) {
	g.mu.Lock()
	lv, ok := g.byID[id]
	if !ok {
		g.mu.Unlock()
		return
	}
	lv.rec.FinishedAt = time.Now().UTC()
	lv.rec.Usage = usage
	if lv.rec.RequestID == "" {
		lv.rec.RequestID = obs.IDFrom(runCtx)
	}
	if runCtx.Err() != nil {
		lv.rec.Status = StatusCancelled
		if err != nil {
			lv.rec.Err = err.Error()
		} else {
			lv.rec.Err = runCtx.Err().Error()
		}
	} else if err != nil {
		lv.rec.Status = StatusFailed
		lv.rec.Err = err.Error()
		lv.rec.ChatError = isChatErr(err)
	} else {
		lv.rec.Status = StatusSucceeded
		lv.rec.Output = out
	}
	if g.bySession[sessionID] == id {
		delete(g.bySession, sessionID)
	}
	g.evictLocked()
	done := lv.rec
	cb := g.OnDone
	g.mu.Unlock()
	if cb != nil {
		cb(done)
	}
}

func (g *Registry) evictLocked() {
	max := g.maxRecords()
	type item struct {
		id string
		t  time.Time
	}
	var finished []item
	for id, lv := range g.byID {
		if lv.rec.Status == StatusRunning {
			continue
		}
		t := lv.rec.FinishedAt
		if t.IsZero() {
			t = lv.rec.StartedAt
		}
		finished = append(finished, item{id: id, t: t})
	}
	extra := len(finished) - max
	if extra <= 0 {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].t.Before(finished[j].t) })
	for i := 0; i < extra; i++ {
		delete(g.byID, finished[i].id)
	}
}

func isChatErr(err error) bool {
	if err == nil {
		return false
	}
	var se *llm.StatusError
	if errors.As(err, &se) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "llm chat") || strings.Contains(s, "llm:")
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("r_%d", time.Now().UnixNano())
	}
	return "r_" + hex.EncodeToString(b[:])
}
