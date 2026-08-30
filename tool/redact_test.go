package tool

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	in := "token Bearer abcdefghijklmnop sk-abcdefghijk deadbeefdeadbeefdeadbeefdeadbeef"
	out := RedactSecrets("x", in)
	if strings.Contains(out, "abcdefghijklmnop") || strings.Contains(out, "sk-abcdefghijk") {
		t.Fatalf("%s", out)
	}
	if !strings.Contains(out, "sk-***") {
		t.Fatalf("%s", out)
	}
}
