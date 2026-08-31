package llm

import "fmt"

// StatusError is an HTTP failure from an OpenAI-compatible endpoint.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string {
	if e == nil {
		return "llm: status error"
	}
	if e.Msg != "" {
		return fmt.Sprintf("llm: status %d: %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("llm: status %d", e.Status)
}

// Retryable reports 429 and 5xx.
func (e *StatusError) Retryable() bool {
	if e == nil {
		return false
	}
	return e.Status == 429 || e.Status >= 500
}
