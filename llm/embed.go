package llm

import "context"

// Embedder turns texts into dense vectors. Used by RAG, not by the agent loop.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
