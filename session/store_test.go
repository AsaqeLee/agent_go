package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asaqelee/agent_go/llm"
)

func TestMemoryRoundTrip(t *testing.T) {
	st := NewMemory()
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
	}
	if err := st.Save(context.Background(), "s1", msgs); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "hi" {
		t.Fatalf("%+v", got)
	}
	got[0].Content = "mutated"
	again, _ := st.Load(context.Background(), "s1")
	if again[0].Content != "hi" {
		t.Fatal("store should copy on load")
	}
}

func TestFileRoundTripAndMissing(t *testing.T) {
	dir := t.TempDir()
	st := &File{Dir: dir}
	got, err := st.Load(context.Background(), "missing")
	if err != nil || got != nil {
		t.Fatalf("missing: %v %+v", err, got)
	}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "q"}}
	if err := st.Save(context.Background(), "abc", msgs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "abc.json")); err != nil {
		t.Fatal(err)
	}
	got, err = st.Load(context.Background(), "abc")
	if err != nil || len(got) != 1 || got[0].Content != "q" {
		t.Fatalf("%v %+v", err, got)
	}
	if err := st.Delete(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	got, err = st.Load(context.Background(), "abc")
	if err != nil || got != nil {
		t.Fatalf("after delete: %v %+v", err, got)
	}
}

func TestFileConcurrentSaveLoad(t *testing.T) {
	dir := t.TempDir()
	st := &File{Dir: dir}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			_ = st.Save(context.Background(), "s1", []llm.Message{{Role: llm.RoleUser, Content: "x"}})
			_ = n
		}(i)
		go func() {
			defer wg.Done()
			_, _ = st.Load(context.Background(), "s1")
		}()
	}
	wg.Wait()
}

func TestFileRejectsPathID(t *testing.T) {
	st := &File{Dir: t.TempDir()}
	err := st.Save(context.Background(), "../etc", nil)
	if err == nil {
		t.Fatal("expected invalid id")
	}
}
