// Package retrieve is the knowledge-lookup seam used by search_docs (and RAG).
//
// The agent loop never sees this package: tools call Retriever, adapters vary.
package retrieve

import "context"

// Query is one retrieval request.
type Query struct {
	// Text is the user / model lookup string.
	Text string
	// K is the maximum number of chunks to return. 0 means the adapter default.
	K int
}

// Chunk is one retrieved snippet. Path is a relative document path when known.
type Chunk struct {
	ID    string
	Path  string
	Text  string
	Score float64
	Meta  map[string]string
}

// Retriever ranks chunks for a query. Grep and vector indexes are adapters.
type Retriever interface {
	Retrieve(ctx context.Context, q Query) ([]Chunk, error)
}
