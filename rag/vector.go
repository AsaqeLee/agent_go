package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/retrieve"
)

// Vector retrieves by cosine similarity against a built Index.
type Vector struct {
	Index *Index
	Embed llm.Embedder
}

// Retrieve implements retrieve.Retriever.
func (v *Vector) Retrieve(ctx context.Context, q retrieve.Query) ([]retrieve.Chunk, error) {
	if v == nil || v.Index == nil || v.Embed == nil {
		return nil, fmt.Errorf("rag: vector retriever not configured")
	}
	query := strings.TrimSpace(q.Text)
	if query == "" {
		return nil, fmt.Errorf("rag: empty query")
	}
	k := q.K
	if k <= 0 {
		k = 8
	}
	vecs, err := v.Embed.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("rag: empty query embedding")
	}
	qv := vecs[0]
	type scored struct {
		chunk retrieve.Chunk
		score float64
	}
	var ranked []scored
	for _, rec := range v.Index.Items {
		s := cosine(qv, rec.Vector)
		c := rec.Chunk
		c.Score = s
		ranked = append(ranked, scored{chunk: c, score: s})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if k > len(ranked) {
		k = len(ranked)
	}
	out := make([]retrieve.Chunk, k)
	for i := 0; i < k; i++ {
		out[i] = ranked[i].chunk
	}
	return out, nil
}

func cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot // vectors are L2-normalized at embed time
}
