package tool

// MemoryStore is a structured profile sink used by memory-related tools.
// Implemented by agent.Memory (fields: name / likes / notes).
//
// Field updates are intentional tool calls from the LLM — not regex extraction
// over free text. The model chooses field values via tool arguments (JSON schema).
type MemoryStore interface {
	// Remember appends free-form text to notes only (no name/like parsing).
	Remember(text string) string
	// SetField sets one of name|like|note.
	SetField(field, value string) (string, error)
	// ApplyPatch applies a structured multi-field update from the model.
	// Empty/omitted parts are ignored (partial update).
	ApplyPatch(name string, likes []string, notes []string) (string, error)
}
