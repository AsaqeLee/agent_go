package tool

import (
	"strings"
	"testing"
)

type fakeStore struct {
	remembered []string
	fields     map[string]string
}

func (f *fakeStore) Remember(text string) string {
	f.remembered = append(f.remembered, text)
	return "ok:" + text
}

func (f *fakeStore) SetField(field, value string) (string, error) {
	if f.fields == nil {
		f.fields = map[string]string{}
	}
	f.fields[field] = value
	return "set:" + field + "=" + value, nil
}

func TestEchoNoteUsesStore(t *testing.T) {
	s := &fakeStore{}
	out, err := EchoNote{Store: s}.Run(`{"text":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok:hello" || len(s.remembered) != 1 {
		t.Fatalf("out=%q remembered=%v", out, s.remembered)
	}
}

func TestMemorySetUsesStore(t *testing.T) {
	s := &fakeStore{}
	out, err := MemorySet{Store: s}.Run(`{"field":"name","value":"小明"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "name") || s.fields["name"] != "小明" {
		t.Fatalf("out=%q fields=%v", out, s.fields)
	}
}

func TestDefaultToolsIncludesMemorySet(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range DefaultTools(nil) {
		names[tl.Name()] = true
	}
	for _, want := range []string{"echo_note", "memory_set", "word_count"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}
