package termenv

import (
	"bytes"
	"io"
	"testing"
	"text/template"
)

func TestStylePreserveResets(t *testing.T) {
	s := String("A\x1b[0mB").Bold().PreserveResets()
	got := s.String()
	want := "\x1b[1mA\x1b[0m\x1b[1mB\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	run := String("A\x1b[0m\x1b[mB").Bold().PreserveResets().String()
	want = "\x1b[1mA\x1b[0m\x1b[m\x1b[1mB\x1b[0m"
	if run != want {
		t.Fatalf("run %q want %q", run, want)
	}

	plain := String("A\x1b[31mB").Bold().PreserveResets().String()
	want = "\x1b[1mA\x1b[31mB\x1b[0m"
	if plain != want {
		t.Fatalf("non-reset %q want %q", plain, want)
	}
}

func TestStyleTruncate(t *testing.T) {
	s := String("hello").Bold()
	got := s.Truncate(4, TruncateOptions{Tail: ".."})
	want := "\x1b[1mhe..\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	ascii := Ascii.String("\x1b[31mhello").Truncate(4, TruncateOptions{Tail: ".."})
	if ascii != "hell" {
		t.Fatalf("ascii style = %q", ascii)
	}

	red := TrueColor.String("hello").Foreground(TrueColor.Color("#ff0000"))
	got = red.Truncate(4, TruncateOptions{Tail: "."})
	want = "\x1b[38;2;255;0;0mhel.\x1b[0m"
	if got != want {
		t.Fatalf("truecolor = %q want %q", got, want)
	}
}

func TestOutputTruncateAndString(t *testing.T) {
	o := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	got := o.String("A\x1b[0mB").Bold().String()
	want := "\x1b[1mA\x1b[0m\x1b[1mB\x1b[0m"
	if got != want {
		t.Fatalf("inherited = %q want %q", got, want)
	}

	got = o.Truncate("\x1b[31mA\x1b[0mB", 10, TruncateOptions{})
	want = "\x1b[31mA\x1b[0m\x1b[31mB\x1b[0m"
	if got != want {
		t.Fatalf("output preserve = %q want %q", got, want)
	}

	off := NewOutput(io.Discard, WithProfile(ANSI))
	got = off.Truncate("\x1b[31mA\x1b[0mB", 10, TruncateOptions{PreserveResets: true})
	if got != want {
		t.Fatalf("opt preserve = %q want %q", got, want)
	}
	got = off.Truncate("\x1b[31mhello\x1b[0m", 4, TruncateOptions{Tail: "."})
	want = "\x1b[31mhel.\x1b[0m"
	if got != want {
		t.Fatalf("output trunc = %q want %q", got, want)
	}

	ascii := NewOutput(io.Discard, WithProfile(Ascii))
	got = ascii.Truncate("\x1b[31mhello", 4, TruncateOptions{Tail: ".."})
	if got != "he.." {
		t.Fatalf("ascii output = %q", got)
	}
}

func TestTemplateTruncate(t *testing.T) {
	o := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	tpl := template.Must(template.New("t").Funcs(o.TemplateFuncs()).Parse(
		"{{ Bold \"A\x1b[0mB\" }}|{{ Truncate 4 \"..\" \"hello\" }}|{{ truncate 4 \"hello\" }}",
	))
	var out bytes.Buffer
	if err := tpl.Execute(&out, nil); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[1mA\x1b[0m\x1b[1mB\x1b[0m|he..|hell"
	if out.String() != want {
		t.Fatalf("template = %q want %q", out.String(), want)
	}

	ascii := NewOutput(io.Discard, WithProfile(Ascii))
	tpl = template.Must(template.New("a").Funcs(ascii.TemplateFuncs()).Parse(
		"{{ Truncate 4 \"..\" \"\x1b[31mhello\" }}",
	))
	out.Reset()
	if err := tpl.Execute(&out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "he.." {
		t.Fatalf("ascii template = %q", out.String())
	}
}
