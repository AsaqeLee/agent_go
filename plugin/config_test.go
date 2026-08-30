package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	raw := `{
		"retriever": "vector",
		"index_path": ".agent_index.json",
		"mcp": {"servers": [{"name":"echo","command":"go","allow":["*"]}]},
		"specialists": [{"name":"docs","tools":["read_doc"]}],
		"http": {"addr": ":9090"}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retriever != "vector" || cfg.HTTP.Addr != ":9090" {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.MCP.Servers) != 1 || cfg.MCP.Servers[0].Name != "echo" {
		t.Fatalf("%+v", cfg.MCP)
	}
	if len(cfg.Specialists) != 1 || cfg.Specialists[0].Name != "docs" {
		t.Fatalf("%+v", cfg.Specialists)
	}
}
