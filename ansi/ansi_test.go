package ansi

import (
	"reflect"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	s := "a\x1b[31mb\x1b[0m\x1b]8;;http://x\x1b\\c\x1b]8;;\x07\x1b[2Jd\x1b[m"
	want := []Token{
		{TokenText, "a", "a"},
		{TokenSGR, "\x1b[31m", ""},
		{TokenText, "b", "b"},
		{TokenReset, "\x1b[0m", ""},
		{TokenHyperlinkOpen, "\x1b]8;;http://x\x1b\\", ""},
		{TokenText, "c", "c"},
		{TokenHyperlinkClose, "\x1b]8;;\x07", ""},
		{TokenText, "\x1b[2J", ""},
		{TokenText, "d", "d"},
		{TokenReset, "\x1b[m", ""},
	}
	got := Tokenize(s)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize(%q)\n got: %q\nwant: %q", s, got, want)
	}

	var raw strings.Builder
	for _, tok := range got {
		raw.WriteString(tok.Raw)
	}
	if raw.String() != s {
		t.Fatalf("raw round-trip mismatch: %q", raw.String())
	}
}

func TestResetClassification(t *testing.T) {
	tests := map[string]TokenType{
		"\x1b[m":       TokenReset,
		"\x1b[0m":      TokenReset,
		"\x1b[00m":     TokenReset,
		"\x1b[1;0m":    TokenReset,
		"\x1b[0;31m":   TokenReset,
		"\x1b[1m":      TokenSGR,
		"\x1b[38;5;1m": TokenSGR,
		"\x1b[10m":     TokenSGR,
	}
	for s, want := range tests {
		toks := Tokenize(s)
		if len(toks) != 1 || toks[0].Type != want {
			t.Errorf("Tokenize(%q) = %v, want single %v", s, toks, want)
		}
	}
}

func TestTokenizeUnterminated(t *testing.T) {
	for _, s := range []string{"ab\x1b[31", "ab\x1b]8;;http://x", "ab\x1b"} {
		toks := Tokenize(s)
		if len(toks) != 2 || toks[1].Raw != s[2:] {
			t.Errorf("Tokenize(%q) = %q", s, toks)
		}
	}
}

func TestStripWidthHas(t *testing.T) {
	s := "\x1b[1m漢字\x1b[0m a\u200b\x1b]8;;u\x1b\\l\x1b]8;;\x1b\\"
	if got := StripANSI(s); got != "漢字 a\u200bl" {
		t.Errorf("StripANSI = %q", got)
	}
	if got := ANSIWidth(s); got != 7 {
		t.Errorf("ANSIWidth = %d, want 7", got)
	}
	if !HasANSI(s) || HasANSI("plain") {
		t.Error("HasANSI mismatch")
	}
}

func TestTruncateANSI(t *testing.T) {
	const (
		red   = "\x1b[31m"
		bold  = "\x1b[1m"
		reset = "\x1b[0m"
		open  = "\x1b]8;;http://x\x1b\\"
		close = "\x1b]8;;\x1b\\"
	)
	tests := []struct {
		name  string
		in    string
		width int
		opts  TruncateOptions
		want  string
	}{
		{"fits", red + "abc" + reset, 3, TruncateOptions{}, red + "abc" + reset},
		{"plain", "abcdef", 3, TruncateOptions{}, "abc"},
		{"plain tail", "abcdef", 4, TruncateOptions{Tail: "…"}, "abc…"},
		{"styled", red + "abcdef" + reset, 3, TruncateOptions{}, red + "abc" + reset},
		{"tail inherits style", red + "abcdef" + reset, 3, TruncateOptions{Tail: "."}, red + "ab." + reset},
		{"no reset when inactive", red + "ab" + reset + "cdef", 3, TruncateOptions{}, red + "ab" + reset + "c"},
		{"csi not split", "a\x1b[2Kbcdef", 3, TruncateOptions{}, "a\x1b[2Kbc"},
		{"no dangling sgr", "ab" + red + "cd", 2, TruncateOptions{}, "ab"},
		{"wide rune boundary", "a漢字", 2, TruncateOptions{}, "a"},
		{"wide runes", "漢字漢", 4, TruncateOptions{}, "漢字"},
		{"zero width space", "a\u200bbc", 2, TruncateOptions{}, "a\u200bb"},
		{"hyperlink closed", open + "linktext" + close, 4, TruncateOptions{}, open + "link" + close},
		{"hyperlink in style", red + open + "linktext" + close + reset, 4, TruncateOptions{}, red + open + "link" + close + reset},
		{"style in hyperlink", open + red + "linktext" + reset + close, 4, TruncateOptions{}, open + red + "link" + reset + close},
		{"hyperlink BEL", "\x1b]8;;u\x07linktext\x1b]8;;\x07", 2, TruncateOptions{}, "\x1b]8;;u\x07li\x1b]8;;\x07"},
		{"tail too wide", "abcdef", 2, TruncateOptions{Tail: "..."}, ""},
		{"zero width", "abc", 0, TruncateOptions{}, ""},
		{"tail only", red + "abcdef", 1, TruncateOptions{Tail: "…"}, red + "…" + reset},
		{
			"preserve resets reopens for tail",
			red + "ab" + bold + "cd" + reset + "efgh" + reset, 5, TruncateOptions{Tail: "…", PreserveResets: true},
			red + "ab" + bold + "cd" + reset + red + "…" + reset,
		},
		{
			"preserve resets without truncation",
			red + "ab" + reset + "cd", 10, TruncateOptions{PreserveResets: true},
			red + "ab" + reset + red + "cd" + reset,
		},
		{
			"preserve resets idempotent",
			red + "ab" + reset + red + "cd" + reset, 10, TruncateOptions{PreserveResets: true},
			red + "ab" + reset + red + "cd" + reset,
		},
		{
			"preserve resets reuses existing reopen",
			red + "ab" + reset + red + "cdef" + reset, 3, TruncateOptions{Tail: ".", PreserveResets: true},
			red + "ab" + reset + red + "." + reset,
		},
		{"trailing close kept", open + "ab" + close + "cd", 2, TruncateOptions{}, open + "ab" + close},
		{"mixed reset keeps style active", "\x1b[0;31mabcdef", 2, TruncateOptions{}, "\x1b[0;31mab" + reset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateANSI(tt.in, tt.width, tt.opts)
			if got != tt.want {
				t.Errorf("TruncateANSI(%q, %d, %+v)\n got: %q\nwant: %q", tt.in, tt.width, tt.opts, got, tt.want)
			}
			if tt.width >= 0 && ANSIWidth(got) > tt.width {
				t.Errorf("result width %d exceeds %d", ANSIWidth(got), tt.width)
			}
		})
	}
}
