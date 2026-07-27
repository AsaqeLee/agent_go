package agent

import (
	"fmt"
	"strings"

	"github.com/asaqelee/agent_go/llm"
)

// DefaultKeepRecentFullTurns: how many newest user-turns keep full tool trajectories.
// Older turns are folded to user + final assistant (tool chain removed).
const DefaultKeepRecentFullTurns = 1

// foldOldTurns collapses tool trajectories in all but the keepRecent newest user-turns.
// keepRecent <= 0 uses DefaultKeepRecentFullTurns; keepRecent < 0 disables folding.
// System/summary/profile prefix is unchanged. Protocol-safe: no orphan tool_calls.
func foldOldTurns(msgs []llm.Message, keepRecent int) (out []llm.Message, folded int) {
	if keepRecent < 0 {
		return msgs, 0
	}
	if keepRecent == 0 {
		keepRecent = DefaultKeepRecentFullTurns
	}
	prefix, turns := splitUserTurns(msgs)
	if len(turns) <= keepRecent {
		return msgs, 0
	}
	cut := len(turns) - keepRecent
	for i := 0; i < cut; i++ {
		before := len(turns[i])
		turns[i] = foldOneUserTurn(turns[i])
		if len(turns[i]) < before {
			folded++
		}
	}
	return joinTurns(prefix, turns), folded
}

// foldOneUserTurn keeps the user message and the last plain assistant reply.
// Intermediate assistant(tool_calls)+tool messages are dropped; tool outcomes may be
// briefly noted in the final assistant content if empty.
func foldOneUserTurn(turn []llm.Message) []llm.Message {
	if len(turn) == 0 {
		return turn
	}
	user := turn[0]
	if user.Role != llm.RoleUser {
		return turn
	}
	var (
		finalAsst *llm.Message
		toolBits  []string
	)
	for i := 1; i < len(turn); i++ {
		m := turn[i]
		switch m.Role {
		case llm.RoleTool:
			name := m.Name
			if name == "" {
				name = "tool"
			}
			toolBits = append(toolBits, fmt.Sprintf("%s→%s", name, clipOneLine(m.Content, 40)))
		case llm.RoleAssistant:
			if len(m.ToolCalls) == 0 {
				cp := m
				finalAsst = &cp
			}
		}
	}
	out := []llm.Message{user}
	if finalAsst != nil {
		if strings.TrimSpace(finalAsst.Content) == "" && len(toolBits) > 0 {
			finalAsst.Content = "[folded tools: " + strings.Join(toolBits, "; ") + "]"
		}
		out = append(out, *finalAsst)
		return out
	}
	// No plain final assistant (shouldn't happen on successful runs); keep a digest.
	if len(toolBits) > 0 {
		out = append(out, llm.Message{
			Role:    llm.RoleAssistant,
			Content: "[folded tools: " + strings.Join(toolBits, "; ") + "]",
		})
	}
	return out
}
