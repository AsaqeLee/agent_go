package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadDotEnv reads a simple KEY=VALUE file and sets process env vars.
// Existing non-empty environment variables are never overwritten.
// Missing file is not an error (returns n=0, err=nil).
//
// Supported lines:
//
//	# comment
//	KEY=value
//	KEY="quoted value"
//	KEY='quoted value'
//	export KEY=value
func loadDotEnv(path string) (n int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Allow long values (API keys, etc.)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		// Optional "export " prefix (shell-style).
		if strings.HasPrefix(raw, "export ") {
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "export "))
		}

		key, val, ok := parseDotEnvLine(raw)
		if !ok {
			return n, fmt.Errorf("%s:%d: invalid line %q", path, lineNo, raw)
		}
		// Do not override env already set by the shell / parent process.
		if os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return n, fmt.Errorf("%s:%d: setenv %s: %w", path, lineNo, key, err)
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return n, err
	}
	return n, nil
}

func parseDotEnvLine(line string) (key, value string, ok bool) {
	// Strip inline comment only when not inside quotes: KEY=val # comment
	// Keep it simple: split on first '='.
	i := strings.IndexByte(line, '=')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	value = strings.TrimSpace(line[i+1:])
	value = unquoteDotEnvValue(value)
	return key, value, true
}

func unquoteDotEnvValue(v string) string {
	if len(v) < 2 {
		return v
	}
	if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	// Unquoted: drop trailing inline comment "value # comment"
	if j := strings.Index(v, " #"); j >= 0 {
		return strings.TrimSpace(v[:j])
	}
	return v
}
