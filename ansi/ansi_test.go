package ansi

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	s := "a\x1b[1mb\x1b[0m\x1b]8;;http://x\x1b\\c\x1b]8;;\x07\x1b[2Jd"
	want := []Token{
		{TokenText, "a", "a"},
		{TokenSGR, "\x1b[1m", ""},
		{TokenText, "b", "b"},
		{TokenReset, "\x1b[0m", ""},
		{TokenHyperlinkOpen, "\x1b]8;;http://x\x1b\\", ""},
		{TokenText, "c", "c"},
		{TokenHyperlinkClose, "\x1b]8;;\x07", ""},
		{TokenText, "\x1b[2J", ""},
		{TokenText, "d", "d"},
	}
	if got := Tokenize(s); !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize() =\n%q\nwant\n%q", got, want)
	}
}

func TestResetDetection(t *testing.T) {
	for raw, want := range map[string]TokenType{
		"\x1b[m":       TokenReset,
		"\x1b[0m":      TokenReset,
		"\x1b[00m":     TokenReset,
		"\x1b[1;0m":    TokenReset,
		"\x1b[0;31m":   TokenReset,
		"\x1b[1m":      TokenSGR,
		"\x1b[38;5;1m": TokenSGR,
	} {
		toks := Tokenize(raw)
		if len(toks) != 1 || toks[0].Type != want {
			t.Errorf("Tokenize(%q) = %v, want %v", raw, toks, want)
		}
	}
}

func TestWidthAndStrip(t *testing.T) {
	s := "\x1b[31m日本\u200bx\x1b[0m"
	if got := StripANSI(s); got != "日本\u200bx" {
		t.Errorf("StripANSI = %q", got)
	}
	if got := ANSIWidth(s); got != 5 {
		t.Errorf("ANSIWidth = %d, want 5", got)
	}
	if !HasANSI(s) || HasANSI("plain") {
		t.Error("HasANSI mismatch")
	}
}

func TestTruncateANSI(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		opts  TruncateOptions
		want  string
	}{
		{"fits", "\x1b[1mabc\x1b[0m", 3, TruncateOptions{Tail: "…"}, "\x1b[1mabc\x1b[0m"},
		{"plain", "abcdef", 4, TruncateOptions{Tail: "…"}, "abc…"},
		{"styled tail", "\x1b[1mabcdef\x1b[0m", 4, TruncateOptions{Tail: "…"}, "\x1b[1mabc…\x1b[0m"},
		{"no split csi", "ab\x1b[31mcdef", 2, TruncateOptions{}, "ab\x1b[31m\x1b[0m"},
		{"reset clears active", "\x1b[1ma\x1b[0mbcdef", 3, TruncateOptions{}, "\x1b[1ma\x1b[0mbc"},
		{"wide", "日本語", 5, TruncateOptions{}, "日本"},
		{"wide tail", "日本語", 4, TruncateOptions{Tail: "."}, "日."},
		{"zero width", "a\u200bbcd", 2, TruncateOptions{}, "a\u200bb"},
		{"hyperlink", "\x1b]8;;http://x\x1b\\link text\x1b]8;;\x1b\\", 4, TruncateOptions{}, "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\"},
		{"tail too wide", "abcdef", 2, TruncateOptions{Tail: "..."}, ".."},
		{"zero", "abc", 0, TruncateOptions{}, ""},
		{
			"preserve resets", "\x1b[1ma\x1b[31mb\x1b[0mcdef", 4,
			TruncateOptions{PreserveResets: true, Tail: "…"},
			"\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc…\x1b[0m",
		},
		{
			"preserve resets fits", "\x1b[1ma\x1b[0m\x1b[m b", 10,
			TruncateOptions{PreserveResets: true},
			"\x1b[1ma\x1b[0m\x1b[m\x1b[1m b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TruncateANSI(tt.in, tt.width, tt.opts); got != tt.want {
				t.Errorf("TruncateANSI(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}
