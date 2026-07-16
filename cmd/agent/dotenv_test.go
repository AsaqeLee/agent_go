package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	valid := `
# comment
OPENAI_API_KEY=sk-from-file
OPENAI_MODEL="gpt-test"
export AGENT_VERBOSE=0
NOTE=hello # trailing comment
`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}

	// Clear keys we care about for the test.
	for _, k := range []string{"OPENAI_API_KEY", "OPENAI_MODEL", "AGENT_VERBOSE", "NOTE", "PRESET"} {
		_ = os.Unsetenv(k)
	}

	n, err := loadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("loaded %d vars, want 4", n)
	}
	if os.Getenv("OPENAI_API_KEY") != "sk-from-file" {
		t.Fatalf("key=%q", os.Getenv("OPENAI_API_KEY"))
	}
	if os.Getenv("OPENAI_MODEL") != "gpt-test" {
		t.Fatalf("model=%q", os.Getenv("OPENAI_MODEL"))
	}
	if os.Getenv("AGENT_VERBOSE") != "0" {
		t.Fatalf("verbose=%q", os.Getenv("AGENT_VERBOSE"))
	}
	if os.Getenv("NOTE") != "hello" {
		t.Fatalf("note=%q", os.Getenv("NOTE"))
	}
}

func TestLoadDotEnvDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("PRESET=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRESET", "from-shell")

	n, err := loadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 sets when overridden, got %d", n)
	}
	if os.Getenv("PRESET") != "from-shell" {
		t.Fatalf("got %q", os.Getenv("PRESET"))
	}
}

func TestLoadDotEnvMissingOK(t *testing.T) {
	n, err := loadDotEnv(filepath.Join(t.TempDir(), "nope.env"))
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestLoadDotEnvInvalidLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(contentInvalid()), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadDotEnv(path)
	if err == nil {
		t.Fatal("expected error for invalid line")
	}
}

func contentInvalid() string {
	return "GOOD=1\nNOT_A_VALID_LINE\n"
}

func TestParseDotEnvLine(t *testing.T) {
	k, v, ok := parseDotEnvLine(`FOO=bar`)
	if !ok || k != "FOO" || v != "bar" {
		t.Fatalf("%q %q %v", k, v, ok)
	}
	k, v, ok = parseDotEnvLine(`FOO="a b"`)
	if !ok || v != "a b" {
		t.Fatalf("%q %q %v", k, v, ok)
	}
	_, _, ok = parseDotEnvLine(`=nope`)
	if ok {
		t.Fatal("expected fail")
	}
}
