package httpapi

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asaqelee/agent_go/tool"
)

func TestParkExpires(t *testing.T) {
	old := approvalTTL
	approvalTTL = 20 * time.Millisecond
	defer func() { approvalTTL = old }()
	p := NewPark()
	ok, err := p.Approve(context.Background(), tool.Approval{Name: "rm"})
	if ok || err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestParkNotifyIsPerContext(t *testing.T) {
	p := NewPark()
	var mu sync.Mutex
	got := map[string]int{}
	notify := func(key string) func(id, runID string, req tool.Approval) {
		return func(id, runID string, req tool.Approval) {
			mu.Lock()
			got[key]++
			mu.Unlock()
			_ = p.Decide(id, true)
		}
	}
	ctxA := WithApprovalNotify(context.Background(), notify("a"))
	ctxB := WithApprovalNotify(context.Background(), notify("b"))
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = p.Approve(ctxA, tool.Approval{Name: "write_a"})
	}()
	go func() {
		defer wg.Done()
		_, _ = p.Approve(ctxB, tool.Approval{Name: "write_b"})
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	mu.Lock()
	defer mu.Unlock()
	if got["a"] != 1 || got["b"] != 1 {
		t.Fatalf("%v", got)
	}
}
