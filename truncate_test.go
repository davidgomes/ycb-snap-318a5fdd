package termenv

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"text/template"
)

func TestPreserveResetsStyle(t *testing.T) {
	got := String("a\x1b[0mb").Bold().PreserveResets().String()
	want := "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	plain := String("a\x1b[0mb").Bold().String()
	wantPlain := "\x1b[1ma\x1b[0mb\x1b[0m"
	if plain != wantPlain {
		t.Fatalf("without preserve got %q want %q", plain, wantPlain)
	}

	// A zero color channel must not swallow the enclosing style: #ff0000 is
	// 38;2;255;0;0, which contains a 0 parameter but still has to be re-opened.
	rgb := String("a\x1b[0mb").Foreground(RGBColor("#ff0000")).Bold().PreserveResets().String()
	wantRGB := "\x1b[38;2;255;0;0;1ma\x1b[0m\x1b[38;2;255;0;0;1mb\x1b[0m"
	if rgb != wantRGB {
		t.Fatalf("rgb preserve got %q want %q", rgb, wantRGB)
	}
}

func TestOutputStringInheritsPreserve(t *testing.T) {
	o := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	got := o.String("a\x1b[0mb").Bold().String()
	want := "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	off := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(false))
	if off.String("a\x1b[0mb").Bold().String() != "\x1b[1ma\x1b[0mb\x1b[0m" {
		t.Fatalf("default off: %q", off.String("a\x1b[0mb").Bold().String())
	}
}

func TestStyleTruncate(t *testing.T) {
	got := String("hello world").Bold().Truncate(5, TruncateOptions{Tail: "…"})
	want := "\x1b[1mhell…\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	ascii := Ascii.String("\x1b[1mhello world").Truncate(5, TruncateOptions{Tail: "…"})
	if ascii != "hello" {
		t.Fatalf("ascii style truncate %q", ascii)
	}
	if strings.Contains(ascii, "\x1b") {
		t.Fatal("ascii emitted ANSI")
	}
}

func TestOutputTruncate(t *testing.T) {
	o := NewOutput(io.Discard, WithProfile(ANSI))
	got := o.Truncate("\x1b[31mhello", 4, TruncateOptions{Tail: "…"})
	want := "\x1b[31mhel…\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	preserving := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	got = preserving.Truncate("\x1b[1ma\x1b[0mb", 10, TruncateOptions{})
	want = "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m"
	if got != want {
		t.Fatalf("output default preserve got %q want %q", got, want)
	}

	ascii := NewOutput(io.Discard, WithProfile(Ascii))
	got = ascii.Truncate("\x1b[1mhello world", 7, TruncateOptions{Tail: "…"})
	if got != "hello …" {
		t.Fatalf("ascii output truncate %q", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatal("ascii output emitted ANSI")
	}
}

func TestTemplateTruncateAndPreserve(t *testing.T) {
	o := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	tpl, err := template.New("t").Funcs(o.TemplateFuncs()).Parse(`{{ Bold "a\x1b[0mb" }}|{{ Truncate 4 "…" "hello" }}|{{ truncate 3 "hello" }}`)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, nil); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m|hel…|hel"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}

	ascii := NewOutput(io.Discard, WithProfile(Ascii))
	atpl, err := template.New("a").Funcs(ascii.TemplateFuncs()).Parse(`{{ Truncate 4 ".." "\x1b[1mhello" }}|{{ Bold "x" }}`)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := atpl.Execute(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "he..|x" {
		t.Fatalf("ascii template %q", buf.String())
	}
}

func TestWrappers(t *testing.T) {
	if !HasANSI("\x1b[0m") || HasANSI("x") {
		t.Fatal("HasANSI wrapper")
	}
	if StripANSI("\x1b[1mhi\x1b[0m") != "hi" {
		t.Fatal("StripANSI wrapper")
	}
	if ANSIWidth("你") != 2 {
		t.Fatal("ANSIWidth wrapper")
	}
	if TruncateANSI("abcd", 2, TruncateOptions{}) != "ab" {
		t.Fatal("TruncateANSI wrapper")
	}
}
