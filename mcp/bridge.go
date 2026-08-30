package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/asaqelee/agent_go/tool"
)

// ServerConfig describes one MCP server to spawn.
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	// Allow is the tool allowlist (unprefixed or prefixed names). Empty denies all. ["*"] allows all.
	Allow []string `json:"allow,omitempty"`
}

// FileConfig is the JSON file loaded by AGENT_MCP_CONFIG.
type FileConfig struct {
	Servers []ServerConfig `json:"servers"`
}

// Bridge owns MCP clients and exposes them as tool.Tool values.
type Bridge struct {
	clients []*Client
	tools   []tool.Tool
}

// Connect dials every server and registers allowlisted tools as `{name}__{tool}`.
func Connect(ctx context.Context, servers []ServerConfig) (*Bridge, error) {
	b := &Bridge{}
	for _, cfg := range servers {
		if strings.TrimSpace(cfg.Name) == "" || strings.TrimSpace(cfg.Command) == "" {
			continue
		}
		c, err := Dial(ctx, cfg)
		if err != nil {
			b.Close()
			return nil, fmt.Errorf("mcp %s: %w", cfg.Name, err)
		}
		listed, err := c.ListTools(ctx)
		if err != nil {
			_ = c.Close()
			b.Close()
			return nil, fmt.Errorf("mcp %s tools/list: %w", cfg.Name, err)
		}
		b.clients = append(b.clients, c)
		for _, sch := range listed {
			if !allowed(cfg, sch.Name) {
				continue
			}
			b.tools = append(b.tools, mcpTool{
				client: c,
				prefix: cfg.Name,
				schema: sch,
			})
		}
	}
	return b, nil
}

// Tools returns the bridged tools (may be empty).
func (b *Bridge) Tools() []tool.Tool {
	if b == nil {
		return nil
	}
	return b.tools
}

// Close kills all server processes.
func (b *Bridge) Close() {
	if b == nil {
		return
	}
	for _, c := range b.clients {
		_ = c.Close()
	}
	b.clients = nil
}

func allowed(cfg ServerConfig, toolName string) bool {
	if len(cfg.Allow) == 0 {
		return false
	}
	prefixed := cfg.Name + "__" + toolName
	for _, a := range cfg.Allow {
		a = strings.TrimSpace(a)
		if a == "*" || a == toolName || a == prefixed {
			return true
		}
	}
	return false
}

type mcpTool struct {
	client *Client
	prefix string
	schema toolSchema
}

func (t mcpTool) Name() string { return t.prefix + "__" + t.schema.Name }
func (t mcpTool) Description() string {
	d := t.schema.Description
	if d == "" {
		d = "MCP tool " + t.schema.Name
	}
	return "[mcp:" + t.prefix + "] " + d
}
func (t mcpTool) Parameters() map[string]any {
	if t.schema.InputSchema != nil {
		return t.schema.InputSchema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t mcpTool) Annotations() tool.Annotations {
	ann := tool.Annotations{OpenWorld: true, NeedsApproval: true}
	if t.schema.Annotations != nil {
		ann.Title = t.schema.Annotations.Title
		ann.ReadOnly = t.schema.Annotations.ReadOnlyHint
		ann.Destructive = t.schema.Annotations.DestructiveHint
		ann.OpenWorld = t.schema.Annotations.OpenWorldHint
		if ann.ReadOnly && !ann.Destructive {
			ann.NeedsApproval = false
		}
	}
	return ann
}
func (t mcpTool) Run(ctx context.Context, argsJSON string) (string, error) {
	args := map[string]any{}
	if strings.TrimSpace(argsJSON) != "" && argsJSON != "{}" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments json: %w", err)
		}
	}
	return t.client.CallTool(ctx, t.schema.Name, args)
}
