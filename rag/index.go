package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/retrieve"
)

// Record is one embedded chunk.
type Record struct {
	Chunk  retrieve.Chunk `json:"chunk"`
	Vector []float32      `json:"vector"`
}

// Index is a JSON-serializable vector store.
type Index struct {
	Model string   `json:"model,omitempty"`
	Dim   int      `json:"dim"`
	Items []Record `json:"items"`
}

// Build walks a docs root, chunks text files, and embeds them.
func Build(ctx context.Context, root string, embed llm.Embedder, opt ChunkOpts) (*Index, error) {
	if embed == nil {
		return nil, fmt.Errorf("rag: embedder is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var chunks []retrieve.Chunk
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !retrieve.IsTextDoc(d.Name()) || retrieve.SkipDocName(d.Name()) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(data) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		chunks = append(chunks, ChunkDocument(rel, string(data), opt)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("rag: no text chunks under %s", root)
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	vecs, err := embed.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(chunks) {
		return nil, fmt.Errorf("rag: embed count %d != chunks %d", len(vecs), len(chunks))
	}
	idx := &Index{Items: make([]Record, len(chunks))}
	for i := range chunks {
		if i == 0 {
			idx.Dim = len(vecs[i])
		}
		if len(vecs[i]) != idx.Dim {
			return nil, fmt.Errorf("rag: mixed embedding dimensions")
		}
		idx.Items[i] = Record{Chunk: chunks[i], Vector: vecs[i]}
	}
	return idx, nil
}

// Save writes idx atomically as JSON.
func (idx *Index) Save(path string) error {
	if idx == nil {
		return fmt.Errorf("rag: nil index")
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".index-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Load reads a JSON index from path.
func Load(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("rag load: %w", err)
	}
	if len(idx.Items) == 0 {
		return nil, fmt.Errorf("rag: empty index")
	}
	return &idx, nil
}
