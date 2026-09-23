package interp_test

import (
	"testing"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func TestEmbed(t *testing.T) {
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	_, err := i.CompilePath("./testdata/embed/main.go")
	if err == nil {
		t.Fatal("expected no-match error")
	}
	i = interp.New(interp.Options{})
	_ = i.Use(stdlib.Symbols)
	_, err = i.EvalPath("./testdata/embed/ok.go")
	if err != nil {
		t.Fatal(err)
	}
}
