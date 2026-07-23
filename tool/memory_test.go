package tool

import (
	"strings"
	"testing"
)

type fakeStore struct {
	remembered []string
	fields     map[string]string
	patches    int
	lastName   string
	lastLikes  []string
	lastNotes  []string
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

func (f *fakeStore) ApplyPatch(name string, likes, notes []string) (string, error) {
	f.patches++
	f.lastName = name
	f.lastLikes = likes
	f.lastNotes = notes
	return "patched", nil
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

func TestProfileUpdateUsesStore(t *testing.T) {
	s := &fakeStore{}
	out, err := ProfileUpdate{Store: s}.Run(`{"name":"小明","likes":["梨"],"notes":["住杭州"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "patched" || s.patches != 1 || s.lastName != "小明" || len(s.lastLikes) != 1 {
		t.Fatalf("out=%q store=%+v", out, s)
	}
}

func TestDefaultToolsIncludesProfileUpdate(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range DefaultTools(nil) {
		names[tl.Name()] = true
	}
	for _, want := range []string{"profile_update", "memory_set", "echo_note", "word_count"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}
