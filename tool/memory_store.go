package tool

// MemoryStore is a structured profile sink used by memory-related tools.
// Implemented by agent.Memory (fields: name / likes / notes).
type MemoryStore interface {
	// Remember free-form text (may also update name/likes via heuristics).
	Remember(text string) string
	// SetField sets name|like|note to value.
	SetField(field, value string) (string, error)
}
