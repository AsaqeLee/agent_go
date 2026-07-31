package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeToolsSandboxAndSearch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# Hello\nannual leave is 10 days\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("wifi password is secret-demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// outside file should not be readable via ..
	outside := filepath.Join(filepath.Dir(dir), "outside.md")
	_ = os.WriteFile(outside, []byte("leak"), 0o644)

	tools := KnowledgeTools(dir)
	if len(tools) != 3 {
		t.Fatalf("tools=%d", len(tools))
	}
	reg := NewRegistry(tools)

	list := reg.Execute(context.Background(), "list_docs", `{}`)
	if !strings.Contains(list, "a.md") || !strings.Contains(list, "b.txt") {
		t.Fatalf("list=%s", list)
	}

	read := reg.Execute(context.Background(), "read_doc", `{"path":"a.md"}`)
	if !strings.Contains(read, "annual leave") {
		t.Fatalf("read=%s", read)
	}

	// path escape
	bad := reg.Execute(context.Background(), "read_doc", `{"path":"../outside.md"}`)
	if !strings.HasPrefix(bad, "error:") {
		t.Fatalf("expected sandbox error, got %s", bad)
	}

	search := reg.Execute(context.Background(), "search_docs", `{"query":"wifi"}`)
	if !strings.Contains(search, "b.txt") || !strings.Contains(strings.ToLower(search), "wifi") {
		t.Fatalf("search=%s", search)
	}
}

func TestKnowledgeToolsEmptyRoot(t *testing.T) {
	if KnowledgeTools("") != nil {
		t.Fatal("expected nil")
	}
	if KnowledgeTools("/no/such/path/hopefully") != nil {
		t.Fatal("expected nil for missing path")
	}
}

func TestWordCount(t *testing.T) {
	w := WordCount{}
	cases := []struct {
		args string
		want string
	}{
		{`{"text":"hello world"}`, "2"},
		{`{"text":"  a   b  c "}`, "3"},
		{`{"text":""}`, "0"},
		{`{"text":"我叫小明"}`, "1"},
	}
	for _, tc := range cases {
		got, err := w.Run(context.Background(), tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.args, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.args, got, tc.want)
		}
	}
}
