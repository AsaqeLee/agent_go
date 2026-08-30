package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LoadFile reads a FileConfig JSON document.
func LoadFile(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("mcp config: %w", err)
	}
	for i := range cfg.Servers {
		cfg.Servers[i].Name = strings.TrimSpace(cfg.Servers[i].Name)
		cfg.Servers[i].Command = strings.TrimSpace(cfg.Servers[i].Command)
	}
	return cfg, nil
}
