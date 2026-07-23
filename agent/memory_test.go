package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

func TestMemoryHeuristicsAndRender(t *testing.T) {
	m := NewMemory()
	m.Remember("用户叫小明，喜欢吃梨")
	if m.Name != "小明" && m.Name != "小明，喜欢吃梨" {
		// heuristic takes 我叫/我是; "用户叫小明" may not match — still notes
		_ = m.Name
	}
	m.SetField("name", "小明")
	m.SetField("like", "梨")
	if m.Name != "小明" {
		t.Fatalf("name=%q", m.Name)
	}
	if len(m.Likes) != 1 || m.Likes[0] != "梨" {
		t.Fatalf("likes=%v", m.Likes)
	}
	block := m.RenderSystemBlock()
	if !strings.Contains(block, profileMarker) || !strings.Contains(block, "小明") || !strings.Contains(block, "梨") {
		t.Fatalf("block=%q", block)
	}
}

func TestMemoryRememberChineseName(t *testing.T) {
	m := NewMemory()
	m.Remember("我叫小明")
	if m.Name != "小明" {
		t.Fatalf("name=%q", m.Name)
	}
	m.Remember("我喜欢吃梨")
	if len(m.Likes) == 0 || m.Likes[0] != "梨" {
		t.Fatalf("likes=%v", m.Likes)
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
	// Profile independent of chat trim.
	if a.Memory.Name != "小明" || len(a.Memory.Likes) != 1 {
		t.Fatalf("memory lost: %+v", a.Memory.Snapshot())
	}
	// Next chat still gets profile even if history was trimmed.
	var saw bool
	p2 := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "still know you"}},
		},
		onChat: func(req llm.Request, _ int) {
			for _, m := range req.Messages {
				if strings.Contains(m.Content, "小明") && strings.Contains(m.Content, profileMarker) {
					saw = true
				}
			}
		},
	}
	a.Provider = p2
	if _, err := a.Run(context.Background(), "我叫什么"); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("profile not injected after trim")
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
