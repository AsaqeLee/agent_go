package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/asaqelee/agent_go/retrieve"
)

// Case is one retrieval gold question.
type Case struct {
	ID           string   `json:"id"`
	Question     string   `json:"question"`
	ExpectPath   string   `json:"expect_path"`
	ExpectSubstr []string `json:"expect_substr,omitempty"`
}

// Result is per-case outcome.
type Result struct {
	ID      string  `json:"id"`
	Hit     bool    `json:"hit"`
	Path    string  `json:"path,omitempty"`
	Snippet string  `json:"snippet,omitempty"`
	Score   float64 `json:"score,omitempty"`
	Reason  string  `json:"reason,omitempty"`
}

// Report aggregates Evaluate.
type Report struct {
	Total int      `json:"total"`
	Hits  int      `json:"hits"`
	Items []Result `json:"items"`
}

// LoadCases reads eval JSON (array of Case).
func LoadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []Case
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("rag eval: %w", err)
	}
	return cases, nil
}

// Evaluate runs each case against r. A hit requires the top-k paths to include
// ExpectPath; optional ExpectSubstr must appear in some returned chunk text.
func Evaluate(ctx context.Context, r retrieve.Retriever, cases []Case, k int) Report {
	if k <= 0 {
		k = 5
	}
	rep := Report{Total: len(cases), Items: make([]Result, 0, len(cases))}
	for _, c := range cases {
		item := Result{ID: c.ID}
		hits, err := r.Retrieve(ctx, retrieve.Query{Text: c.Question, K: k})
		if err != nil {
			item.Reason = err.Error()
			rep.Items = append(rep.Items, item)
			continue
		}
		if len(hits) > 0 {
			item.Path = hits[0].Path
			item.Snippet = hits[0].Text
			item.Score = hits[0].Score
		}
		want := strings.TrimSpace(c.ExpectPath)
		pathOK := want == ""
		substrOK := len(c.ExpectSubstr) == 0
		for _, h := range hits {
			if want != "" && (h.Path == want || strings.HasSuffix(h.Path, "/"+want) || strings.Contains(h.Path, want)) {
				pathOK = true
			}
			blob := strings.ToLower(h.Text)
			for _, s := range c.ExpectSubstr {
				if s != "" && strings.Contains(blob, strings.ToLower(s)) {
					substrOK = true
				}
			}
		}
		item.Hit = pathOK && substrOK
		if item.Hit {
			rep.Hits++
		} else if item.Reason == "" {
			item.Reason = "miss"
		}
		rep.Items = append(rep.Items, item)
	}
	return rep
}

// Format is a one-line + miss list summary.
func (r Report) Format() string {
	miss := r.Total - r.Hits
	s := fmt.Sprintf("hits=%d/%d", r.Hits, r.Total)
	if miss == 0 {
		return s
	}
	var ids []string
	for _, it := range r.Items {
		if !it.Hit {
			ids = append(ids, it.ID)
		}
	}
	return s + " miss=" + strings.Join(ids, ",")
}
