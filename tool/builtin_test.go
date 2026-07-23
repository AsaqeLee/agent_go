package tool

import "testing"

func TestCalculator(t *testing.T) {
	c := Calculator{}
	cases := []struct {
		args string
		want string
	}{
		{`{"expression":"2 + 3"}`, "5"},
		{`{"expression":"12.5 * 3"}`, "37.5"},
		{`{"expression":"10 / 4"}`, "2.5"},
		{`{"expression":"-2 + 5"}`, "3"},
	}
	for _, tc := range cases {
		got, err := c.Run(tc.args)
		if err != nil {
			t.Fatalf("Run(%s): %v", tc.args, err)
		}
		if got != tc.want {
			t.Fatalf("Run(%s)=%q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestRegistryUnknownTool(t *testing.T) {
	r := NewRegistry(DefaultTools(nil))
	out := r.Execute("nope", `{}`)
	if out == "" || len(out) < 5 || out[:5] != "error" {
		t.Fatalf("expected error string, got %q", out)
	}
}

func TestEchoNoteEmpty(t *testing.T) {
	_, err := EchoNote{}.Run(`{"text":"  "}`)
	if err == nil {
		t.Fatal("expected error for empty note")
	}
}

func TestDefaultToolsNames(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range DefaultTools(nil) {
		if names[tl.Name()] {
			t.Fatalf("duplicate tool name %q", tl.Name())
		}
		names[tl.Name()] = true
		if tl.Description() == "" {
			t.Fatalf("tool %q missing description", tl.Name())
		}
		_ = Defs([]Tool{tl})
	}
}
