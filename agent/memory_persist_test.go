package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemorySaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mem.json")
	m := &Memory{Path: path}
	if _, err := m.SetField("name", "小明"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetField("like", "梨"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("expected file", err)
	}

	m2, err := LoadMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Name != "小明" || len(m2.Likes) != 1 || m2.Likes[0] != "梨" {
		t.Fatalf("%+v", m2.Snapshot())
	}
	if m2.Path != path {
		t.Fatalf("path=%q", m2.Path)
	}
}

func TestLoadMemoryMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.json")
	m, err := LoadMemory(path)
	if err != nil || m.Name != "" {
		t.Fatalf("%+v %v", m, err)
	}
}
