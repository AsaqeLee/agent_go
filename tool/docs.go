package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/retrieve"
)

const (
	maxDocReadBytes = 48 * 1024
	maxSearchHits   = 20
	maxSearchFiles  = 200
)

// KnowledgeTools returns list/read/search tools rooted at docsRoot (sandbox).
// docsRoot must be an existing directory; empty root returns nil.
// search_docs uses a substring Grep retriever.
func KnowledgeTools(docsRoot string) []Tool {
	return KnowledgeToolsWith(docsRoot, nil)
}

// KnowledgeToolsWith is KnowledgeTools with an explicit Retriever for search_docs.
// A nil retriever installs retrieve.Grep over the same sandbox root.
func KnowledgeToolsWith(docsRoot string, r retrieve.Retriever) []Tool {
	root, err := sanitizeRoot(docsRoot)
	if err != nil {
		return nil
	}
	kb := &docRoot{root: root}
	if r == nil {
		r = retrieve.NewGrep(root, retrieve.GrepLimits{
			MaxHits:  maxSearchHits,
			MaxFiles: maxSearchFiles,
			MaxBytes: maxDocReadBytes,
		})
	}
	return []Tool{
		ListDocs{kb: kb},
		ReadDoc{kb: kb},
		SearchDocs{kb: kb, retriever: r},
	}
}

type docRoot struct {
	root string // absolute
}

func sanitizeRoot(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("empty docs root")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("not a directory: %s", abs)
	}
	return abs, nil
}

// resolve ensures rel is under root; returns absolute path.
func (d *docRoot) resolve(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return d.root, nil
	}
	// Disallow absolute and parent escapes in the raw string early.
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths not allowed")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes docs root")
	}
	full := filepath.Join(d.root, clean)
	full = filepath.Clean(full)
	rootWithSep := d.root + string(filepath.Separator)
	if full != d.root && !strings.HasPrefix(full, rootWithSep) {
		return "", fmt.Errorf("path escapes docs root")
	}
	return full, nil
}

func isTextDoc(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".md", ".txt", ".markdown", ".rst", ".csv", ".json", ".yaml", ".yml", ".toml", ".go", ".env.example":
		return true
	case "":
		// allow extensionless small files by name
		return true
	default:
		return false
	}
}

// ListDocs lists files under the knowledge root (optional relative subdir).
type ListDocs struct{ kb *docRoot }

func (ListDocs) Name() string { return "list_docs" }
func (ListDocs) Description() string {
	return "List documents in the local knowledge base (sandboxed directory). " +
		"Use before read_doc/search_docs to see available files. Optional path is a relative subdirectory."
}
func (ListDocs) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative subdirectory under the docs root (default \".\")",
			},
		},
	}
}

type listDocsArgs struct {
	Path string `json:"path"`
}

func (t ListDocs) Run(ctx context.Context, argsJSON string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.kb == nil {
		return "", fmt.Errorf("docs root not configured")
	}
	args, err := ParseArgs[listDocsArgs](argsJSON)
	if err != nil {
		return "", err
	}
	dir, err := t.kb.resolve(args.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	relBase, _ := filepath.Rel(t.kb.root, dir)
	if relBase == "." {
		relBase = ""
	}
	fmt.Fprintf(&b, "docs_root=%s\n", t.kb.root)
	n := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rel := name
		if relBase != "" {
			rel = filepath.Join(relBase, name)
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "dir  %s/\n", filepath.ToSlash(rel))
			n++
			continue
		}
		if !isTextDoc(name) {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		fmt.Fprintf(&b, "file %s (%d bytes)\n", filepath.ToSlash(rel), size)
		n++
	}
	if n == 0 {
		b.WriteString("(no listable text files)\n")
	}
	return strings.TrimSpace(b.String()), nil
}

// ReadDoc reads one text file under the knowledge root.
type ReadDoc struct{ kb *docRoot }

func (ReadDoc) Name() string { return "read_doc" }
func (ReadDoc) Description() string {
	return "Read a text/markdown file from the local knowledge base by relative path. " +
		"Use after list_docs or search_docs when you need full content. Paths cannot escape the docs root."
}
func (ReadDoc) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative file path under docs root, e.g. faq.md or hr/leave.md",
			},
		},
		"required": []string{"path"},
	}
}

type readDocArgs struct {
	Path string `json:"path"`
}

func (t ReadDoc) Run(ctx context.Context, argsJSON string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.kb == nil {
		return "", fmt.Errorf("docs root not configured")
	}
	args, err := ParseArgs[readDocArgs](argsJSON)
	if err != nil {
		return "", err
	}
	full, err := t.kb.resolve(args.Path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("path is a directory; use list_docs")
	}
	if !isTextDoc(full) {
		return "", fmt.Errorf("unsupported file type")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	truncated := false
	if len(data) > maxDocReadBytes {
		data = data[:maxDocReadBytes]
		truncated = true
	}
	// Validate mostly-text
	if !utf8.Valid(data) {
		return "", fmt.Errorf("file is not valid UTF-8 text")
	}
	rel, _ := filepath.Rel(t.kb.root, full)
	var b strings.Builder
	fmt.Fprintf(&b, "path: %s\n", filepath.ToSlash(rel))
	fmt.Fprintf(&b, "bytes: %d\n", st.Size())
	if truncated {
		fmt.Fprintf(&b, "note: truncated to %d bytes\n", maxDocReadBytes)
	}
	b.WriteString("---\n")
	b.Write(data)
	return b.String(), nil
}

// SearchDocs searches the knowledge base via a Retriever (grep by default).
type SearchDocs struct {
	kb        *docRoot
	retriever retrieve.Retriever
}

func (SearchDocs) Name() string { return "search_docs" }
func (SearchDocs) Description() string {
	return "Search the local knowledge base. Returns matching file paths and snippets. " +
		"Use to locate answers before read_doc."
}
func (SearchDocs) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Substring to search for",
			},
		},
		"required": []string{"query"},
	}
}

type searchDocsArgs struct {
	Query string `json:"query"`
}

func (t SearchDocs) Run(ctx context.Context, argsJSON string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.kb == nil {
		return "", fmt.Errorf("docs root not configured")
	}
	args, err := ParseArgs[searchDocsArgs](argsJSON)
	if err != nil {
		return "", err
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return "", fmt.Errorf("query is empty")
	}
	if t.retriever == nil {
		return "", fmt.Errorf("no retriever configured")
	}

	hits, err := t.retriever.Retrieve(ctx, retrieve.Query{Text: q, K: maxSearchHits})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "query: %s\n", q)
	if len(hits) == 0 {
		b.WriteString("(no hits)\n")
		return strings.TrimSpace(b.String()), nil
	}
	for _, c := range hits {
		path := c.Path
		if path == "" {
			path = c.ID
		}
		if line := c.Meta["line"]; line != "" {
			fmt.Fprintf(&b, "%s:%s: %s\n", path, line, c.Text)
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", path, c.Text)
	}
	fmt.Fprintf(&b, "hits: %d (capped at %d)\n", len(hits), maxSearchHits)
	return strings.TrimSpace(b.String()), nil
}
