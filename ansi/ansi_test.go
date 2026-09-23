package ansi

import "testing"

func TestTokenizeResetAndSGR(t *testing.T) {
	in := "a\x1b[1;31mb\x1b[mc\x1b[0md\x1b[31;0me"
	toks := Tokenize(in)
	want := []TokenType{TokenText, TokenSGR, TokenText, TokenReset, TokenText, TokenReset, TokenText, TokenReset, TokenText}
	if len(toks) != len(want) {
		t.Fatalf("len=%d tokens=%#v", len(toks), toks)
	}
	for i, typ := range want {
		if toks[i].Type != typ {
			t.Fatalf("tok %d type %v want %v (%q)", i, toks[i].Type, typ, toks[i].Raw)
		}
	}
}

func TestTokenizeHyperlink(t *testing.T) {
	in := "\x1b]8;;https://example.com\x1b\\hi\x1b]8;;\x1b\\"
	toks := Tokenize(in)
	if len(toks) != 3 || toks[0].Type != TokenHyperlinkOpen || toks[1].Type != TokenText || toks[2].Type != TokenHyperlinkClose {
		t.Fatalf("%#v", toks)
	}
	if toks[1].Text != "hi" {
		t.Fatalf("text %q", toks[1].Text)
	}
}

func TestTruncateDoesNotSplitCSI(t *testing.T) {
	in := "\x1b[31mhello"
	got := TruncateANSI(in, 4, TruncateOptions{Tail: ".."})
	want := "\x1b[31mhe..\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTruncatePreserveResets(t *testing.T) {
	in := "\x1b[1mfoo\x1b[0mbar"
	got := TruncateANSI(in, 6, TruncateOptions{PreserveResets: true})
	want := "\x1b[1mfoo\x1b[0m\x1b[1mbar\x1b[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTruncateWidths(t *testing.T) {
	if w := ANSIWidth("你\u200b"); w != 2 {
		t.Fatalf("width %d", w)
	}
	got := TruncateANSI("你a", 2, TruncateOptions{Tail: "…"})
	if got != "…" {
		t.Fatalf("wide got %q", got)
	}
	got = TruncateANSI("ab你", 3, TruncateOptions{})
	if got != "ab" {
		t.Fatalf("cut before wide got %q", got)
	}
}

func TestStripAndHas(t *testing.T) {
	in := "\x1b[31mred\x1b[0m"
	if !HasANSI(in) || HasANSI("plain") {
		t.Fatal("HasANSI")
	}
	if StripANSI(in) != "red" {
		t.Fatalf("strip %q", StripANSI(in))
	}
}

func TestTruncateClosesHyperlink(t *testing.T) {
	in := "\x1b]8;;https://example.com\x1b\\hello"
	got := TruncateANSI(in, 4, TruncateOptions{Tail: "…"})
	want := "\x1b]8;;https://example.com\x1b\\hel…\x1b]8;;\x1b\\"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
