// Package task provides a minimal in-process async task runner for the agent.
//
//	submit → queued → worker runs Agent → succeeded|failed|cancelled
//
// Callers get a task_id immediately and poll Get/List (or Wait).
package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Status is the task state machine.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Task is a snapshot-friendly async job record.
type Task struct {
	ID         string    `json:"id"`
	Goal       string    `json:"goal"`
	Status     Status    `json:"status"`
	Progress   string    `json:"progress,omitempty"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// Runner executes one goal to completion (typically agent.Agent.Run).
type Runner func(ctx context.Context, goal string) (result string, err error)

// Manager owns an in-memory queue and worker pool.
type Manager struct {
	runner  Runner
	workers int
	queue   chan string

	mu      sync.Mutex
	tasks   map[string]*taskRec
	order   []string // creation order for List
	cancels map[string]context.CancelFunc

	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup
}

type taskRec struct {
	Task
}

// Options configures Manager.
type Options struct {
	Workers   int // default 1
	QueueSize int // default 64
}

// NewManager starts workers immediately. Stop cancels root context and waits.
func NewManager(parent context.Context, runner Runner, opt Options) *Manager {
	if parent == nil {
		parent = context.Background()
	}
	if runner == nil {
		panic("task: runner is nil")
	}
	if opt.Workers <= 0 {
		opt.Workers = 1
	}
	if opt.QueueSize <= 0 {
		opt.QueueSize = 64
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{
		runner:     runner,
		workers:    opt.Workers,
		queue:      make(chan string, opt.QueueSize),
		tasks:      make(map[string]*taskRec),
		cancels:    make(map[string]context.CancelFunc),
		rootCtx:    ctx,
		rootCancel: cancel,
	}
	for i := 0; i < opt.Workers; i++ {
		m.wg.Add(1)
		go m.workerLoop()
	}
	return m
}

// Stop cancels workers and in-flight tasks, then waits for workers to exit.
func (m *Manager) Stop() {
	m.rootCancel()
	m.mu.Lock()
	for id, c := range m.cancels {
		c()
		delete(m.cancels, id)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Submit enqueues a goal and returns a snapshot with status=queued.
func (m *Manager) Submit(goal string) (Task, error) {
	goal = trimGoal(goal)
	if goal == "" {
		return Task{}, fmt.Errorf("task: empty goal")
	}
	id := newID()
	now := time.Now().UTC()
	rec := &taskRec{Task: Task{
		ID:        id,
		Goal:      goal,
		Status:    StatusQueued,
		Progress:  "queued",
		CreatedAt: now,
	}}

	m.mu.Lock()
	m.tasks[id] = rec
	m.order = append(m.order, id)
	m.mu.Unlock()

	select {
	case <-m.rootCtx.Done():
		m.setTerminal(id, StatusFailed, "", "manager stopped")
		return m.mustGet(id), fmt.Errorf("task: manager stopped")
	case m.queue <- id:
		return m.mustGet(id), nil
	default:
		m.setTerminal(id, StatusFailed, "", "queue full")
		return m.mustGet(id), fmt.Errorf("task: queue full")
	}
}

// Get returns a copy of the task or false if unknown.
func (m *Manager) Get(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.tasks[id]
	if !ok {
		return Task{}, false
	}
	return rec.Task, true
}

// List returns tasks newest-last (creation order).
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, 0, len(m.order))
	for _, id := range m.order {
		if rec, ok := m.tasks[id]; ok {
			out = append(out, rec.Task)
		}
	}
	return out
}

// Cancel requests cancellation. Queued tasks flip to cancelled when dequeued;
// running tasks get their context cancelled.
func (m *Manager) Cancel(id string) (Task, error) {
	m.mu.Lock()
	rec, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("task: not found: %s", id)
	}
	switch rec.Status {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		t := rec.Task
		m.mu.Unlock()
		return t, fmt.Errorf("task: already finished (%s)", rec.Status)
	case StatusQueued:
		rec.Status = StatusCancelled
		rec.Progress = "cancelled before start"
		rec.FinishedAt = time.Now().UTC()
		t := rec.Task
		m.mu.Unlock()
		return t, nil
	default: // running
		if c, ok := m.cancels[id]; ok {
			c()
		}
		// status becomes cancelled in worker when ctx ends
		t := rec.Task
		m.mu.Unlock()
		return t, nil
	}
}

// Wait blocks until the task reaches a terminal state or ctx is done.
func (m *Manager) Wait(ctx context.Context, id string) (Task, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		t, ok := m.Get(id)
		if !ok {
			return Task{}, fmt.Errorf("task: not found: %s", id)
		}
		if isTerminal(t.Status) {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return t, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) workerLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case id, ok := <-m.queue:
			if !ok {
				return
			}
			m.runOne(id)
		}
	}
}

func (m *Manager) runOne(id string) {
	m.mu.Lock()
	rec, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	if rec.Status == StatusCancelled {
		m.mu.Unlock()
		return
	}
	if rec.Status != StatusQueued {
		m.mu.Unlock()
		return
	}
	rec.Status = StatusRunning
	rec.Progress = "running"
	rec.StartedAt = time.Now().UTC()
	goal := rec.Goal
	taskCtx, cancel := context.WithCancel(m.rootCtx)
	m.cancels[id] = cancel
	m.mu.Unlock()

	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.cancels, id)
		m.mu.Unlock()
	}()

	result, err := m.runner(taskCtx, goal)

	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok = m.tasks[id]
	if !ok {
		return
	}
	// If user cancelled while running, prefer cancelled.
	if taskCtx.Err() != nil && rec.Status == StatusRunning {
		if err != nil && taskCtx.Err() != nil {
			rec.Status = StatusCancelled
			rec.Error = err.Error()
			rec.Progress = "cancelled"
			rec.FinishedAt = time.Now().UTC()
			return
		}
	}
	if rec.Status == StatusCancelled {
		rec.FinishedAt = time.Now().UTC()
		return
	}
	rec.FinishedAt = time.Now().UTC()
	if err != nil {
		// context cancel
		if taskCtx.Err() != nil {
			rec.Status = StatusCancelled
			rec.Error = err.Error()
			rec.Progress = "cancelled"
			return
		}
		rec.Status = StatusFailed
		rec.Error = err.Error()
		rec.Progress = "failed"
		return
	}
	rec.Status = StatusSucceeded
	rec.Result = result
	rec.Progress = "done"
	rec.Error = ""
}

func (m *Manager) setTerminal(id string, st Status, result, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.tasks[id]
	if !ok {
		return
	}
	rec.Status = st
	rec.Result = result
	rec.Error = errMsg
	rec.FinishedAt = time.Now().UTC()
}

func (m *Manager) mustGet(id string) Task {
	t, _ := m.Get(id)
	return t
}

func isTerminal(s Status) bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func trimGoal(s string) string {
	// keep simple; CLI already trims
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == ' ' || c == '\t' || c == '\n' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t_%d", time.Now().UnixNano())
	}
	return "t_" + hex.EncodeToString(b[:])
}
