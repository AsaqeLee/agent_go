// Package plugin loads the local catalog (agent.json): retriever, MCP, specialists.
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/mcp"
)

// Config is the on-disk plugin catalog.
type Config struct {
	Retriever   string             `json:"retriever,omitempty"`
	IndexPath   string             `json:"index_path,omitempty"`
	MCP         mcp.FileConfig     `json:"mcp"`
	Specialists []agent.Specialist `json:"specialists,omitempty"`
	HTTP        HTTP               `json:"http"`
}

// HTTP is the optional listen config.
type HTTP struct {
	Addr string `json:"addr,omitempty"`
}

// Load reads JSON from path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("plugin config: %w", err)
	}
	cfg.Retriever = strings.TrimSpace(cfg.Retriever)
	cfg.IndexPath = strings.TrimSpace(cfg.IndexPath)
	cfg.HTTP.Addr = strings.TrimSpace(cfg.HTTP.Addr)
	return cfg, nil
}

// LookupDefault returns AGENT_CONFIG, else agent.json if present, else "".
func LookupDefault() string {
	if v := strings.TrimSpace(os.Getenv("AGENT_CONFIG")); v != "" {
		if v == "off" || v == "-" {
			return ""
		}
		return v
	}
	if st, err := os.Stat("agent.json"); err == nil && !st.IsDir() {
		return "agent.json"
	}
	return ""
}
