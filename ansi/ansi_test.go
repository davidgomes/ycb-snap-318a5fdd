package ansi

import (
	"strings"
	"testing"
)

func TestTokenizeRoundTrip(t *testing.T) {
	samples := []string{
		"",
		"plain",
		"你a\u200bb",
		"\x1b[m",
		"\x1b[0m",
		"\x1b[00m",
		"\x1b[1m",
		"\x1b[01m",
		"\x1b[31m",
		"\x1b[1;31m",
		"\x1b[1;0m",
		"\x1b[0;31m",
		"\x1b[38;5;196m",
		"\x1b[38;5;0m",
		"\x1b[38;2;255;0;0m",
		"\x1b[38;2;1;2;3m",
		"\x1b[1A",
		"hello\x1b[1mworld\x1b[0m",
		"\x1b]8;;https://example.com\x1b\\click\x1b]8;;\x1b\\",
		"\x1b]8;;https://example.com\aclick\x1b]8;;\a",
		"\x1b]8;id=1;https://example.com\x1b\\x",
		"\x1b]0;title\a",
		"\x1b[1m你\x1b[0m",
	}
	for _, s := range samples {
		var b strings.Builder
		for _, tok := range Tokenize(s) {
			b.WriteString(tok.Raw)
		}
		if b.String() != s {
			t.Fatalf("round trip %q -> %q", s, b.String())
		}
	}
}

func TestTokenTypes(t *testing.T) {
	link := "\x1b]8;;https://example.com\x1b\\click\x1b]8;;\x1b\\"
	toks := Tokenize(link)
	if len(toks) != 3 {
		t.Fatalf("hyperlink tokens: %#v", toks)
	}
	if toks[0].Type != TokenHyperlinkOpen || toks[0].Text != "" {
		t.Fatalf("open: %#v", toks[0])
	}
	if toks[1].Type != TokenText || toks[1].Text != "click" || toks[1].Raw != "click" {
		t.Fatalf("text: %#v", toks[1])
	}
	if toks[2].Type != TokenHyperlinkClose {
		t.Fatalf("close: %#v", toks[2])
	}

	resets := []string{"\x1b[m", "\x1b[0m", "\x1b[00m", "\x1b[0;1m", "\x1b[1;0m", "\x1b[38;5;0m", "\x1b[38;2;255;0;0m"}
	for _, s := range resets {
		got := Tokenize(s)
		if len(got) != 1 || got[0].Type != TokenReset {
			t.Fatalf("%q type %#v, want reset", s, got)
		}
	}
	keeps := []string{"\x1b[1m", "\x1b[31m", "\x1b[01m", "\x1b[10m", "\x1b[38;5;196m", "\x1b[38;2;1;2;3m"}
	for _, s := range keeps {
		got := Tokenize(s)
		if len(got) != 1 || got[0].Type != TokenSGR {
			t.Fatalf("%q type %#v, want sgr", s, got)
		}
	}
}

func TestWidthAndStrip(t *testing.T) {
	if ANSIWidth("你") != 2 {
		t.Fatalf("wide width %d", ANSIWidth("你"))
	}
	if ANSIWidth("a\u200bb") != 2 {
		t.Fatalf("zwsp width %d", ANSIWidth("a\u200bb"))
	}
	if ANSIWidth("\u200b") != 0 {
		t.Fatalf("lone zwsp")
	}
	styled := "\x1b[1m你\x1b[0m"
	if ANSIWidth(styled) != 2 {
		t.Fatalf("styled width %d", ANSIWidth(styled))
	}
	if StripANSI(styled) != "你" {
		t.Fatalf("strip %q", StripANSI(styled))
	}
	if HasANSI("plain") || !HasANSI(styled) {
		t.Fatal("HasANSI")
	}
	if ANSIWidth("\x1b]8;;https://example.com\x1b\\hi\x1b]8;;\x1b\\") != 2 {
		t.Fatal("link width")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
		opts  TruncateOptions
		want  string
	}{
		{"fit", "foo", 10, TruncateOptions{}, "foo"},
		{"cut", "foobar", 3, TruncateOptions{}, "foo"},
		{"tail", "foobar", 4, TruncateOptions{Tail: "."}, "foo."},
		{"tail wider", "foo", 2, TruncateOptions{Tail: "..."}, "..."},
		{"wide fit", "你", 2, TruncateOptions{}, "你"},
		{"wide drop", "你", 1, TruncateOptions{}, ""},
		{"wide tail", "你好", 3, TruncateOptions{Tail: "…"}, "你…"},
		{"zwsp", "a\u200bb", 1, TruncateOptions{}, "a\u200b"},
		{"style", "\x1b[1mabcd", 2, TruncateOptions{}, "\x1b[1mab\x1b[0m"},
		{"style tail", "\x1b[31mHello", 4, TruncateOptions{Tail: "…"}, "\x1b[31mHel…\x1b[0m"},
		{"no split", "\x1b[38;2;1;2;3mXYZ", 1, TruncateOptions{}, "\x1b[38;2;1;2;3mX\x1b[0m"},
		{"truecolor zero component", "\x1b[38;2;255;0;0mHello", 2, TruncateOptions{}, "\x1b[38;2;255;0;0mHe\x1b[0m"},
		{"exact", "foo", 3, TruncateOptions{Tail: "."}, "foo"},
		{"zero", "foo", 0, TruncateOptions{Tail: "x"}, ""},
		{"link", "\x1b]8;;https://example.com\x1b\\hello\x1b]8;;\x1b\\", 4, TruncateOptions{Tail: ".."}, "\x1b]8;;https://example.com\x1b\\he..\x1b]8;;\x1b\\"},
		{"link bel", "\x1b]8;;https://example.com\ahello", 2, TruncateOptions{}, "\x1b]8;;https://example.com\ahe\x1b]8;;\a"},
		{"preserve", "\x1b[1ma\x1b[0mb", 10, TruncateOptions{PreserveResets: true}, "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m"},
		{"preserve run", "\x1b[1ma\x1b[0m\x1b[mb", 10, TruncateOptions{PreserveResets: true}, "\x1b[1ma\x1b[0m\x1b[m\x1b[1mb\x1b[0m"},
		{"preserve no extra at end", "\x1b[1mhello\x1b[0m", 10, TruncateOptions{PreserveResets: true}, "\x1b[1mhello\x1b[0m"},
		{"preserve nested", "\x1b[1mA\x1b[31mB\x1b[0mC", 10, TruncateOptions{PreserveResets: true}, "\x1b[1mA\x1b[31mB\x1b[0m\x1b[1m\x1b[31mC\x1b[0m"},
		{"preserve cut tail", "\x1b[1mabcdef\x1b[0m", 4, TruncateOptions{Tail: "…", PreserveResets: true}, "\x1b[1mabc…\x1b[0m"},
		{"spaces tail", "    ", 2, TruncateOptions{Tail: "…"}, " …"},
		{"cursor not split", "ab\x1b[1Ccd", 2, TruncateOptions{}, "ab"},
		{"cursor kept", "a\x1b[1Cb", 2, TruncateOptions{}, "a\x1b[1Cb"},
		{"tail inherits", "\x1b[31mhello", 1, TruncateOptions{Tail: "…"}, "\x1b[31m…\x1b[0m"},
		{"rgb preserve", "\x1b[38;2;255;0;0ma\x1b[0mb", 10, TruncateOptions{PreserveResets: true}, "\x1b[38;2;255;0;0ma\x1b[0m\x1b[38;2;255;0;0mb\x1b[0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateANSI(tc.in, tc.width, tc.opts)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
