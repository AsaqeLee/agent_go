package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/asaqelee/agent_go/obs"
	"github.com/asaqelee/agent_go/tool"
)

var approvalTTL = 5 * time.Minute

type approvalNotifyKey struct{}

// WithApprovalNotify attaches a per-request callback so concurrent streams
// do not clobber a process-global OnAsk.
func WithApprovalNotify(ctx context.Context, fn func(id, runID string, req tool.Approval)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalNotifyKey{}, fn)
}

func approvalNotifyFrom(ctx context.Context) func(id, runID string, req tool.Approval) {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(approvalNotifyKey{}).(func(id, runID string, req tool.Approval))
	return fn
}

type pendingApproval struct {
	ch       chan bool
	req      tool.Approval
	runID    string
	deadline time.Time
}

// Park holds tool calls until Decide is invoked (HTTP approval).
type Park struct {
	mu    sync.Mutex
	wait  map[string]pendingApproval
	OnAsk func(id string, req tool.Approval) // optional process-wide hook (metrics)
}

func NewPark() *Park {
	return &Park{wait: map[string]pendingApproval{}}
}

func (p *Park) Approve(ctx context.Context, req tool.Approval) (bool, error) {
	id := newID()
	ch := make(chan bool, 1)
	runID := obs.RunIDFrom(ctx)
	deadline := time.Now().Add(approvalTTL)
	p.mu.Lock()
	p.wait[id] = pendingApproval{ch: ch, req: req, runID: runID, deadline: deadline}
	on := p.OnAsk
	p.mu.Unlock()
	if n := approvalNotifyFrom(ctx); n != nil {
		n(id, runID, req)
	}
	if on != nil {
		on(id, req)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		p.drop(id)
		return false, ctx.Err()
	case <-timer.C:
		p.drop(id)
		return false, fmt.Errorf("approval expired")
	case v := <-ch:
		return v, nil
	}
}

func (p *Park) drop(id string) {
	p.mu.Lock()
	delete(p.wait, id)
	p.mu.Unlock()
}

// Decide resolves a pending approval. Unknown or expired ids error.
func (p *Park) Decide(id string, allow bool) error {
	p.mu.Lock()
	pend, ok := p.wait[id]
	if ok {
		delete(p.wait, id)
	}
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown approval id")
	}
	if time.Now().After(pend.deadline) {
		return fmt.Errorf("approval expired")
	}
	pend.ch <- allow
	return nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
