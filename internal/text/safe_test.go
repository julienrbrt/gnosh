package text

import "testing"

// Safe keeps a reply's text and its styles, and takes out what would make
// the terminal do anything else.
func TestSafe(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain words\n\tand a tab", "plain words\n\tand a tab"},
		{"\x1b[1mbold\x1b[0m", "\x1b[1mbold\x1b[0m"},
		{"copy \x1b]52;c;cm0gLXJmIH4=\x07this", "copy this"},
		{"title\x1b]0;pwned\x07 here", "title here"},
		{"clear\x1b[2J\x1b[H screen", "clear screen"},
		{"cr\rover", "crover"},
		{"bell\x07 del\x7f", "bell del"},
		{"c1 \u009b2J csi", "c1 2J csi"},
		{"link \x1b]8;;http://203.0.113.9/\x1b\\here\x1b]8;;\x1b\\", "link here"},
		{"caf\u00e9 \u65e5", "caf\u00e9 \u65e5"},
		// Conceal hides words that are still copied; a style left open is
		// closed (the terminal review of 2026-11-19).
		{"run \x1b[8mrm -rf ~\x1b[0m now", "run rm -rf ~\x1b[0m now"},
		{"a \x1b[1;8mhidden", "a \x1b[1mhidden\x1b[0m"},
		{"gray \x1b[38;5;8mtext\x1b[m", "gray \x1b[38;5;8mtext\x1b[m"},
		{"left \x1b[31mred", "left \x1b[31mred\x1b[0m"},
	} {
		if got := Safe(c.in); got != c.want {
			t.Errorf("Safe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A sequence cut by the end of a piece is held until its rest comes: a
// style stays whole, and nothing of another kind gets through.
func TestSafeStream(t *testing.T) {
	var st SafeStream
	got := st.Write("a \x1b[3") + st.Write("1mred\x1b[0m b \x1b]52;c;") + st.Write("cm0=") + st.Write("\x07 c")
	if want := "a \x1b[31mred\x1b[0m b  c"; got != want {
		t.Errorf("the stream wrote %q, want %q", got, want)
	}
	if end := st.End(); end != "" {
		t.Errorf("a stream that closed its style ends with %q", end)
	}
	st.Write("open \x1b[1m")
	if end := st.End(); end != "\x1b[0m" {
		t.Errorf("a stream that left a style open ends with %q, want a reset", end)
	}
	st.Write("tail \x1b]0;")
	st.Reset()
	if got := st.Write("next"); got != "next" {
		t.Errorf("after Reset the stream wrote %q", got)
	}
}
