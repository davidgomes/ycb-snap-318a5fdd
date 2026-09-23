package ansi

import "testing"

func TestTokenize(t *testing.T) {
	const link = "\x1b]8;;https://example.com\x1b\\example\x1b]8;;\x1b\\"
	tokens := Tokenize("\x1b[31mred\x1b[0m " + link)
	want := []TokenType{
		TokenSGR,
		TokenText,
		TokenReset,
		TokenText,
		TokenHyperlinkOpen,
		TokenText,
		TokenHyperlinkClose,
	}
	if len(tokens) != len(want) {
		t.Fatalf("len=%d tokens=%#v", len(tokens), tokens)
	}
	for i, typ := range want {
		if tokens[i].Type != typ {
			t.Fatalf("token %d type=%d want %d raw=%q", i, tokens[i].Type, typ, tokens[i].Raw)
		}
	}
	if tokens[0].Text != "31" || tokens[0].Raw != "\x1b[31m" {
		t.Fatalf("sgr token = %#v", tokens[0])
	}
	if tokens[2].Text != "0" {
		t.Fatalf("reset text = %q", tokens[2].Text)
	}
	if tokens[4].Text != "https://example.com" {
		t.Fatalf("uri = %q", tokens[4].Text)
	}
}

func TestResetDetection(t *testing.T) {
	resets := []string{"\x1b[m", "\x1b[0m", "\x1b[00m", "\x1b[0;31m", "\x1b[31;0m", "\x1b[1;0;4m"}
	for _, seq := range resets {
		toks := Tokenize(seq)
		if len(toks) != 1 || toks[0].Type != TokenReset {
			t.Fatalf("%q classified as %#v", seq, toks)
		}
	}
	plain := []string{"\x1b[31m", "\x1b[1;31m", "\x1b[01m"}
	for _, seq := range plain {
		toks := Tokenize(seq)
		if len(toks) != 1 || toks[0].Type != TokenSGR {
			t.Fatalf("%q classified as %#v", seq, toks)
		}
	}
}

func TestStripWidthHas(t *testing.T) {
	s := "\x1b[31m你\u200bA\x1b[0m"
	if !HasANSI(s) {
		t.Fatal("expected ANSI")
	}
	if HasANSI("plain") {
		t.Fatal("plain reported ANSI")
	}
	if got := StripANSI(s); got != "你\u200bA" {
		t.Fatalf("strip = %q", got)
	}
	if got := ANSIWidth(s); got != 3 {
		t.Fatalf("width = %d", got)
	}
	if got := ANSIWidth("\u200b"); got != 0 {
		t.Fatalf("zwsp width = %d", got)
	}
	if got := ANSIWidth("你"); got != 2 {
		t.Fatalf("wide width = %d", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := TruncateANSI("hello", 10, TruncateOptions{}); got != "hello" {
		t.Fatalf("fit = %q", got)
	}
	if got := TruncateANSI("hello", 4, TruncateOptions{Tail: ".."}); got != "he.." {
		t.Fatalf("tail = %q", got)
	}
	if got := TruncateANSI("hello", 5, TruncateOptions{Tail: ".."}); got != "hello" {
		t.Fatalf("exact = %q", got)
	}
	if got := TruncateANSI("hello", 0, TruncateOptions{Tail: "x"}); got != "" {
		t.Fatalf("zero = %q", got)
	}
	if got := TruncateANSI("你a", 2, TruncateOptions{}); got != "你" {
		t.Fatalf("wide = %q", got)
	}
	if got := TruncateANSI("你", 1, TruncateOptions{}); got != "" {
		t.Fatalf("wide overflow = %q", got)
	}
	if got := TruncateANSI("a\u200bb", 1, TruncateOptions{}); got != "a\u200b" {
		t.Fatalf("zwsp = %q", got)
	}

	styled := "\x1b[31mhello\x1b[0m"
	if got := TruncateANSI(styled, 4, TruncateOptions{}); got != "\x1b[31mhell\x1b[0m" {
		t.Fatalf("style = %q", got)
	}
	if got := TruncateANSI(styled, 4, TruncateOptions{Tail: "."}); got != "\x1b[31mhel.\x1b[0m" {
		t.Fatalf("styled tail = %q", got)
	}

	// Do not split a CSI sequence that sits on the cut.
	if got := TruncateANSI("ab\x1b[31mcd", 3, TruncateOptions{}); got != "ab\x1b[31mc\x1b[0m" {
		t.Fatalf("split = %q", got)
	}

	link := "\x1b]8;;https://example.com\x1b\\example\x1b]8;;\x1b\\"
	if got := TruncateANSI(link, 3, TruncateOptions{}); got != "\x1b]8;;https://example.com\x1b\\exa\x1b]8;;\x1b\\" {
		t.Fatalf("link = %q", got)
	}
	bel := "\x1b]8;;https://example.com\aexample\x1b]8;;\a"
	if got := TruncateANSI(bel, 3, TruncateOptions{}); got != "\x1b]8;;https://example.com\aexa\x1b]8;;\a" {
		t.Fatalf("bel link = %q", got)
	}
}

func TestPreserveResets(t *testing.T) {
	in := "\x1b[31mA\x1b[0mB"
	got := TruncateANSI(in, 10, TruncateOptions{PreserveResets: true})
	want := "\x1b[31mA\x1b[0m\x1b[31mB\x1b[0m"
	if got != want {
		t.Fatalf("preserve = %q want %q", got, want)
	}

	run := "\x1b[1mA\x1b[0m\x1b[mB"
	got = TruncateANSI(run, 10, TruncateOptions{PreserveResets: true})
	want = "\x1b[1mA\x1b[0m\x1b[m\x1b[1mB\x1b[0m"
	if got != want {
		t.Fatalf("run = %q want %q", got, want)
	}

	// A reset that does not continue is left closed.
	closed := "\x1b[31mA\x1b[0m"
	got = TruncateANSI(closed, 10, TruncateOptions{PreserveResets: true})
	if got != closed {
		t.Fatalf("closed = %q", got)
	}

	mixed := "\x1b[0;31mhello"
	got = TruncateANSI(mixed, 4, TruncateOptions{})
	want = "\x1b[0;31mhell\x1b[0m"
	if got != want {
		t.Fatalf("reset-and-color = %q want %q", got, want)
	}

	// No reset to preserve: a string that already fits is unchanged.
	open := "\x1b[31mhello"
	got = TruncateANSI(open, 10, TruncateOptions{PreserveResets: true})
	if got != open {
		t.Fatalf("open fit = %q", got)
	}
}
