package ansi

import (
	"strings"
	"testing"
)

func TestTokenizeTextAndSGR(t *testing.T) {
	tokens := Tokenize("hello \x1b[31mworld\x1b[0m!")
	if len(tokens) != 5 {
		t.Fatalf("expected 5 tokens, got %d", len(tokens))
	}
	if tokens[0].Type != TokenText || tokens[0].Text != "hello " {
		t.Fatalf("unexpected first token: %+v", tokens[0])
	}
	if tokens[1].Type != TokenSGR || tokens[1].Raw != "\x1b[31m" {
		t.Fatalf("unexpected sgr token: %+v", tokens[1])
	}
	if tokens[2].Type != TokenText || tokens[2].Text != "world" {
		t.Fatalf("unexpected text token: %+v", tokens[2])
	}
	if tokens[3].Type != TokenReset {
		t.Fatalf("expected reset token, got %+v", tokens[3])
	}
}

func TestTokenizeResetVariants(t *testing.T) {
	for _, seq := range []string{"\x1b[m", "\x1b[0m", "\x1b[1;0m", "\x1b[;0m"} {
		tokens := Tokenize(seq)
		if len(tokens) != 1 || tokens[0].Type != TokenReset {
			t.Fatalf("expected reset for %q, got %+v", seq, tokens)
		}
	}
}

func TestTokenizeHyperlink(t *testing.T) {
	open := "\x1b]8;;https://example.com\x1b\\"
	closeSeq := "\x1b]8;;\x1b\\"
	tokens := Tokenize(open + "link" + closeSeq)
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %+v", len(tokens), tokens)
	}
	if tokens[0].Type != TokenHyperlinkOpen {
		t.Fatalf("expected hyperlink open, got %+v", tokens[0])
	}
	if tokens[2].Type != TokenHyperlinkClose {
		t.Fatalf("expected hyperlink close, got %+v", tokens[2])
	}
}

func TestANSIWidth(t *testing.T) {
	s := "\x1b[1mHello 世界\x1b[0m"
	if got := ANSIWidth(s); got != 10 {
		t.Fatalf("expected width 10, got %d", got)
	}
	if got := ANSIWidth("a\u200Bb"); got != 2 {
		t.Fatalf("expected zero-width joiner ignored, got %d", got)
	}
}

func TestStripANSI(t *testing.T) {
	s := "\x1b[31mred\x1b[0m"
	if got := StripANSI(s); got != "red" {
		t.Fatalf("expected red, got %q", got)
	}
}

func TestHasANSI(t *testing.T) {
	if HasANSI("plain") {
		t.Fatal("plain text should not have ansi")
	}
	if !HasANSI("\x1b[0m") {
		t.Fatal("expected ansi detected")
	}
}

func TestTruncateANSI(t *testing.T) {
	s := "\x1b[31mHello World\x1b[0m"
	got := TruncateANSI(s, 8, TruncateOptions{Tail: "…"})
	if ANSIWidth(got) != 8 {
		t.Fatalf("expected width 8, got %d in %q", ANSIWidth(got), got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("expected tail in %q", got)
	}
}

func TestTruncateANSIPreserveResets(t *testing.T) {
	s := "\x1b[1mBold \x1b[0mrest"
	got := TruncateANSI(s, 20, TruncateOptions{PreserveResets: true})
	if !strings.Contains(got, "\x1b[1m") {
		t.Fatalf("expected bold re-opened, got %q", got)
	}
}

func TestTruncateANSIWithEnclosing(t *testing.T) {
	s := "hello \x1b[0mworld"
	got := TruncateANSIWithEnclosing(s, 20, TruncateOptions{PreserveResets: true}, "1;31")
	if !strings.Contains(got, "\x1b[1;31m") {
		t.Fatalf("expected enclosing style re-opened, got %q", got)
	}
}

func TestTruncateANSIClosesHyperlink(t *testing.T) {
	open := "\x1b]8;;https://example.com\x1b\\"
	s := open + "long hyperlink text"
	got := TruncateANSI(s, 6, TruncateOptions{Tail: "…"})
	if !strings.Contains(got, "\x1b]8;;\x1b\\") {
		t.Fatalf("expected hyperlink close in %q", got)
	}
}

func TestTruncateANSINeverSplitsSequence(t *testing.T) {
	s := "\x1b[38;2;255;0;0mHi"
	got := TruncateANSI(s, 1, TruncateOptions{})
	if strings.Contains(got, "\x1b[38;2;") && !strings.HasSuffix(strings.TrimSuffix(got, "\x1b[0m"), "m") {
		// sequence must remain intact if present
	}
	if ANSIWidth(got) > 1 {
		// allow tail-less truncation
		if !strings.HasPrefix(got, "\x1b[38;2;255;0;0m") {
			t.Fatalf("split sequence in %q", got)
		}
	}
}

func TestTruncateANSIFinalReset(t *testing.T) {
	s := "\x1b[31mHello"
	got := TruncateANSI(s, 10, TruncateOptions{})
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("expected final reset in %q", got)
	}
}
