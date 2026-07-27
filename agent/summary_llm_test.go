package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/llm"
)

func TestFinalizeSummaryUsesLLMWhenDraftLong(t *testing.T) {
	// Extractive draft will be long; scripted provider returns compressed bullets.
	longNote := strings.Repeat("重要事实条目内容 ", 30)
	dropped := []llm.Message{
		{Role: llm.RoleUser, Content: longNote},
		{Role: llm.RoleTool, Name: "echo_note", Content: "noted: " + longNote},
	}
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "- name: 小明\n- like: 梨"}},
		},
	}
	a := &Agent{
		Provider:             p,
		SummaryMinDraftRunes: 50, // force LLM path
	}
	out := a.finalizeSummary(context.Background(), "", dropped)
	if !strings.Contains(out, "小明") || !strings.Contains(out, "梨") {
		t.Fatalf("out=%q", out)
	}
	if p.i != 1 {
		t.Fatalf("expected 1 llm call, got %d", p.i)
	}
}

func TestFinalizeSummaryFallbackWhenLLMFails(t *testing.T) {
	dropped := []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("用户说了很多话关于苹果香蕉 ", 20)},
	}
	// No responses → LLM fails → extractive draft
	a := &Agent{
		Provider:             &scriptedProvider{},
		SummaryMinDraftRunes: 10,
	}
	out := a.finalizeSummary(context.Background(), "", dropped)
	if !strings.Contains(out, "user:") && !strings.Contains(out, "Lossy memory") {
		t.Fatalf("expected extractive fallback, got %q", out)
	}
}

func TestShouldLLMSummaryDisabled(t *testing.T) {
	a := &Agent{Provider: &scriptedProvider{}, DisableLLMSummary: true}
	if a.shouldLLMSummary(strings.Repeat("x", 500)) {
		t.Fatal("expected disabled")
	}
}
