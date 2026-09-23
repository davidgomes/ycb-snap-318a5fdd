package termenv

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

func TestPreserveResetsAndTruncate(t *testing.T) {
	bold := "\x1b[1m"
	reset := "\x1b[0m"

	s := String("a\x1b[0mb").Bold().PreserveResets()
	want := bold + "a" + reset + bold + "b" + reset
	if got := s.String(); got != want {
		t.Fatalf("Style.String preserve:\n got %q\nwant %q", got, want)
	}

	o := NewOutput(&bytes.Buffer{}, WithProfile(TrueColor), WithPreserveResets(true))
	got := o.String("a\x1b[0mb").Bold().String()
	if got != want {
		t.Fatalf("Output.String preserve:\n got %q\nwant %q", got, want)
	}

	plain := NewOutput(&bytes.Buffer{}, WithProfile(TrueColor))
	got = plain.String("a\x1b[0mb").Bold().String()
	want = bold + "a" + reset + "b" + reset
	if got != want {
		t.Fatalf("default does not preserve:\n got %q\nwant %q", got, want)
	}

	trunc := String("Hello World").Bold().Truncate(5, TruncateOptions{Tail: "…"})
	want = bold + "Hell…" + reset
	if trunc != want {
		t.Fatalf("Style.Truncate:\n got %q\nwant %q", trunc, want)
	}

	asciiStyle := Ascii.String("\x1b[1mHello").Truncate(4, TruncateOptions{Tail: "!!"})
	if asciiStyle != "Hell" {
		t.Fatalf("Ascii Style.Truncate = %q", asciiStyle)
	}

	asciiOut := NewOutput(&bytes.Buffer{}, WithProfile(Ascii))
	got = asciiOut.Truncate("\x1b[1mHello", 4, TruncateOptions{Tail: "!"})
	if got != "Hel!" {
		t.Fatalf("Ascii Output.Truncate = %q", got)
	}
	if strings.Contains(got, "\x1b") || strings.Contains(asciiStyle, "\x1b") {
		t.Fatal("Ascii truncation emitted ANSI")
	}

	forced := asciiOut.Truncate("Hello", 4, TruncateOptions{Tail: "!", PreserveResets: true})
	if forced != "Hel!" {
		t.Fatalf("Ascii ignores preserve flag visually: %q", forced)
	}

	colored := NewOutput(&bytes.Buffer{}, WithProfile(TrueColor))
	got = colored.Truncate(bold+"abcdef", 4, TruncateOptions{Tail: "..", PreserveResets: true})
	want = bold + "ab.." + reset
	if got != want {
		t.Fatalf("Output.Truncate opts:\n got %q\nwant %q", got, want)
	}

	got = o.Truncate("a"+reset+"bc", 3, TruncateOptions{Tail: "!"})
	want = "a" + reset + "!"
	// output default preserve is on, so the reset run is followed by re-open of nothing
	// (no SGR before the reset). Tail is plain.
	if ANSIWidth(got) > 3 {
		t.Fatalf("width %d for %q", ANSIWidth(got), got)
	}
}

func TestTruncateTemplateFuncs(t *testing.T) {
	o := NewOutput(&bytes.Buffer{}, WithProfile(ANSI), WithPreserveResets(true))
	tpl, err := template.New("t").Funcs(o.TemplateFuncs()).Parse(
		`{{ Truncate 4 "…" "Hello" }}|{{ truncate 4 "Hello" }}|{{ Bold "a\x1b[0mb" }}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, nil); err != nil {
		t.Fatal(err)
	}
	bold := "\x1b[1m"
	reset := "\x1b[0m"
	want := "Hel…|Hell|" + bold + "a" + reset + bold + "b" + reset
	if buf.String() != want {
		t.Fatalf("template:\n got %q\nwant %q", buf.String(), want)
	}

	ascii := NewOutput(&bytes.Buffer{}, WithProfile(Ascii), WithPreserveResets(true))
	tpl, err = template.New("a").Funcs(ascii.TemplateFuncs()).Parse(`{{ Truncate 4 "!" "\x1b[1mHello" }}|{{ Bold "x" }}`)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := tpl.Execute(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "Hel!|x" {
		t.Fatalf("ascii template: %q", buf.String())
	}
}
