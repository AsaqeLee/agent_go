package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/asaqelee/agent_go/tool"
)

// Specialist is a named inner agent with its own prompt and tool allowlist.
type Specialist struct {
	Name        string
	Description string
	Prompt      string
	Tools       []string // empty = all parent tools except handoff
}

// Roster routes handoff calls to specialists cloned from a parent Agent.
type Roster struct {
	parent *Agent
	byName map[string]Specialist
}

// NewRoster attaches specialists to parent. parent must outlive the roster.
func NewRoster(parent *Agent, specs []Specialist) *Roster {
	r := &Roster{parent: parent, byName: map[string]Specialist{}}
	for _, s := range specs {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		r.byName[name] = s
	}
	return r
}

// Tool returns the handoff tool. Safe to append to parent.Tools.
func (r *Roster) Tool() tool.Tool {
	return rosterTool{roster: r}
}

func (r *Roster) names() []string {
	out := make([]string, 0, len(r.byName))
	for k := range r.byName {
		out = append(out, k)
	}
	return out
}

func (r *Roster) run(ctx context.Context, name, goal string) (string, error) {
	if r == nil || r.parent == nil {
		return "", fmt.Errorf("handoff: no roster")
	}
	spec, ok := r.byName[name]
	if !ok {
		return "", fmt.Errorf("handoff: unknown agent %q (have %s)", name, strings.Join(r.names(), ", "))
	}
	p := r.parent
	child := Agent{
		Provider:             p.Provider,
		Tools:                filterTools(p.Tools, spec.Tools),
		SystemPrompt:         p.SystemPrompt,
		MaxTurns:             p.MaxTurns,
		MaxToolResultChars:   p.MaxToolResultChars,
		MaxHistoryMessages:   p.MaxHistoryMessages,
		KeepRecentFullTurns:  p.KeepRecentFullTurns,
		DisableLLMSummary:    p.DisableLLMSummary,
		SummaryMinDraftRunes: p.SummaryMinDraftRunes,
		Memory:               p.Memory,
		Verbose:              p.Verbose,
		Log:                  p.Log,
		OnEvent:              p.OnEvent,
		Stream:               p.Stream,
		ToolTimeout:          p.ToolTimeout,
		MaxToolConcurrency:   p.MaxToolConcurrency,
		Approver:             p.Approver,
		Redact:               p.Redact,
		Tracer:               p.Tracer,
	}
	if spec.Prompt != "" {
		child.SystemPrompt = spec.Prompt
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return child.Run(ctx, goal)
}

func filterTools(all []tool.Tool, allow []string) []tool.Tool {
	var out []tool.Tool
	allowSet := map[string]bool{}
	for _, n := range allow {
		allowSet[n] = true
	}
	for _, t := range all {
		if t.Name() == "handoff" {
			continue
		}
		if len(allowSet) > 0 && !allowSet[t.Name()] {
			continue
		}
		out = append(out, t)
	}
	return out
}

type rosterTool struct {
	roster *Roster
}

func (rosterTool) Name() string { return "handoff" }
func (t rosterTool) Description() string {
	names := "none"
	if t.roster != nil {
		if n := t.roster.names(); len(n) > 0 {
			names = strings.Join(n, ", ")
		}
	}
	return "Delegate a goal to a specialist agent and return its final answer. " +
		"Available agents: " + names + ". Use when a sub-task matches a specialist."
}
func (rosterTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent": map[string]any{
				"type":        "string",
				"description": "Specialist name",
			},
			"goal": map[string]any{
				"type":        "string",
				"description": "The full task for the specialist",
			},
		},
		"required": []string{"agent", "goal"},
	}
}

type handoffArgs struct {
	Agent string `json:"agent"`
	Goal  string `json:"goal"`
}

func (t rosterTool) Run(ctx context.Context, argsJSON string) (string, error) {
	args, err := tool.ParseArgs[handoffArgs](argsJSON)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(args.Agent)
	goal := strings.TrimSpace(args.Goal)
	if name == "" || goal == "" {
		return "", fmt.Errorf("agent and goal are required")
	}
	return t.roster.run(ctx, name, goal)
}
