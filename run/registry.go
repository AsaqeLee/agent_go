// Package run tracks one Agent.Run as a first-class job: id, session mutex, cancel.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/asaqelee/agent_go/llm"
)

// Status is the Run state machine.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Record is a snapshot of one Run.
type Record struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Input      string    `json:"input,omitempty"`
	Output     string    `json:"output,omitempty"`
	Err        string    `json:"error,omitempty"`
	Status     Status    `json:"status"`
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
	mu        sync.Mutex
	byID      map[string]*live
	bySession map[string]string // session → running id
	// OnDone is invoked after a Run reaches a terminal status (optional).
	OnDone func(Record)
}

type live struct {
	rec    Record
	cancel context.CancelFunc
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:      map[string]*live{},
		bySession: map[string]string{},
	}
}

// Start registers a Run. If wait is true it blocks until terminal status.
// A second Start for the same session while one is running returns ConflictError.
func (g *Registry) Start(ctx context.Context, sessionID, input string, wait bool, fn Runner) (Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		sessionID = "default"
	}
	if fn == nil {
		return Record{}, fmt.Errorf("run: nil runner")
	}

	parent := ctx
	runCtx, cancel := context.WithCancel(parent)
	if !wait {
		runCtx, cancel = context.WithCancel(context.Background())
	}

	id := newID()
	now := time.Now().UTC()
	rec := Record{
		ID:        id,
		SessionID: sessionID,
		Input:     input,
		Status:    StatusRunning,
		StartedAt: now,
	}

	g.mu.Lock()
	if existing, ok := g.bySession[sessionID]; ok {
		g.mu.Unlock()
		cancel()
		return Record{}, &ConflictError{SessionID: sessionID, RunID: existing}
	}
	g.byID[id] = &live{rec: rec, cancel: cancel}
	g.bySession[sessionID] = id
	g.mu.Unlock()

	done := make(chan struct{})
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

// Cancel requests cancellation. Unknown or already-terminal ids error.
func (g *Registry) Cancel(id string) (Record, error) {
	g.mu.Lock()
	lv, ok := g.byID[id]
	if !ok {
		g.mu.Unlock()
		return Record{}, fmt.Errorf("run: not found: %s", id)
	}
	st := lv.rec.Status
	cancel := lv.cancel
	g.mu.Unlock()
	if st != StatusRunning {
		return lv.rec, fmt.Errorf("run: already finished (%s)", st)
	}
	if cancel != nil {
		cancel()
	}
	// Wait briefly so Get sees terminal status in tests; caller may still poll.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := g.Get(id)
		if got.Status != StatusRunning {
			return got, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := g.Get(id)
	return got, nil
}

// InFlight is the number of runs in StatusRunning.
func (g *Registry) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.bySession)
}

func (g *Registry) finish(id, sessionID, out string, usage llm.Usage, err error, runCtx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	lv, ok := g.byID[id]
	if !ok {
		return
	}
	lv.rec.FinishedAt = time.Now().UTC()
	lv.rec.Usage = usage
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
	} else {
		lv.rec.Status = StatusSucceeded
		lv.rec.Output = out
	}
	if g.bySession[sessionID] == id {
		delete(g.bySession, sessionID)
	}
	done := lv.rec
	cb := g.OnDone
	if cb != nil {
		g.mu.Unlock()
		cb(done)
		g.mu.Lock()
	}
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("r_%d", time.Now().UnixNano())
	}
	return "r_" + hex.EncodeToString(b[:])
}
