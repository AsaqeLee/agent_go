package task

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSubmitQueueFull(t *testing.T) {
	block := make(chan struct{})
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		<-block
		return "done", nil
	}, Options{Workers: 1, QueueSize: 1})
	defer func() {
		close(block)
		m.Stop()
	}()

	first, err := m.Submit("running")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ := m.Get(first.ID)
		if got.Status == StatusRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := m.Submit("queued"); err != nil {
		t.Fatal(err)
	}
	_, err = m.Submit("overflow")
	if err == nil || !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("want queue full, got %v", err)
	}
}

func TestSubmitQueueFullMarksTaskFailed(t *testing.T) {
	block := make(chan struct{})
	m := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		<-block
		return "done", nil
	}, Options{Workers: 1, QueueSize: 1})
	defer func() {
		close(block)
		m.Stop()
	}()

	first, err := m.Submit("running")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ := m.Get(first.ID)
		if got.Status == StatusRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := m.Submit("queued"); err != nil {
		t.Fatal(err)
	}

	overflow, err := m.Submit("overflow")
	if err == nil {
		t.Fatal("expected queue full")
	}
	if overflow.ID == "" {
		t.Fatal("overflow task should still have an id")
	}
	got, ok := m.Get(overflow.ID)
	if !ok {
		t.Fatal("overflow task missing from manager")
	}
	if got.Status != StatusFailed || !strings.Contains(got.Error, "queue full") {
		t.Fatalf("overflow snapshot=%+v", got)
	}
}
