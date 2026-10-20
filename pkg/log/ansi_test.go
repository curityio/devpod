package log

import "testing"

func TestColorize(t *testing.T) {
	tests := []struct {
		style string
		want  string
	}{
		{style: "red+b", want: "\x1b[0;1;31mhi\x1b[0m"},
		{style: "blue", want: "\x1b[0;34mhi\x1b[0m"},
		{style: "cyan+h", want: "\x1b[0;96mhi\x1b[0m"},
		{style: "white+bh", want: "\x1b[0;1;97mhi\x1b[0m"},
		{style: "", want: "hi"},
		{style: "unknown", want: "hi"},
	}
	for _, tt := range tests {
		if got := colorize("hi", tt.style); got != tt.want {
			t.Errorf("colorize(%q) = %q, want %q", tt.style, got, tt.want)
		}
	}
}

func TestStripANSI(t *testing.T) {
	for _, style := range Colors {
		if got := stripANSI("a " + colorize("b", style) + " c"); got != "a b c" {
			t.Errorf("stripANSI(colorize(%q)) = %q", style, got)
		}
	}
	if got := stripANSI("\x1b[2K\x1b[1Gdone"); got != "done" {
		t.Errorf("stripANSI(cursor movement) = %q", got)
	}
}
