package llm

import "context"

// Delta is one incremental piece of a streamed completion.
type Delta struct {
	// Content is a text fragment (may be empty when only tool_calls are streaming).
	Content string
}

// Streamer is an optional Provider capability. Agent uses Chat when the
// provider does not implement it, or when streaming is disabled.
type Streamer interface {
	// ChatStream is Chat plus incremental text deltas. The returned Response
	// is the fully assembled assistant message (including tool_calls / usage).
	ChatStream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error)
}
