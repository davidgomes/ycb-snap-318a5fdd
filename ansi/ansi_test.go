package ansi

import (
	"reflect"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Token
	}{
		{"empty", "", nil},
		{"plain", "hello", []Token{{TokenText, "hello", "hello"}}},
		{
			"styled",
			"a\x1b[1;31mbc\x1b[0md",
			[]Token{
				{TokenText, "a", "a"},
				{TokenSGR, "\x1b[1;31m", ""},
				{TokenText, "bc", "bc"},
				{TokenReset, "\x1b[0m", ""},
				{TokenText, "d", "d"},
			},
		},
		{
			"hyperlink st",
			"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
			[]Token{
				{TokenHyperlinkOpen, "\x1b]8;;https://example.com\x1b\\", ""},
				{TokenText, "link", "link"},
				{TokenHyperlinkClose, "\x1b]8;;\x1b\\", ""},
			},
		},
		{
			"hyperlink bel with params",
			"\x1b]8;id=1;https://example.com\alink\x1b]8;;\a",
			[]Token{
				{TokenHyperlinkOpen, "\x1b]8;id=1;https://example.com\a", ""},
				{TokenText, "link", "link"},
				{TokenHyperlinkClose, "\x1b]8;;\a", ""},
			},
		},
		{
			"other sequences are zero-width text",
			"\x1b[2J\x1b[?25l\x1b]0;title\a\x1b7\x1b(Bx",
			[]Token{
				{TokenText, "\x1b[2J", ""},
				{TokenText, "\x1b[?25l", ""},
				{TokenText, "\x1b]0;title\a", ""},
				{TokenText, "\x1b7", ""},
				{TokenText, "\x1b(B", ""},
				{TokenText, "x", "x"},
			},
		},
		{
			"private sgr-like sequence",
			"\x1b[>4;2m",
			[]Token{{TokenText, "\x1b[>4;2m", ""}},
		},
		{
			"unterminated csi",
			"ab\x1b[31",
			[]Token{{TokenText, "ab", "ab"}, {TokenText, "\x1b[31", ""}},
		},
		{
			"unterminated osc",
			"ab\x1b]8;;https://example.com",
			[]Token{{TokenText, "ab", "ab"}, {TokenText, "\x1b]8;;https://example.com", ""}},
		},
		{
			"osc aborted by escape",
			"\x1b]0;title\x1b[1mx",
			[]Token{
				{TokenText, "\x1b]0;title", ""},
				{TokenSGR, "\x1b[1m", ""},
				{TokenText, "x", "x"},
			},
		},
		{
			"csi aborted by invalid byte",
			"\x1b[12\nx",
			[]Token{{TokenText, "\x1b[12", ""}, {TokenText, "\nx", "\nx"}},
		},
		{"lone escape", "a\x1b", []Token{{TokenText, "a", "a"}, {TokenText, "\x1b", ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Tokenize(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
			var raw strings.Builder
			for _, tok := range got {
				raw.WriteString(tok.Raw)
			}
			if raw.String() != tt.in {
				t.Errorf("concatenated Raw = %q, want %q", raw.String(), tt.in)
			}
		})
	}
}

func TestTokenizeResets(t *testing.T) {
	tests := []struct {
		seq  string
		want TokenType
	}{
		{"\x1b[m", TokenReset},
		{"\x1b[0m", TokenReset},
		{"\x1b[00m", TokenReset},
		{"\x1b[1;0m", TokenReset},
		{"\x1b[0;32m", TokenReset},
		{"\x1b[;1m", TokenReset},
		{"\x1b[1;38;5;0;0m", TokenReset},
		{"\x1b[1m", TokenSGR},
		{"\x1b[10m", TokenSGR},
		{"\x1b[38;5;0m", TokenSGR},
		{"\x1b[48;2;0;0;0m", TokenSGR},
		{"\x1b[58;2;0;10;0m", TokenSGR},
		{"\x1b[38:2::0:0:0m", TokenSGR},
		{"\x1b[4:0m", TokenSGR},
	}
	for _, tt := range tests {
		toks := Tokenize(tt.seq)
		if len(toks) != 1 || toks[0].Type != tt.want {
			t.Errorf("Tokenize(%q) = %q, want a single %v token", tt.seq, toks, tt.want)
		}
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"\x1b[1;31mred\x1b[0m text", "red text"},
		{"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"\x1b[2Jcleared\x1b[?25l", "cleared"},
		{"中\x1b[1m文\x1b[0m", "中文"},
	}
	for _, tt := range tests {
		if got := StripANSI(tt.in); got != tt.want {
			t.Errorf("StripANSI(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestANSIWidth(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 5},
		{"\x1b[1;31mhello\x1b[0m", 5},
		{"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", 4},
		{"中文", 4},
		{"a\u200bb", 2},
		{"e\u0301", 1},
		{"\x1b[38;2;255;0;0m👍\x1b[0m", 2},
	}
	for _, tt := range tests {
		if got := ANSIWidth(tt.in); got != tt.want {
			t.Errorf("ANSIWidth(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestHasANSI(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"plain text", false},
		{"\x1b[1mbold", true},
		{"\x1b]8;;x\x1b\\", true},
		{"\x1b", true},
	}
	for _, tt := range tests {
		if got := HasANSI(tt.in); got != tt.want {
			t.Errorf("HasANSI(%q) = %t, want %t", tt.in, got, tt.want)
		}
	}
}
