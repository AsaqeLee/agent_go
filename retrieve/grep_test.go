package retrieve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGrepFindsSubstringAndRespectsCap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("annual leave is 10 days\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("wifi password is secret-demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := NewGrep(dir, GrepLimits{MaxHits: 1})
	hits, err := g.Retrieve(context.Background(), Query{Text: "wifi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits=%d %+v", len(hits), hits)
	}
	if hits[0].Path != "b.txt" {
		t.Fatalf("path=%q", hits[0].Path)
	}
	if hits[0].Meta["line"] != "1" {
		t.Fatalf("meta=%v", hits[0].Meta)
	}
}

func TestGrepEmptyQuery(t *testing.T) {
	g := NewGrep(t.TempDir(), GrepLimits{})
	_, err := g.Retrieve(context.Background(), Query{Text: "  "})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGrepHonorsContext(t *testing.T) {
	g := NewGrep(t.TempDir(), GrepLimits{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.Retrieve(ctx, Query{Text: "x"})
	if err == nil {
		t.Fatal("expected canceled")
	}
}
