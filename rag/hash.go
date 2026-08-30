package rag

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/asaqelee/agent_go/llm"
)

// HashEmbedder is a deterministic bag-of-n-grams embedder.
// Useful for tests and offline `agent index --hash` / eval without an API.
type HashEmbedder struct {
	Dim int // default 64
}

var _ llm.Embedder = HashEmbedder{}

// Embed implements llm.Embedder.
func (h HashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	dim := h.Dim
	if dim <= 0 {
		dim = 64
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		v := make([]float32, dim)
		runes := []rune(strings.ToLower(text))
		var prev rune
		for _, r := range runes {
			if unicode.IsSpace(r) {
				prev = 0
				continue
			}
			v[mod(int(r), dim)] += 1
			if prev != 0 {
				v[mod(int(prev)*31+int(r), dim)] += 1.5
			}
			prev = r
		}
		normalize(v)
		out[i] = v
	}
	return out, nil
}

func mod(n, dim int) int {
	n %= dim
	if n < 0 {
		n += dim
	}
	return n
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}
