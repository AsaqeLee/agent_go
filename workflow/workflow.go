// Package workflow is a fixed 3-step DAG: retrieve → answer → emit.
// It is not a general engine; Agent loop remains the open-ended planner.
package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/asaqelee/agent_go/channel"
	"github.com/asaqelee/agent_go/retrieve"
)

// Result is one DAG execution.
type Result struct {
	Query  string           `json:"query"`
	Hits   []retrieve.Chunk `json:"hits"`
	Answer string           `json:"answer"`
	Step   string           `json:"step"` // last successful step
}

// Answerer turns a grounded prompt into a final reply.
type Answerer func(ctx context.Context, grounded string) (string, error)

// Run executes retrieve → answer → optional Channel.Send.
func Run(ctx context.Context, query string, retr retrieve.Retriever, answer Answerer, ch channel.Channel) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, fmt.Errorf("workflow: empty query")
	}
	if retr == nil {
		return Result{}, fmt.Errorf("workflow: nil retriever")
	}
	if answer == nil {
		return Result{}, fmt.Errorf("workflow: nil answerer")
	}

	hits, err := retr.Retrieve(ctx, retrieve.Query{Text: query, K: 8})
	if err != nil {
		return Result{Query: query, Step: "retrieve"}, fmt.Errorf("workflow retrieve: %w", err)
	}
	if len(hits) == 0 {
		return Result{Query: query, Step: "retrieve"}, fmt.Errorf("workflow retrieve: no hits")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Answer using only these snippets. Cite file paths.\nQuestion: %s\n\n", query)
	for _, h := range hits {
		fmt.Fprintf(&b, "- %s: %s\n", h.Path, h.Text)
	}
	out, err := answer(ctx, b.String())
	if err != nil {
		return Result{Query: query, Hits: hits, Step: "retrieve"}, fmt.Errorf("workflow answer: %w", err)
	}
	res := Result{Query: query, Hits: hits, Answer: strings.TrimSpace(out), Step: "answer"}
	if ch != nil {
		_ = ch.Send(ctx, channel.Message{Text: res.Answer, Kind: "assistant", SessionID: "workflow"})
		res.Step = "emit"
	}
	return res, nil
}
