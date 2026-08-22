package termenv

import (
	"strings"
	"testing"
)

func TestOutputStringInheritsPreserveResets(t *testing.T) {
	o := NewOutput(nil, WithProfile(TrueColor), WithPreserveResets(true))
	s := o.String("hello")
	if !s.preserveResets {
		t.Fatal("expected preserveResets inherited from output")
	}
}

func TestOutputTruncateAscii(t *testing.T) {
	o := NewOutput(nil, WithProfile(Ascii))
	s := "\x1b[31mHello World\x1b[0m"
	got := o.Truncate(s, 8, TruncateOptions{Tail: "…"})
	if HasANSI(got) {
		t.Fatalf("ascii truncate should strip ansi, got %q", got)
	}
	if got != "Hello W…" {
		t.Fatalf("expected truncated text with tail, got %q", got)
	}
}

func TestOutputTruncatePreserveResetsDefault(t *testing.T) {
	o := NewOutput(nil, WithProfile(TrueColor), WithPreserveResets(true))
	s := "\x1b[1mBold \x1b[0mrest"
	got := o.Truncate(s, 20, TruncateOptions{})
	if !strings.Contains(got, "\x1b[1m") {
		t.Fatalf("expected preserve-resets from output default, got %q", got)
	}
}

func TestStyleTruncateAscii(t *testing.T) {
	s := Ascii.String("Hello World").Bold()
	got := s.Truncate(5, TruncateOptions{Tail: "…"})
	if got != "Hello" {
		t.Fatalf("ascii style truncate should omit tail, got %q", got)
	}
	if HasANSI(got) {
		t.Fatalf("ascii style truncate should be plain text, got %q", got)
	}
}

func TestStyleTruncateWithEnclosing(t *testing.T) {
	s := String("hello reset world").Foreground(ANSI.Color("1")).PreserveResets()
	content := "hi \x1b[0m there"
	s.string = content
	got := s.Truncate(20, TruncateOptions{})
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("expected foreground reapplied after reset, got %q", got)
	}
}

func TestStylePreserveResets(t *testing.T) {
	s := String("x").PreserveResets()
	if !s.preserveResets {
		t.Fatal("expected preserveResets enabled")
	}
}

func TestTermenvWrappers(t *testing.T) {
	s := "\x1b[1mHi\x1b[0m"
	if !HasANSI(s) {
		t.Fatal("HasANSI failed")
	}
	if StripANSI(s) != "Hi" {
		t.Fatalf("StripANSI failed: %q", StripANSI(s))
	}
	if ANSIWidth(s) != 2 {
		t.Fatalf("ANSIWidth failed: %d", ANSIWidth(s))
	}
}
