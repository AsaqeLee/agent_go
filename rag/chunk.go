package rag

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/retrieve"
)

// ChunkOpts controls how a document is split before embedding.
type ChunkOpts struct {
	MaxRunes int // default 480
}

func (o ChunkOpts) withDefaults() ChunkOpts {
	if o.MaxRunes <= 0 {
		o.MaxRunes = 480
	}
	return o
}

// ChunkDocument splits markdown/text into overlapping-free chunks.
// Heading lines start a new chunk; oversized blocks are split on rune budget.
func ChunkDocument(path, text string, opt ChunkOpts) []retrieve.Chunk {
	opt = opt.withDefaults()
	text = strings.ReplaceAll(text, "\r\n", "\n")
	blocks := splitBlocks(text)
	var out []retrieve.Chunk
	n := 0
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		for _, piece := range splitRunes(block, opt.MaxRunes) {
			n++
			out = append(out, retrieve.Chunk{
				ID:   fmt.Sprintf("%s#%d", path, n),
				Path: path,
				Text: piece,
			})
		}
	}
	return out
}

func splitBlocks(text string) []string {
	lines := strings.Split(text, "\n")
	var (
		blocks []string
		cur    strings.Builder
	)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			blocks = append(blocks, s)
		}
		cur.Reset()
	}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			flush()
			cur.WriteString(trim)
			cur.WriteByte('\n')
			continue
		}
		if trim == "" {
			flush()
			continue
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
	}
	flush()
	return blocks
}

func splitRunes(s string, max int) []string {
	if utf8.RuneCountInString(s) <= max {
		return []string{s}
	}
	runes := []rune(s)
	var out []string
	for len(runes) > 0 {
		n := max
		if n > len(runes) {
			n = len(runes)
		}
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return out
}
