package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

func TestMemoryFieldsNoHeuristics(t *testing.T) {
	m := NewMemory()
	// Remember must NOT regex-parse name/likes.
	m.Remember("我叫小明，喜欢吃梨")
	if m.Name != "" {
		t.Fatalf("Remember should not set name via heuristics, got %q", m.Name)
	}
	if len(m.Likes) != 0 {
		t.Fatalf("Remember should not set likes via heuristics, got %v", m.Likes)
	}
	if len(m.Notes) != 1 {
		t.Fatalf("notes=%v", m.Notes)
	}

	// LLM-style structured writes.
	if _, err := m.ApplyPatch("小明", []string{"梨"}, nil); err != nil {
		t.Fatal(err)
	}
	if m.Name != "小明" || len(m.Likes) != 1 || m.Likes[0] != "梨" {
		t.Fatalf("patch failed: %+v", m.Snapshot())
	}
	block := m.RenderSystemBlock()
	if !strings.Contains(block, profileMarker) || !strings.Contains(block, "小明") {
		t.Fatalf("block=%q", block)
	}
}

func TestApplyPatchPartial(t *testing.T) {
	m := NewMemory()
	if _, err := m.ApplyPatch("小明", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplyPatch("", []string{"梨"}, nil); err != nil {
		t.Fatal(err)
	}
	if m.Name != "小明" || m.Likes[0] != "梨" {
		t.Fatalf("%+v", m.Snapshot())
	}
	if _, err := m.ApplyPatch("", nil, nil); err == nil {
		t.Fatal("empty patch should error")
	}
}

func TestUpsertProfileInjectedBeforeChat(t *testing.T) {
	mem := NewMemory()
	mem.SetField("name", "小明")

	var sawProfile bool
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "你好小明"}},
		},
		onChat: func(req llm.Request, _ int) {
			for _, m := range req.Messages {
				if m.Role == llm.RoleSystem && strings.Contains(m.Content, profileMarker) && strings.Contains(m.Content, "小明") {
					sawProfile = true
				}
			}
		},
	}
	a := &Agent{
		Provider: p,
		Memory:   mem,
		Tools:    tool.DefaultTools(mem),
		MaxTurns: 3,
	}
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if !sawProfile {
		t.Fatal("expected [user_profile] in Chat messages")
	}
}

func TestProfileSurvivesHistoryTrim(t *testing.T) {
	mem := NewMemory()
	mem.SetField("name", "小明")
	mem.SetField("like", "梨")

	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r1"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r2"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r3"}},
		},
	}
	a := &Agent{
		Provider:           p,
		Memory:             mem,
		MaxTurns:           3,
		MaxHistoryMessages: 5,
	}
	for _, q := range []string{"q1", "q2", "q3"} {
		if _, err := a.Run(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if a.Memory.Name != "小明" || len(a.Memory.Likes) != 1 {
		t.Fatalf("memory lost: %+v", a.Memory.Snapshot())
	}
}

func TestResetKeepsMemoryUnlessResetAll(t *testing.T) {
	mem := NewMemory()
	mem.SetField("name", "小明")
	a := &Agent{Memory: mem}
	a.history = []llm.Message{{Role: llm.RoleUser, Content: "x"}}
	a.Reset()
	if len(a.History()) != 0 || a.Memory.Name != "小明" {
		t.Fatal("Reset should clear chat only")
	}
	a.ResetAll()
	if a.Memory.Name != "" {
		t.Fatal("ResetAll should clear memory")
	}
}

func TestProfileUpdateToolRoundTrip(t *testing.T) {
	mem := NewMemory()
	out, err := tool.ProfileUpdate{Store: mem}.Run(`{"name":"小明","likes":["梨","茶"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "name=小明") {
		t.Fatalf("out=%q", out)
	}
	if mem.Name != "小明" || len(mem.Likes) != 2 {
		t.Fatalf("%+v", mem.Snapshot())
	}
}
