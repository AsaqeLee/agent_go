package retrieve

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// GrepLimits caps a substring walk so a huge docs root cannot blow the context.
type GrepLimits struct {
	MaxHits  int // default 20
	MaxFiles int // default 200
	MaxBytes int // bytes read per file; default 48KiB
}

func (l GrepLimits) withDefaults() GrepLimits {
	if l.MaxHits <= 0 {
		l.MaxHits = 20
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = 200
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = 48 * 1024
	}
	return l
}

// Grep is a case-insensitive substring retriever over a sandboxed directory.
// It is the zero-dependency adapter and the eval baseline for vector search.
type Grep struct {
	root   string
	limits GrepLimits
}

// NewGrep walks root (must already be an absolute, existing directory).
func NewGrep(root string, limits GrepLimits) *Grep {
	return &Grep{root: root, limits: limits.withDefaults()}
}

// Retrieve implements Retriever.
func (g *Grep) Retrieve(ctx context.Context, q Query) ([]Chunk, error) {
	if g == nil || g.root == "" {
		return nil, fmt.Errorf("grep: empty root")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(q.Text)
	if query == "" {
		return nil, fmt.Errorf("grep: empty query")
	}
	limits := g.limits
	k := q.K
	if k <= 0 {
		k = limits.MaxHits
	}

	needles := grepNeedles(query)
	var (
		out      []Chunk
		fileScan int
	)
	err := filepath.WalkDir(g.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != g.root {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !isTextDoc(d.Name()) || skipDocName(d.Name()) {
			return nil
		}
		fileScan++
		if fileScan > limits.MaxFiles {
			return fs.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(data) {
			return nil
		}
		if len(data) > limits.MaxBytes {
			data = data[:limits.MaxBytes]
		}
		rel, _ := filepath.Rel(g.root, path)
		rel = filepath.ToSlash(rel)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			score, ok := lineScore(line, needles)
			if !ok {
				continue
			}
			snippet := strings.TrimSpace(line)
			if utf8.RuneCountInString(snippet) > 160 {
				snippet = string([]rune(snippet)[:160]) + "…"
			}
			out = append(out, Chunk{
				ID:    fmt.Sprintf("%s:%d", rel, i+1),
				Path:  rel,
				Text:  snippet,
				Score: score,
				Meta:  map[string]string{"line": fmt.Sprintf("%d", i+1)},
			})
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

type needle struct {
	text  string
	score float64
}

func grepNeedles(q string) []needle {
	q = strings.TrimSpace(q)
	out := []needle{{text: strings.ToLower(q), score: 1}}
	seen := map[string]bool{strings.ToLower(q): true}
	add := func(s string, score float64) {
		s = strings.ToLower(strings.TrimSpace(s))
		if utf8.RuneCountInString(s) < 2 || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, needle{text: s, score: score})
	}
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	for _, f := range fields {
		add(f, 0.7)
	}
	rs := []rune(q)
	for i := 0; i+1 < len(rs); i++ {
		if unicode.Is(unicode.Han, rs[i]) && unicode.Is(unicode.Han, rs[i+1]) {
			add(string(rs[i:i+2]), 0.55)
		}
	}
	return out
}

func lineScore(line string, needles []needle) (float64, bool) {
	lower := strings.ToLower(line)
	best := 0.0
	ok := false
	for _, n := range needles {
		if strings.Contains(lower, n.text) && n.score > best {
			best = n.score
			ok = true
		}
	}
	return best, ok
}

// IsTextDoc reports whether name looks like a searchable text file.
func IsTextDoc(name string) bool {
	return isTextDoc(name)
}

// SkipDocName reports eval/index sidecars that must not be retrieved as corpus.
func SkipDocName(name string) bool {
	return skipDocName(name)
}

func skipDocName(name string) bool {
	switch strings.ToLower(name) {
	case "eval.json", "eval.yaml", "eval.yml", "readme.md", "readme":
		return true
	default:
		return false
	}
}

func isTextDoc(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".md", ".txt", ".markdown", ".rst", ".csv", ".json", ".yaml", ".yml", ".toml", ".go", ".env.example":
		return true
	case "":
		return true
	default:
		return false
	}
}
