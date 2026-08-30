package rag

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/retrieve"
)

func TestChunkSplitsHeadings(t *testing.T) {
	chunks := ChunkDocument("p.md", "# A\n\nhello\n\n## B\n\nworld\n", ChunkOpts{})
	if len(chunks) < 2 {
		t.Fatalf("chunks=%d %+v", len(chunks), chunks)
	}
	var joined strings.Builder
	for _, c := range chunks {
		joined.WriteString(c.Text)
	}
	if !strings.Contains(joined.String(), "hello") || !strings.Contains(joined.String(), "world") {
		t.Fatalf("%+v", chunks)
	}
}

func TestBuildAndRetrieve(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leave.md"), []byte("年假政策：入职满一年年假十个工作日。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wifi.md"), []byte("办公 Wi-Fi 名称 DemoCorp-Office，访客密码前台领取。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Build(context.Background(), dir, HashEmbedder{Dim: 64}, ChunkOpts{})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "index.json")
	if err := idx.Save(out); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	v := &Vector{Index: loaded, Embed: HashEmbedder{Dim: 64}}
	hits, err := v.Retrieve(context.Background(), retrieve.Query{Text: "年假十个工作日", K: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "leave.md" {
		t.Fatalf("hits=%+v", hits)
	}
}

func TestEvalJSONAgainstExampleKB(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "examples", "kb")
	cases, err := LoadCases(filepath.Join(root, "eval.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 15 {
		t.Fatalf("cases=%d", len(cases))
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	g := retrieve.NewGrep(abs, retrieve.GrepLimits{})
	rep := Evaluate(context.Background(), g, cases, 8)
	if rep.Hits != rep.Total {
		t.Fatalf("grep eval %s", rep.Format())
	}
}

func TestEvaluatePathHit(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "leave.md"), []byte("年假 10 天\n"), 0o644)
	g := retrieve.NewGrep(dir, retrieve.GrepLimits{})
	rep := Evaluate(context.Background(), g, []Case{{
		ID: "leave", Question: "年假", ExpectPath: "leave.md", ExpectSubstr: []string{"10"},
	}}, 3)
	if rep.Hits != 1 {
		t.Fatalf("%+v", rep)
	}
}
