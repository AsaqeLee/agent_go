package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoToolsSandbox(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := RepoTools(dir)
	if len(tools) != 2 {
		t.Fatalf("tools=%d", len(tools))
	}
	reg := NewRegistry(tools)
	list := reg.Execute(context.Background(), "list_repo", `{}`)
	if !strings.Contains(list, "main.go") {
		t.Fatalf("list=%s", list)
	}
	if !strings.HasPrefix(reg.Execute(context.Background(), "list_docs", `{}`), "error:") {
		t.Fatal("list_docs should not be registered")
	}
	bad := reg.Execute(context.Background(), "read_repo", `{"path":"../x.go"}`)
	if !strings.HasPrefix(bad, "error:") {
		t.Fatalf("expected sandbox error, got %s", bad)
	}
}
