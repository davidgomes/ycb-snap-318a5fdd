package termenv

import (
	"bytes"
	"io"
	"testing"
	"text/template"
)

func TestStylePreserveResets(t *testing.T) {
	inner := ANSI.String("inner").Bold().String()
	s := ANSI.String("a " + inner + " b").Foreground(ANSIColor(1))

	if got, want := s.String(), "\x1b[31ma \x1b[1minner\x1b[0m b\x1b[0m"; got != want {
		t.Errorf("without preserve: got %q, want %q", got, want)
	}
	if got, want := s.PreserveResets().String(), "\x1b[31ma \x1b[1minner\x1b[0m\x1b[31m b\x1b[0m"; got != want {
		t.Errorf("with preserve: got %q, want %q", got, want)
	}
}

func TestOutputPreserveResetsDefault(t *testing.T) {
	o := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	got := o.String("x\x1b[mz").Bold().String()
	if want := "\x1b[1mx\x1b[m\x1b[1mz\x1b[0m"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	o = NewOutput(io.Discard, WithProfile(ANSI))
	if got := o.String("x\x1b[mz").Bold().String(); got != "\x1b[1mx\x1b[mz\x1b[0m" {
		t.Errorf("default should not preserve resets: %q", got)
	}
}

func TestStyleTruncate(t *testing.T) {
	s := ANSI.String("Hello World").Foreground(ANSIColor(1))
	if got, want := s.Truncate(6, TruncateOptions{Tail: "…"}), "\x1b[31mHello…\x1b[0m"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := s.Truncate(20, TruncateOptions{Tail: "…"}), s.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	a := Ascii.String("Hello World").Foreground(ANSIColor(1))
	if got := a.Truncate(6, TruncateOptions{Tail: "…"}); got != "Hello " {
		t.Errorf("ascii: got %q", got)
	}
}

func TestOutputTruncate(t *testing.T) {
	in := "\x1b[31mab\x1b[0mcdef"

	o := NewOutput(io.Discard, WithProfile(ANSI))
	if got, want := o.Truncate(in, 3, TruncateOptions{Tail: "."}), "\x1b[31mab\x1b[0m."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := o.Truncate(in, 3, TruncateOptions{Tail: ".", PreserveResets: true}), "\x1b[31mab\x1b[0m\x1b[31m.\x1b[0m"; got != want {
		t.Errorf("opts preserve: got %q, want %q", got, want)
	}

	op := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	if got, want := op.Truncate(in, 3, TruncateOptions{Tail: "."}), "\x1b[31mab\x1b[0m\x1b[31m.\x1b[0m"; got != want {
		t.Errorf("default preserve: got %q, want %q", got, want)
	}

	oa := NewOutput(io.Discard, WithProfile(Ascii))
	if got := oa.Truncate(in, 3, TruncateOptions{Tail: "."}); got != "ab." {
		t.Errorf("ascii: got %q", got)
	}
}

func TestTemplateTruncate(t *testing.T) {
	const tpl = `{{ Truncate 4 "…" (Bold "Hello") }}|{{ "Hello" | truncate 2 }}|{{ Foreground "1" (print "a " (Bold "b") " c") }}`
	tests := []struct {
		name string
		o    *Output
		want string
	}{
		{"ascii", NewOutput(io.Discard, WithProfile(Ascii)), "Hel…|He|a b c"},
		{"ansi", NewOutput(io.Discard, WithProfile(ANSI)), "\x1b[1mHel…\x1b[0m|He|\x1b[31ma \x1b[1mb\x1b[0m c\x1b[0m"},
		{
			"ansi preserve",
			NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true)),
			"\x1b[1mHel…\x1b[0m|He|\x1b[31ma \x1b[1mb\x1b[0m\x1b[31m c\x1b[0m",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := template.Must(template.New("t").Funcs(tt.o.TemplateFuncs()).Parse(tpl))
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, nil); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestANSIWrappers(t *testing.T) {
	s := "\x1b[1m漢\x1b[0mb"
	if StripANSI(s) != "漢b" || ANSIWidth(s) != 3 || !HasANSI(s) {
		t.Errorf("wrappers mismatch for %q", s)
	}
	if got := TruncateANSI(s, 2, TruncateOptions{}); got != "\x1b[1m漢\x1b[0m" {
		t.Errorf("TruncateANSI = %q", got)
	}
}
