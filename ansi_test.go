package termenv

import (
	"bytes"
	"testing"
	"text/template"
)

func TestStylePreserveResets(t *testing.T) {
	inner := ANSI.String("b").Foreground(ANSIRed).String()
	s := ANSI.String("a" + inner + "c").Bold()

	if got, want := s.String(), "\x1b[1ma\x1b[31mb\x1b[0mc\x1b[0m"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := s.PreserveResets().String(), "\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc\x1b[0m"; got != want {
		t.Errorf("PreserveResets().String() = %q, want %q", got, want)
	}
}

func TestStyleTruncate(t *testing.T) {
	s := ANSI.String("hello world").Bold()
	if got, want := s.Truncate(6, TruncateOptions{Tail: "…"}), "\x1b[1mhello…\x1b[0m"; got != want {
		t.Errorf("Truncate = %q, want %q", got, want)
	}

	a := Ascii.String("hello world").Bold()
	if got, want := a.Truncate(6, TruncateOptions{Tail: "…"}), "hello "; got != want {
		t.Errorf("Ascii Truncate = %q, want %q", got, want)
	}

	inner := ANSI.String("b").Foreground(ANSIRed).String()
	p := ANSI.String("a" + inner + "cdef").Bold()
	want := "\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc…\x1b[0m"
	if got := p.Truncate(4, TruncateOptions{Tail: "…", PreserveResets: true}); got != want {
		t.Errorf("Truncate preserve = %q, want %q", got, want)
	}
	if got := p.PreserveResets().Truncate(4, TruncateOptions{Tail: "…"}); got != want {
		t.Errorf("PreserveResets().Truncate = %q, want %q", got, want)
	}
}

func TestOutputPreserveResets(t *testing.T) {
	o := NewOutput(&bytes.Buffer{}, WithProfile(ANSI), WithPreserveResets(true))
	inner := ANSI.String("b").Foreground(ANSIRed).String()

	if got, want := o.String("a"+inner+"c").Bold().String(), "\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc\x1b[0m"; got != want {
		t.Errorf("Output.String = %q, want %q", got, want)
	}

	in := "\x1b[1ma" + inner + "cdef\x1b[0m"
	want := "\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc…\x1b[0m"
	if got := o.Truncate(in, 4, TruncateOptions{Tail: "…"}); got != want {
		t.Errorf("Output.Truncate = %q, want %q", got, want)
	}

	plain := NewOutput(&bytes.Buffer{}, WithProfile(ANSI))
	if got := plain.Truncate(in, 4, TruncateOptions{Tail: "…", PreserveResets: true}); got != want {
		t.Errorf("Output.Truncate opts = %q, want %q", got, want)
	}

	ascii := NewOutput(&bytes.Buffer{}, WithProfile(Ascii))
	if got, want := ascii.Truncate(in, 4, TruncateOptions{Tail: "…"}), "abc…"; got != want {
		t.Errorf("Ascii Output.Truncate = %q, want %q", got, want)
	}
}

func TestTemplateTruncate(t *testing.T) {
	tests := []struct {
		name string
		o    *Output
		tpl  string
		want string
	}{
		{
			"ansi", NewOutput(&bytes.Buffer{}, WithProfile(ANSI)),
			`{{ Bold "hello" | Truncate 3 "~" }}|{{ "hello" | truncate 2 }}`,
			"\x1b[1mhe~\x1b[0m|he",
		},
		{
			"ascii", NewOutput(&bytes.Buffer{}, WithProfile(Ascii)),
			`{{ Bold "hello" | Truncate 3 "~" }}|{{ "hello" | truncate 2 }}`,
			"he~|he",
		},
		{
			"preserve", NewOutput(&bytes.Buffer{}, WithProfile(ANSI), WithPreserveResets(true)),
			`{{ Bold (printf "a%sc" (Foreground "1" "b")) }}`,
			"\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc\x1b[0m",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpl := template.Must(template.New("t").Funcs(tt.o.TemplateFuncs()).Parse(tt.tpl))
			var buf bytes.Buffer
			if err := tpl.Execute(&buf, nil); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
