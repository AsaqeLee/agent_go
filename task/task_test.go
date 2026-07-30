package task

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSubmitSucceeds(t *testing.T) {
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		return "ok:" + goal, nil
	}, Options{Workers: 1})
	defer m.Stop()

	tk, err := m.Submit("hello")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != StatusQueued && tk.Status != StatusRunning && tk.Status != StatusSucceeded {
		t.Fatalf("status=%s", tk.Status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done, err := m.Wait(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusSucceeded || done.Result != "ok:hello" {
		t.Fatalf("%+v", done)
	}
}

func TestSubmitFails(t *testing.T) {
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		return "", errors.New("boom")
	}, Options{Workers: 1})
	defer m.Stop()

	tk, err := m.Submit("x")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done, err := m.Wait(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusFailed || !strings.Contains(done.Error, "boom") {
		t.Fatalf("%+v", done)
	}
}

func TestCancelQueued(t *testing.T) {
	block := make(chan struct{})
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		<-block // first task blocks worker
		return "done", nil
	}, Options{Workers: 1, QueueSize: 8})
	defer func() {
		close(block)
		m.Stop()
	}()

	first, err := m.Submit("block")
	if err != nil {
		t.Fatal(err)
	}
	// ensure first is running
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		t1, _ := m.Get(first.ID)
		if t1.Status == StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	second, err := m.Submit("should-cancel")
	if err != nil {
		t.Fatal(err)
	}
	// cancel while still queued (worker busy with first)
	got, err := m.Cancel(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCancelled {
		// may race to running if first finished; accept cancelled only for this test setup
		t2, _ := m.Get(second.ID)
		if t2.Status != StatusCancelled {
			t.Fatalf("want cancelled, got %+v", t2)
		}
	}
}

func TestCancelRunning(t *testing.T) {
	started := make(chan struct{})
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}, Options{Workers: 1})
	defer m.Stop()

	tk, err := m.Submit("long")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner not started")
	}
	if _, err := m.Cancel(tk.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done, err := m.Wait(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusCancelled {
		t.Fatalf("%+v", done)
	}
}

func TestListAndGet(t *testing.T) {
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		return goal, nil
	}, Options{Workers: 2})
	defer m.Stop()

	a, _ := m.Submit("a")
	b, _ := m.Submit("b")
	list := m.List()
	if len(list) < 2 {
		t.Fatalf("list=%d", len(list))
	}
	if _, ok := m.Get(a.ID); !ok {
		t.Fatal("missing a")
	}
	if _, ok := m.Get(b.ID); !ok {
		t.Fatal("missing b")
	}
	if _, ok := m.Get("nope"); ok {
		t.Fatal("unexpected")
	}
}

func TestEmptyGoal(t *testing.T) {
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		return "", nil
	}, Options{})
	defer m.Stop()
	if _, err := m.Submit("  "); err == nil {
		t.Fatal("expected error")
	}
}
