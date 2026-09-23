package termenv

import (
	"bytes"
	"io"
	"testing"
	"text/template"
)

func TestWrappers(t *testing.T) {
	s := "\x1b[1mHello\x1b[0m 世界"
	if !HasANSI(s) || HasANSI("plain") {
		t.Errorf("HasANSI misreports %q", s)
	}
	if got := StripANSI(s); got != "Hello 世界" {
		t.Errorf("StripANSI(%q) = %q", s, got)
	}
	if got := ANSIWidth(s); got != 10 {
		t.Errorf("ANSIWidth(%q) = %d, want 10", s, got)
	}
	if got, want := TruncateANSI(s, 7, TruncateOptions{Tail: "…"}), "\x1b[1mHello\x1b[0m …"; got != want {
		t.Errorf("TruncateANSI(%q) = %q, want %q", s, got, want)
	}
}

func TestStylePreserveResets(t *testing.T) {
	inner := ANSI.String("bold").Bold().String()
	s := ANSI.String("red " + inner + " red").Foreground(ANSIRed)

	if got, want := s.String(), "\x1b[31mred \x1b[1mbold\x1b[0m red\x1b[0m"; got != want {
		t.Errorf("without PreserveResets: got %q, want %q", got, want)
	}
	if got, want := s.PreserveResets().String(), "\x1b[31mred \x1b[1mbold\x1b[0m\x1b[31m red\x1b[0m"; got != want {
		t.Errorf("with PreserveResets: got %q, want %q", got, want)
	}

	if got, want := ANSI.String("a"+inner+"b").PreserveResets().String(), "a"+inner+"b"; got != want {
		t.Errorf("unstyled: got %q, want %q", got, want)
	}
	if got, want := Ascii.String("a"+inner+"b").Bold().PreserveResets().String(), "a"+inner+"b"; got != want {
		t.Errorf("ascii: got %q, want %q", got, want)
	}
}

func TestStyleTruncate(t *testing.T) {
	inner := ANSI.String("bold").Bold().String()
	tests := []struct {
		name  string
		style Style
		width int
		opts  TruncateOptions
		want  string
	}{
		{
			"tail inherits style",
			ANSI.String("Hello World").Bold(), 8, TruncateOptions{Tail: "..."},
			"\x1b[1mHello...\x1b[0m",
		},
		{
			"fits",
			ANSI.String("Hello").Bold(), 5, TruncateOptions{Tail: "..."},
			"\x1b[1mHello\x1b[0m",
		},
		{
			"unstyled",
			ANSI.String("Hello World"), 6, TruncateOptions{Tail: "…"},
			"Hello…",
		},
		{
			"style preserves resets",
			ANSI.String("red " + inner + " red").Foreground(ANSIRed).PreserveResets(), 11, TruncateOptions{Tail: "…"},
			"\x1b[31mred \x1b[1mbold\x1b[0m\x1b[31m r…\x1b[0m",
		},
		{
			"option preserves resets",
			ANSI.String("red " + inner + " red").Foreground(ANSIRed), 11, TruncateOptions{Tail: "…", PreserveResets: true},
			"\x1b[31mred \x1b[1mbold\x1b[0m\x1b[31m r…\x1b[0m",
		},
		{
			"without preserve resets",
			ANSI.String("red " + inner + " red").Foreground(ANSIRed), 11, TruncateOptions{Tail: "…"},
			"\x1b[31mred \x1b[1mbold\x1b[0m r…",
		},
		{
			"ascii returns plain text without tail",
			Ascii.String("Hello " + inner + " World").Bold(), 8, TruncateOptions{Tail: "..."},
			"Hello bo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.style.Truncate(tt.width, tt.opts); got != tt.want {
				t.Errorf("Truncate(%d, %+v)\n got: %q\nwant: %q", tt.width, tt.opts, got, tt.want)
			}
		})
	}

	s := ANSI.String("red " + inner + " red").Foreground(ANSIRed).PreserveResets()
	if got, want := s.Truncate(100, TruncateOptions{Tail: "…"}), s.String(); got != want {
		t.Errorf("untruncated: got %q, want %q", got, want)
	}
}

func TestOutputPreserveResets(t *testing.T) {
	o := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true))
	inner := o.String("bold").Bold().String()

	if got, want := inner, "\x1b[1mbold\x1b[0m"; got != want {
		t.Errorf("inner: got %q, want %q", got, want)
	}
	if got, want := o.String("red "+inner+" red").Foreground(ANSIRed).String(), "\x1b[31mred \x1b[1mbold\x1b[0m\x1b[31m red\x1b[0m"; got != want {
		t.Errorf("default: got %q, want %q", got, want)
	}

	plain := NewOutput(io.Discard, WithProfile(ANSI))
	if got, want := plain.String("red "+inner+" red").Foreground(ANSIRed).String(), "\x1b[31mred \x1b[1mbold\x1b[0m red\x1b[0m"; got != want {
		t.Errorf("no default: got %q, want %q", got, want)
	}
	if got, want := plain.String("red "+inner+" red").Foreground(ANSIRed).PreserveResets().String(), "\x1b[31mred \x1b[1mbold\x1b[0m\x1b[31m red\x1b[0m"; got != want {
		t.Errorf("explicit: got %q, want %q", got, want)
	}
}

func TestOutputTruncate(t *testing.T) {
	s := "\x1b[31mred \x1b[1mbold\x1b[0m red\x1b[0m"
	preserved := "\x1b[31mred \x1b[1mbold\x1b[0m\x1b[31m r…\x1b[0m"
	unpreserved := "\x1b[31mred \x1b[1mbold\x1b[0m r…"

	tests := []struct {
		name string
		o    *Output
		opts TruncateOptions
		want string
	}{
		{"plain", NewOutput(io.Discard, WithProfile(ANSI)), TruncateOptions{Tail: "…"}, unpreserved},
		{"output default", NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true)), TruncateOptions{Tail: "…"}, preserved},
		{"option", NewOutput(io.Discard, WithProfile(ANSI)), TruncateOptions{Tail: "…", PreserveResets: true}, preserved},
		{"both", NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true)), TruncateOptions{Tail: "…", PreserveResets: true}, preserved},
		{"ascii returns text with tail", NewOutput(io.Discard, WithProfile(Ascii), WithPreserveResets(true)), TruncateOptions{Tail: "\x1b[2m…\x1b[0m"}, "red bold r…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.o.Truncate(s, 11, tt.opts); got != tt.want {
				t.Errorf("Truncate(%q, 11, %+v)\n got: %q\nwant: %q", s, tt.opts, got, tt.want)
			}
		})
	}

	ascii := NewOutput(io.Discard, WithProfile(Ascii))
	if got, want := ascii.Truncate(s, 20, TruncateOptions{Tail: "…"}), "red bold red"; got != want {
		t.Errorf("ascii fits: got %q, want %q", got, want)
	}
}

func TestTemplateTruncate(t *testing.T) {
	render := func(t *testing.T, f template.FuncMap, tpl string) string {
		t.Helper()
		tmpl, err := template.New("tpl").Funcs(f).Parse(tpl)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, nil); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	tests := []struct {
		name string
		f    template.FuncMap
		tpl  string
		want string
	}{
		{"Truncate", TemplateFuncs(ANSI), `{{ Truncate 5 "…" "Hello World" }}`, "Hell…"},
		{"truncate", TemplateFuncs(ANSI), `{{ "Hello World" | truncate 5 }}`, "Hello"},
		{"styled", TemplateFuncs(ANSI), `{{ Truncate 5 "…" (Bold "Hello World") }}`, "\x1b[1mHell…\x1b[0m"},
		{"ascii Truncate", TemplateFuncs(Ascii), `{{ Truncate 5 "…" "Hello World" }}`, "Hell…"},
		{"ascii truncate", TemplateFuncs(Ascii), `{{ truncate 5 "\x1b[1mHello World\x1b[0m" }}`, "Hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := render(t, tt.f, tt.tpl); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	nested := `{{ Foreground "1" (printf "a %s b" (Bold "x")) }}`
	preserve := NewOutput(io.Discard, WithProfile(ANSI), WithPreserveResets(true)).TemplateFuncs()
	if got, want := render(t, preserve, nested), "\x1b[31ma \x1b[1mx\x1b[0m\x1b[31m b\x1b[0m"; got != want {
		t.Errorf("preserve default: got %q, want %q", got, want)
	}
	if got, want := render(t, TemplateFuncs(ANSI), nested), "\x1b[31ma \x1b[1mx\x1b[0m b\x1b[0m"; got != want {
		t.Errorf("no preserve: got %q, want %q", got, want)
	}

	truncNested := `{{ "\x1b[31ma \x1b[1mx\x1b[0m b c\x1b[0m" | Truncate 5 "…" }}`
	if got, want := render(t, preserve, truncNested), "\x1b[31ma \x1b[1mx\x1b[0m\x1b[31m …\x1b[0m"; got != want {
		t.Errorf("preserve Truncate: got %q, want %q", got, want)
	}
}
