package run

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asaqelee/agent_go/llm"
)

func TestStartWaitSucceeds(t *testing.T) {
	g := NewRegistry()
	rec, err := g.Start(context.Background(), "s1", "hi", true, func(ctx context.Context, rec Record) (string, llm.Usage, error) {
		if rec.ID == "" {
			t.Error("missing id")
		}
		return "hello", llm.Usage{TotalTokens: 3, Calls: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusSucceeded || rec.Output != "hello" || rec.ID == "" {
		t.Fatalf("%+v", rec)
	}
	got, ok := g.Get(rec.ID)
	if !ok || got.Output != "hello" {
		t.Fatalf("%v %+v", ok, got)
	}
	if g.InFlight() != 0 {
		t.Fatalf("in_flight=%d", g.InFlight())
	}
}

func TestSessionConflict(t *testing.T) {
	g := NewRegistry()
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = g.Start(context.Background(), "s1", "one", true, func(ctx context.Context, rec Record) (string, llm.Usage, error) {
			close(started)
			<-release
			return "a", llm.Usage{}, nil
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first run")
	}
	_, err := g.Start(context.Background(), "s1", "two", true, func(ctx context.Context, rec Record) (string, llm.Usage, error) {
		return "b", llm.Usage{}, nil
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v", err)
	}
	close(release)
}

func TestCancelRunning(t *testing.T) {
	g := NewRegistry()
	started := make(chan struct{})
	rec, err := g.Start(context.Background(), "s1", "x", false, func(ctx context.Context, rec Record) (string, llm.Usage, error) {
		close(started)
		<-ctx.Done()
		return "", llm.Usage{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("not started")
	}
	got, err := g.Cancel(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCancelled {
		t.Fatalf("%+v", got)
	}
	if g.InFlight() != 0 {
		t.Fatalf("in_flight=%d", g.InFlight())
	}
}

func TestEvictsOldestFinished(t *testing.T) {
	g := NewRegistry()
	g.MaxRecords = 2
	var ids []string
	for i := 0; i < 3; i++ {
		rec, err := g.Start(context.Background(), "s"+string(rune('a'+i)), "x", true, func(ctx context.Context, rec Record) (string, llm.Usage, error) {
			return "ok", llm.Usage{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}
	if _, ok := g.Get(ids[0]); ok {
		t.Fatal("oldest finished run should be evicted")
	}
	if _, ok := g.Get(ids[1]); !ok {
		t.Fatal("want id 1 kept")
	}
	if _, ok := g.Get(ids[2]); !ok {
		t.Fatal("want id 2 kept")
	}
}

func TestTimeoutCancels(t *testing.T) {
	g := NewRegistry()
	g.Timeout = 30 * time.Millisecond
	rec, err := g.Start(context.Background(), "s", "x", true, func(ctx context.Context, rec Record) (string, llm.Usage, error) {
		<-ctx.Done()
		return "", llm.Usage{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusCancelled {
		t.Fatalf("%+v", rec)
	}
}

func TestStartWaitFailed(t *testing.T) {
	g := NewRegistry()
	rec, err := g.Start(context.Background(), "s", "x", true, func(ctx context.Context, rec Record) (string, llm.Usage, error) {
		return "", llm.Usage{}, errors.New("boom")
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusFailed || rec.Err != "boom" {
		t.Fatalf("%+v", rec)
	}
}
