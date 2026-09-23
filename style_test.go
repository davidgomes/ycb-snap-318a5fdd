package termenv

import (
	"bytes"
	"testing"
	"text/template"
)

func TestStyleWidth(t *testing.T) {
	s := String("Hello World")
	if s.Width() != 11 {
		t.Errorf("Expected width of 11, got %d", s.Width())
	}

	s = s.Bold()
	if s.Width() != 11 {
		t.Errorf("Expected width of 11, got %d", s.Width())
	}

	s = s.Italic()
	if s.Width() != 11 {
		t.Errorf("Expected width of 11, got %d", s.Width())
	}

	s = s.Foreground(TrueColor.Color("#abcdef"))
	s = s.Background(TrueColor.Color("69"))
	if s.Width() != 11 {
		t.Errorf("Expected width of 11, got %d", s.Width())
	}
}

func TestPreserveResetsAndTruncate(t *testing.T) {
	s := String("a\x1b[0mb").Bold().PreserveResets()
	got := s.String()
	want := "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m"
	if got != want {
		t.Fatalf("styled %q want %q", got, want)
	}

	plain := Ascii.String("\x1b[31mhello").Truncate(4, TruncateOptions{Tail: "…"})
	if plain != "hell" {
		t.Fatalf("ascii style truncate %q", plain)
	}

	o := NewOutput(nil, WithProfile(Ascii))
	if got := o.Truncate("\x1b[31mhello", 4, TruncateOptions{Tail: ".."}); got != "he.." {
		t.Fatalf("ascii output truncate %q", got)
	}

	o = NewOutput(nil, WithProfile(ANSI), WithPreserveResets(true))
	st := o.String("a\x1b[0mb").Bold()
	if st.String() != want {
		t.Fatalf("output style %q", st.String())
	}
	cut := o.Truncate("\x1b[31mhello", 4, TruncateOptions{Tail: ".."})
	if cut != "\x1b[31mhe..\x1b[0m" {
		t.Fatalf("output truncate %q", cut)
	}

	tpl := template.Must(template.New("t").Funcs(o.TemplateFuncs()).Parse(`{{truncate 4 .}}`))
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, "\x1b[1mabcdef"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "\x1b[1mabcd\x1b[0m" {
		t.Fatalf("template truncate %q", buf.String())
	}

	bold := template.Must(template.New("b").Funcs(o.TemplateFuncs()).Parse(`{{Bold .}}`))
	buf.Reset()
	if err := bold.Execute(&buf, "a\x1b[0mb"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != want {
		t.Fatalf("template bold preserve %q", buf.String())
	}
}
