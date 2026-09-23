package ansi

import "testing"

func TestTruncateANSI(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		opts TruncateOptions
		want string
	}{
		{"hello", 10, TruncateOptions{}, "hello"},
		{"\x1b[1mhello\x1b[0m", 3, TruncateOptions{Tail: "…"}, "\x1b[1mhe…\x1b[0m"},
		{"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", 2, TruncateOptions{}, "\x1b]8;;http://x\x1b\\li\x1b]8;;\x1b\\"},
		{"日本語", 3, TruncateOptions{}, "日"},
		{"\x1b[1ma\x1b[mb\x1b[0m", 5, TruncateOptions{PreserveResets: true}, "\x1b[1ma\x1b[m\x1b[1mb\x1b[0m"},
	}
	for _, c := range cases {
		if got := TruncateANSI(c.in, c.w, c.opts); got != c.want {
			t.Errorf("TruncateANSI(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
	if ANSIWidth("\x1b[31ma\u200bb\x1b[0m") != 2 || StripANSI("\x1b[31mx\x1b[0m") != "x" {
		t.Error("width/strip")
	}
}
