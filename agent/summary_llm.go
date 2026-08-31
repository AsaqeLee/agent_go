package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/llm"
)

// DefaultSummaryMinDraftRunes: only call the LLM when extractive draft is at least this large.
const DefaultSummaryMinDraftRunes = 180

// compressSummaryLLM asks the model to shrink an extractive draft into fewer bullets.
// Does not pass tools. On any failure the caller should keep the draft.
func (a *Agent) compressSummaryLLM(ctx context.Context, draft string) (string, error) {
	if a == nil || a.Provider == nil {
		return "", fmt.Errorf("no provider")
	}
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return "", fmt.Errorf("empty draft")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	sys := strings.TrimSpace(`
You compress agent MEMORY drafts. Output ONLY short bullet facts (max 8 lines).
Rules:
- Keep identity, preferences, decisions, tool-recorded notes, numbers.
- Drop greetings, process chatter, and redundancy.
- Do NOT invent facts. If unsure, omit.
- Prefer compact form like "- name: 小明" / "- like: 梨".
- Match the language of the draft (Chinese if draft is Chinese).
- Total length under 400 characters.
`)
	user := "Compress this memory draft:\n\n" + draft

	resp, err := a.Provider.Chat(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: sys},
			{Role: llm.RoleUser, Content: user},
		},
		Purpose: llm.PurposeSummary,
		// No Tools — pure compression.
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(resp.Message.Content)
	if out == "" {
		return "", fmt.Errorf("empty llm summary")
	}
	// Reject obvious refusals / essays that grew.
	if utf8.RuneCountInString(out) > DefaultMaxSummaryRunes*2 {
		return "", fmt.Errorf("llm summary too long")
	}
	// Normalize to our header + bullets when model returns bare lines.
	if !strings.Contains(out, "Lossy memory") && !strings.HasPrefix(out, "-") {
		// keep as-is but wrap
		out = "Lossy memory of trimmed turns (LLM-compressed):\n" + out
	} else if strings.HasPrefix(out, "-") {
		out = "Lossy memory of trimmed turns (LLM-compressed):\n" + out
	}
	capped, _ := truncateRunes(out, DefaultMaxSummaryRunes)
	return capped, nil
}

func (a *Agent) shouldLLMSummary(draft string) bool {
	if a == nil || a.Provider == nil {
		return false
	}
	// Allow tests / callers to disable via negative Min or explicit flag.
	if a.DisableLLMSummary {
		return false
	}
	min := a.SummaryMinDraftRunes
	if min == 0 {
		min = DefaultSummaryMinDraftRunes
	}
	if min < 0 {
		return false
	}
	return utf8.RuneCountInString(draft) >= min
}

// finalizeSummary runs extractive summary then optional LLM compression.
func (a *Agent) finalizeSummary(ctx context.Context, prevBody string, dropped []llm.Message) string {
	draft := buildConversationSummary(prevBody, dropped)
	if !a.shouldLLMSummary(draft) {
		return draft
	}
	out, err := a.compressSummaryLLM(ctx, draft)
	if err != nil {
		a.log("history trim: llm-summary failed (%v), using extractive draft", err)
		return draft
	}
	a.log("history trim: llm-summary ok (%d→%d runes)",
		utf8.RuneCountInString(draft), utf8.RuneCountInString(out))
	return out
}
