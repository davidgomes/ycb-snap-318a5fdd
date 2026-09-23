package ansi

import (
	"strings"
	"testing"
)

func TestTokenizeRoundTrip(t *testing.T) {
	samples := []string{
		"plain",
		"\x1b[1mBold\x1b[0m",
		"\x1b[mreset",
		"\x1b[0;1m",
		"\x1b[31;0m",
		"\x1b[00m",
		"\x1b[38;2;0;0;255mblue",
		"\x1b[38;2;255;0;0mred",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		"\x1b]8;;https://example.com\alink\x1b]8;;\a",
		"ab\x1b[10Ccd",
		"a\u200b你",
	}
	for _, s := range samples {
		var b strings.Builder
		for _, tok := range Tokenize(s) {
			b.WriteString(tok.Raw)
		}
		if b.String() != s {
			t.Fatalf("round trip mismatch:\n got %q\nwant %q", b.String(), s)
		}
	}
}

func TestResetClassification(t *testing.T) {
	resets := []string{"\x1b[m", "\x1b[0m", "\x1b[00m", "\x1b[0;1m", "\x1b[1;0m", "\x1b[31;0;4m"}
	for _, s := range resets {
		toks := Tokenize(s)
		if len(toks) != 1 || toks[0].Type != TokenReset {
			t.Fatalf("%q classified as %#v, want TokenReset", s, toks)
		}
	}

	sgr := []string{"\x1b[1m", "\x1b[31m", "\x1b[38;2;10;20;30m"}
	for _, s := range sgr {
		toks := Tokenize(s)
		if len(toks) != 1 || toks[0].Type != TokenSGR {
			t.Fatalf("%q classified as %#v, want TokenSGR", s, toks)
		}
	}
	for _, s := range []string{"\x1b[38;2;0;0;255m", "\x1b[38;5;0m", "\x1b[1;38;2;255;0;0m"} {
		toks := Tokenize(s)
		if len(toks) != 1 || toks[0].Type != TokenReset {
			t.Fatalf("%q classified as %#v, want TokenReset", s, toks)
		}
	}

	open := Tokenize("\x1b]8;;https://example.com\x1b\\")
	if open[0].Type != TokenHyperlinkOpen || open[0].Text != "https://example.com" {
		t.Fatalf("hyperlink open: %#v", open)
	}
	closeTok := Tokenize("\x1b]8;;\x1b\\")
	if closeTok[0].Type != TokenHyperlinkClose {
		t.Fatalf("hyperlink close: %#v", closeTok)
	}
}

func TestStripWidthHas(t *testing.T) {
	s := "\x1b[1ma\u200b你\x1b[0m"
	if got := StripANSI(s); got != "a\u200b你" {
		t.Fatalf("StripANSI = %q", got)
	}
	if got := ANSIWidth(s); got != 3 {
		t.Fatalf("ANSIWidth = %d, want 3", got)
	}
	if ANSIWidth("\u200b") != 0 {
		t.Fatal("ZWSP width")
	}
	if ANSIWidth("你") != 2 {
		t.Fatal("wide rune width")
	}
	if !HasANSI(s) || HasANSI("plain") {
		t.Fatal("HasANSI")
	}
}

func TestTruncate(t *testing.T) {
	bold := "\x1b[1m"
	reset := "\x1b[0m"

	if got := TruncateANSI(bold+"HelloWorld"+reset, 5, TruncateOptions{Tail: "…"}); got != bold+"Hell…"+reset {
		t.Fatalf("tail inherit: %q", got)
	}

	// Tail counts toward width. Wide rune that does not fit is dropped whole.
	if got := TruncateANSI("a你b", 2, TruncateOptions{}); got != "a" {
		t.Fatalf("wide boundary: %q", got)
	}
	if got := TruncateANSI("你", 1, TruncateOptions{}); got != "" {
		t.Fatalf("wide alone: %q", got)
	}

	// Zero-width space does not consume a cell. CSI is not split.
	in := "ab\x1b[10C\u200bcd"
	if got := TruncateANSI(in, 3, TruncateOptions{}); got != "ab\x1b[10C\u200bc" {
		t.Fatalf("csi/zwsp: %q", got)
	}

	// Unclosed style and hyperlink are closed. Sequences before the cut stay intact.
	link := "\x1b]8;;http://x\x1b\\Hello"
	want := "\x1b]8;;http://x\x1b\\Hell\x1b]8;;\x1b\\"
	if got := TruncateANSI(link, 4, TruncateOptions{}); got != want {
		t.Fatalf("hyperlink: %q", got)
	}

	// A color payload that contains a 0 parameter is a reset, so it does not
	// leave an active style that needs another final reset.
	colored := "\x1b[38;2;0;0;255mHello"
	if got := TruncateANSI(colored, 4, TruncateOptions{Tail: "…"}); got != "\x1b[38;2;0;0;255mHel…" {
		t.Fatalf("color with zero components: %q", got)
	}
	kept := "\x1b[38;2;10;20;30mHello"
	if got := TruncateANSI(kept, 4, TruncateOptions{Tail: "…"}); got != "\x1b[38;2;10;20;30mHel…"+reset {
		t.Fatalf("color without zero: %q", got)
	}

	// Preserve-resets re-opens after a reset run, once.
	src := bold + "Hello" + reset + reset + "World"
	got := TruncateANSI(src, 100, TruncateOptions{PreserveResets: true})
	want = bold + "Hello" + reset + reset + bold + "World" + reset
	if got != want {
		t.Fatalf("preserve run:\n got %q\nwant %q", got, want)
	}

	// No re-open when nothing follows the reset.
	src = bold + "Hello" + reset
	if got := TruncateANSI(src, 100, TruncateOptions{PreserveResets: true}); got != src {
		t.Fatalf("trailing reset changed: %q", got)
	}

	// After a reset, the tail inherits the restored style.
	src = bold + "ab" + reset + "cd"
	got = TruncateANSI(src, 3, TruncateOptions{Tail: "!", PreserveResets: true})
	want = bold + "ab" + reset + bold + "!" + reset
	if got != want {
		t.Fatalf("preserve tail:\n got %q\nwant %q", got, want)
	}

	if got := TruncateANSI("abcdef", 4, TruncateOptions{Tail: ".."}); got != "ab.." {
		t.Fatalf("plain tail: %q", got)
	}
	if got := TruncateANSI("hi", 4, TruncateOptions{Tail: ".."}); got != "hi" {
		t.Fatalf("no tail when it fits: %q", got)
	}
}
