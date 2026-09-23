package ansi

import (
	"strings"
	"testing"
)

func TestTruncateANSI(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		opts  TruncateOptions
		want  string
	}{
		{"fits", "hello", 5, TruncateOptions{Tail: "…"}, "hello"},
		{"fits unchanged", "\x1b[31mhello", 10, TruncateOptions{}, "\x1b[31mhello"},
		{"plain", "hello world", 5, TruncateOptions{}, "hello"},
		{"tail counts toward width", "hello world", 6, TruncateOptions{Tail: "…"}, "hello…"},
		{"multi-cell tail", "hello world", 8, TruncateOptions{Tail: "..."}, "hello..."},
		{"zero width", "\x1b[31mhello\x1b[0m", 0, TruncateOptions{Tail: "…"}, ""},
		{"negative width", "hello", -1, TruncateOptions{}, ""},
		{"tail wider than width", "hello world", 2, TruncateOptions{Tail: "..."}, ".."},
		{
			"tail inherits style",
			"\x1b[31mHello World\x1b[0m", 8, TruncateOptions{Tail: "..."},
			"\x1b[31mHello...\x1b[0m",
		},
		{
			"never splits sequences",
			"ab\x1b[38;2;255;0;0mcd\x1b[0m", 3, TruncateOptions{},
			"ab\x1b[38;2;255;0;0mc\x1b[0m",
		},
		{
			"sequences at the cut are kept",
			"\x1b[1mhello\x1b[0m world", 6, TruncateOptions{Tail: "…"},
			"\x1b[1mhello\x1b[0m…",
		},
		{
			"reset that sets attributes",
			"\x1b[0;32mhello world", 5, TruncateOptions{},
			"\x1b[0;32mhello\x1b[0m",
		},
		{
			"trailing reset makes final reset unnecessary",
			"\x1b[1mab\x1b[0mcd", 3, TruncateOptions{},
			"\x1b[1mab\x1b[0mc",
		},
		{"other sequences are zero-width", "\x1b[2Khello", 3, TruncateOptions{}, "\x1b[2Khel"},
		{"unterminated sequence past the cut", "abc\x1b[31", 2, TruncateOptions{}, "ab"},
		{
			"styled tail",
			"hello world", 6, TruncateOptions{Tail: "\x1b[2m…\x1b[0m"},
			"hello\x1b[2m…\x1b[0m",
		},
		{
			"styled tail inside active style",
			"\x1b[1mhello world", 6, TruncateOptions{Tail: "\x1b[2m…"},
			"\x1b[1mhello\x1b[2m…\x1b[0m",
		},
		{
			"closes hyperlink",
			"\x1b]8;;https://example.com\x1b\\link text\x1b]8;;\x1b\\", 4, TruncateOptions{},
			"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		},
		{
			"closes hyperlink and style",
			"\x1b]8;id=1;https://example.com\a\x1b[4mlink text\x1b[0m\x1b]8;;\a", 5, TruncateOptions{Tail: "…"},
			"\x1b]8;id=1;https://example.com\a\x1b[4mlink…\x1b[0m\x1b]8;;\x1b\\",
		},
		{
			"closed hyperlink stays closed",
			"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ text", 6, TruncateOptions{},
			"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ t",
		},
		{"wide runes", "中文字", 5, TruncateOptions{}, "中文"},
		{"wide rune with tail", "中文字", 4, TruncateOptions{Tail: "…"}, "中…"},
		{"wide rune does not fit", "a中", 2, TruncateOptions{}, "a"},
		{"zero-width space", "a\u200bbc", 2, TruncateOptions{}, "a\u200bb"},
		{"combining mark", "e\u0301e\u0301", 1, TruncateOptions{}, "e\u0301"},
		{"flag emoji", "🇩🇪🇫🇷", 3, TruncateOptions{}, "🇩🇪"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateANSI(tt.in, tt.width, tt.opts)
			if got != tt.want {
				t.Fatalf("TruncateANSI(%q, %d, %+v)\n got: %q\nwant: %q", tt.in, tt.width, tt.opts, got, tt.want)
			}
			if tt.width > 0 && ANSIWidth(got) > tt.width {
				t.Errorf("result width %d exceeds %d", ANSIWidth(got), tt.width)
			}
		})
	}
}

func TestTruncateANSIPreserveResets(t *testing.T) {
	preserve := TruncateOptions{PreserveResets: true}
	tests := []struct {
		name  string
		in    string
		width int
		opts  TruncateOptions
		want  string
	}{
		{
			"re-opens after inner reset",
			"\x1b[31mfoo \x1b[1mbar\x1b[0m baz\x1b[0m", 20, preserve,
			"\x1b[31mfoo \x1b[1mbar\x1b[0m\x1b[31m baz\x1b[0m",
		},
		{
			"re-opens once per run of resets",
			"\x1b[31ma\x1b[1m\x1b[4mb\x1b[0m\x1b[0mc\x1b[0m", 20, preserve,
			"\x1b[31ma\x1b[1m\x1b[4mb\x1b[0m\x1b[0m\x1b[31mc\x1b[0m",
		},
		{
			"treats any zero parameter as reset",
			"\x1b[31ma\x1b[1;0mb\x1b[mc\x1b[0m", 20, preserve,
			"\x1b[31ma\x1b[1;0m\x1b[31mb\x1b[m\x1b[31mc\x1b[0m",
		},
		{
			"keeps attributes set by the reset",
			"\x1b[31ma \x1b[0;32mb\x1b[0m c\x1b[0m", 20, preserve,
			"\x1b[31ma \x1b[0;32m\x1b[31m\x1b[32mb\x1b[0m\x1b[31m c\x1b[0m",
		},
		{
			"extended black is not a reset",
			"\x1b[31ma\x1b[48;2;0;0;0mb\x1b[0mc\x1b[0m", 20, preserve,
			"\x1b[31ma\x1b[48;2;0;0;0mb\x1b[0m\x1b[31mc\x1b[0m",
		},
		{
			"is idempotent",
			"\x1b[31mfoo \x1b[1mbar\x1b[0m\x1b[31m baz\x1b[0m", 20, preserve,
			"\x1b[31mfoo \x1b[1mbar\x1b[0m\x1b[31m baz\x1b[0m",
		},
		{
			"truncates and re-opens",
			"\x1b[31mfoo \x1b[1mbar\x1b[0m baz\x1b[0m", 9, TruncateOptions{Tail: "…", PreserveResets: true},
			"\x1b[31mfoo \x1b[1mbar\x1b[0m\x1b[31m …\x1b[0m",
		},
		{
			"tail after reset inherits enclosing style",
			"\x1b[31mfoo\x1b[0m bar\x1b[0m", 4, TruncateOptions{Tail: "…", PreserveResets: true},
			"\x1b[31mfoo\x1b[0m\x1b[31m…\x1b[0m",
		},
		{
			"no re-open without following text",
			"\x1b[31mfoo\x1b[0m bar\x1b[0m", 3, preserve,
			"\x1b[31mfoo\x1b[0m",
		},
		{
			"closes re-opened style",
			"\x1b[31mfoo\x1b[0mbar", 10, preserve,
			"\x1b[31mfoo\x1b[0m\x1b[31mbar\x1b[0m",
		},
		{
			"no enclosing style when text comes first",
			"a \x1b[1mb\x1b[0m c", 20, preserve,
			"a \x1b[1mb\x1b[0m c",
		},
		{
			"resets before the enclosing style are left alone",
			"\x1b[0m\x1b[31mfoo\x1b[0m", 10, preserve,
			"\x1b[0m\x1b[31mfoo\x1b[0m",
		},
		{
			"enclosing style inside a hyperlink",
			"\x1b]8;;https://example.com\x1b\\\x1b[31ma\x1b[1mb\x1b[0mc\x1b[0m\x1b]8;;\x1b\\", 20, preserve,
			"\x1b]8;;https://example.com\x1b\\\x1b[31ma\x1b[1mb\x1b[0m\x1b[31mc\x1b[0m\x1b]8;;\x1b\\",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateANSI(tt.in, tt.width, tt.opts)
			if got != tt.want {
				t.Fatalf("TruncateANSI(%q, %d, %+v)\n got: %q\nwant: %q", tt.in, tt.width, tt.opts, got, tt.want)
			}
		})
	}
}

func TestTruncateANSIWidths(t *testing.T) {
	inputs := []string{
		"\x1b[1;31mHello, \x1b[4m世界\x1b[0m and \x1b]8;;https://example.com\x1b\\links\x1b]8;;\x1b\\!",
		"a\u200bb\u200bc 🇩🇪 e\u0301 中文",
	}
	for _, in := range inputs {
		for width := 0; width <= ANSIWidth(in)+1; width++ {
			for _, tail := range []string{"", "…", "..."} {
				got := TruncateANSI(in, width, TruncateOptions{Tail: tail})
				if w := ANSIWidth(got); w > width {
					t.Errorf("TruncateANSI(%q, %d, %q) = %q has width %d", in, width, tail, got, w)
				}
				if width > 0 && width < ANSIWidth(in) && ANSIWidth(tail) <= width {
					kept := strings.TrimSuffix(StripANSI(got), tail)
					if !strings.HasSuffix(StripANSI(got), tail) || !strings.HasPrefix(StripANSI(in), kept) {
						t.Errorf("TruncateANSI(%q, %d, %q) = %q is not a prefix plus tail", in, width, tail, got)
					}
				}
				for _, tok := range Tokenize(got) {
					if tok.Text == "" && !strings.Contains(in, tok.Raw) && tok.Raw != sgrReset && tok.Raw != hyperlinkClose {
						t.Errorf("TruncateANSI(%q, %d, %q) = %q contains split sequence %q", in, width, tail, got, tok.Raw)
					}
				}
			}
		}
	}
}
