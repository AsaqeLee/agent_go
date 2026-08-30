package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/asaqelee/agent_go/tool"
)

// Park holds tool calls until Decide is invoked (HTTP approval).
type Park struct {
	mu    sync.Mutex
	wait  map[string]chan bool
	reqs  map[string]tool.Approval
	OnAsk func(id string, req tool.Approval)
}

func NewPark() *Park {
	return &Park{wait: map[string]chan bool{}, reqs: map[string]tool.Approval{}}
}

func (p *Park) Approve(ctx context.Context, req tool.Approval) (bool, error) {
	id := newID()
	ch := make(chan bool, 1)
	p.mu.Lock()
	p.wait[id] = ch
	p.reqs[id] = req
	on := p.OnAsk
	p.mu.Unlock()
	if on != nil {
		on(id, req)
	}
	select {
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.wait, id)
		delete(p.reqs, id)
		p.mu.Unlock()
		return false, ctx.Err()
	case v := <-ch:
		return v, nil
	}
}

// Decide resolves a pending approval. Unknown ids error.
func (p *Park) Decide(id string, allow bool) error {
	p.mu.Lock()
	ch, ok := p.wait[id]
	if ok {
		delete(p.wait, id)
		delete(p.reqs, id)
	}
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown approval id")
	}
	ch <- allow
	return nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
