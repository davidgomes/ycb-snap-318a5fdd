package vm

import (
	"testing"

	"github.com/mattn/anko/env"
	"github.com/mattn/anko/parser"
)

func TestDefaultArguments(t *testing.T) {
	e := env.NewEnv()
	got, err := Execute(e, nil, `func f(a = 1, b = a + 1) { return b }; f()`)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(2) {
		t.Fatalf("got %v, want 2", got)
	}

	got, err = Execute(e, nil, `x = 3; func f(a = x) { return a }; x = 4; f()`)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(4) {
		t.Fatalf("got %v, want 4", got)
	}
}

func TestInvalidDefaultArguments(t *testing.T) {
	for _, script := range []string{
		`func f(a = 1, b) {}`,
		`func f(a = 1, b... = 2) {}`,
	} {
		_, err := parser.ParseSrc(script)
		if err == nil || err.Error() != "invalid default argument declaration" {
			t.Fatalf("script %q: got %v, want invalid default argument declaration", script, err)
		}
	}
}
