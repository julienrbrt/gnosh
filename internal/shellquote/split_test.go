package shellquote

import (
	"slices"
	"strings"
	"testing"
)

// Split reads words as the shell does, and refuses what only a shell can
// carry out.
func TestSplit(t *testing.T) {
	for _, c := range []struct {
		line string
		want []string
	}{
		{"gnokey query auth/accounts/g1abc", []string{"gnokey", "query", "auth/accounts/g1abc"}},
		{"  a   b\tc  ", []string{"a", "b", "c"}},
		{`gnokey maketx call -args "hello world" -args 'it''s' k`, []string{"gnokey", "maketx", "call", "-args", "hello world", "-args", "its", "k"}},
		{`a "x\"y\\z" b\ c 'q"q'`, []string{"a", `x"y\z`, "b c", `q"q`}},
		{`a "" ''`, []string{"a", "", ""}},
		{`a "$` + "`" + `"`, nil},
		{"", nil},
	} {
		got, err := Split(c.line)
		if c.want == nil && strings.ContainsAny(c.line, "$`") {
			if err == nil {
				t.Errorf("Split(%q) = %q, want a refusal", c.line, got)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("Split(%q) = %q, %v; want %q", c.line, got, err, c.want)
		}
	}
	for _, line := range []string{
		"gnokey list | cat", "a && b", "a; b", "a > f", "a < f", "(a)", "a $X", "a `b`", "a \"$(b)\"",
		"a\nb", "a 'b", `a "b`, `a \`, "a & ",
	} {
		if got, err := Split(line); err == nil {
			t.Errorf("Split(%q) = %q, want a refusal", line, got)
		}
	}
	// What Words writes, Split reads back.
	for _, words := range [][]string{{"a b", "it's", `"q"`, "x$y", "", "-flag=1"}} {
		if got, err := Split(Words(words)); err != nil || !slices.Equal(got, words) {
			t.Errorf("Split(Words(%q)) = %q, %v", words, got, err)
		}
	}
}
