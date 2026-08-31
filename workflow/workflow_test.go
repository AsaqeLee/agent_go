package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/channel"
	"github.com/asaqelee/agent_go/retrieve"
)

type fakeRet struct {
	hits []retrieve.Chunk
	err  error
	n    int
}

func (f *fakeRet) Retrieve(context.Context, retrieve.Query) ([]retrieve.Chunk, error) {
	f.n++
	return f.hits, f.err
}

func TestRunHappyPath(t *testing.T) {
	ch := &channel.Memory{}
	retr := &fakeRet{hits: []retrieve.Chunk{{Path: "leave-policy.md", Text: "年假 10 天"}}}
	answered := false
	res, err := Run(context.Background(), "年假几天", retr, func(_ context.Context, grounded string) (string, error) {
		answered = true
		if !strings.Contains(grounded, "leave-policy.md") {
			t.Fatalf("grounded=%s", grounded)
		}
		return "10 天（leave-policy.md）", nil
	}, ch)
	if err != nil {
		t.Fatal(err)
	}
	if !answered || res.Step != "emit" || res.Answer == "" {
		t.Fatalf("%+v", res)
	}
	if len(ch.Transcript()) != 1 {
		t.Fatalf("%+v", ch.Transcript())
	}
}

func TestRunRetrieveFailureSkipsAnswer(t *testing.T) {
	retr := &fakeRet{err: errors.New("boom")}
	called := false
	_, err := Run(context.Background(), "q", retr, func(context.Context, string) (string, error) {
		called = true
		return "", nil
	}, nil)
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestRunNoHitsSkipsAnswer(t *testing.T) {
	retr := &fakeRet{}
	called := false
	_, err := Run(context.Background(), "q", retr, func(context.Context, string) (string, error) {
		called = true
		return "", nil
	}, nil)
	if err == nil || called || !strings.Contains(err.Error(), "no hits") {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
