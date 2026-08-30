package task

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	store := &FileStore{Path: path}

	m1 := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		return "ok:" + goal, nil
	}, Options{Workers: 1, Store: store})
	tk, err := m1.Submit("hello")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done, err := m1.Wait(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusSucceeded {
		t.Fatalf("%+v", done)
	}
	m1.Stop()

	m2 := NewManager(context.Background(), func(ctx context.Context, goal string) (string, error) {
		t.Fatal("should not rerun succeeded task")
		return "", nil
	}, Options{Workers: 1, Store: store})
	defer m2.Stop()
	got, ok := m2.Get(tk.ID)
	if !ok || got.Status != StatusSucceeded || got.Result != "ok:hello" {
		t.Fatalf("%v %+v", ok, got)
	}
}
