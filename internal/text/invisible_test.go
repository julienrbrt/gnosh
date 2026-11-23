package text

import (
	"strings"
	"testing"
)

// CleanInvisible takes out what a terminal draws as nothing, keeps what
// scripts and emoji need, and says when what it took out could hide text.
func TestCleanInvisible(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
		removed        int
		hiding         bool
	}{
		{"plain ASCII is passed back as it is", "run the tests", "run the tests", 0, false},
		{"a zero-width space in a word", "rm\u200B -rf", "rm -rf", 1, false},
		{"a byte order mark at the start", "\uFEFFhello", "hello", 1, false},
		{"a soft hyphen", "hy\u00ADphen", "hyphen", 1, false},
		{"the direction marks", "a\u200Eb\u200Fc\u061C", "abc", 3, false},
		{"tag characters spell hidden text", "hi\U000E0069\U000E0067\U000E006E", "hi", 3, true},
		{"one tag character still hides", "hi\U000E0041", "hi", 1, true},
		{"a right-to-left override", "echo \u202Etxt.exe", "echo txt.exe", 1, true},
		{"an isolate", "x\u2066y\u2069", "xy", 2, true},
		{"three zero-width characters in a row spell bytes", "a\u200B\u200C\u200Bb", "ab", 3, true},
		{"two in a row do not", "a\u200B\u2060b", "ab", 2, false},
		{"joiners between ASCII letters", "a\u200Db\u200Cc", "abc", 2, false},
		{"a Persian non-joiner stays", "\u0645\u06CC\u200C\u062E", "\u0645\u06CC\u200C\u062E", 0, false},
		{"a Devanagari joiner stays", "\u0915\u094D\u200D\u0937", "\u0915\u094D\u200D\u0937", 0, false},
		{"an emoji family stays", "\U0001F468\u200D\U0001F469\u200D\U0001F467", "\U0001F468\u200D\U0001F469\u200D\U0001F467", 0, false},
		{"a heart on fire: selector, then joiner", "\u2764\uFE0F\u200D\U0001F525", "\u2764\uFE0F\u200D\U0001F525", 0, false},
		{"two joiners in a row lose the second", "\u0645\u200C\u200C\u062E", "\u0645\u200C\u062E", 1, false},
		{"a joiner at the end", "\u0645\u200D", "\u0645", 1, false},
		{"a keycap", "#\uFE0F\u20E3", "#\uFE0F\u20E3", 0, false},
		{"a selector after an ASCII letter", "a\uFE0Fb", "ab", 1, false},
		{"an emoji with bytes hidden in selectors", "\U0001F600\uFE01\uFE02\uFE03\uFE04", "\U0001F600\uFE01", 3, true},
		{"the same with the supplement's selectors", "ok \U0001F600\U000E0101\U000E0102", "ok \U0001F600", 2, false},
		{"a Han variant stays", "\u845B\U000E0100", "\u845B\U000E0100", 0, false},
		{"the flag of England stays", "\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F", "\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F", 0, false},
		{"a black flag with tags and no end", "\U0001F3F4\U000E0067\U000E0062 x", "\U0001F3F4 x", 2, true},
		{"a pirate flag stays", "\U0001F3F4\u200D\u2620\uFE0F", "\U0001F3F4\u200D\u2620\uFE0F", 0, false},
		{"non-ASCII text with nothing to take out", "caf\u00E9 \u65E5\u672C", "caf\u00E9 \u65E5\u672C", 0, false},
		{"broken bytes around a zero-width space", "\xe2\u200B\x80\x8b", "\uFFFD\uFFFD", 1, false},
		{"a black flag hiding a message", "\U0001F3F4\U000E0069\U000E0067\U000E006E\U000E006F\U000E0072\U000E0065\U000E0061\U000E006C\U000E006C\U000E007F", "\U0001F3F4", 10, true},
		{"the flag of Scotland stays", "\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", "\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", 0, false},
		{"a selector after a letter", "h\uFE01\u00E9\uFE02llo", "h\u00E9llo", 2, false},
		{"a heart keeps its selector", "\u2764\uFE0F", "\u2764\uFE0F", 0, false},
		{"the information sign keeps its selector", "\u2139\uFE0F", "\u2139\uFE0F", 0, false},
		{"a Hangul filler", "a\u3164b", "ab", 1, false},
		{"an old format control", "a\u206Bb", "ab", 1, false},
		{"an unassigned tag", "a\U000E0080b", "ab", 1, false},
		{"a no-break space before a joiner", "\u0645\u00A0\u200C\u062E", "\u0645\u00A0\u062E", 1, false},
	} {
		got, found := CleanInvisible(c.in)
		if got != c.want || found.Removed != c.removed || found.Hiding != c.hiding {
			t.Errorf("%s: CleanInvisible(%+q) = %+q, %+v; want %+q, %d removed, hiding %v", c.name, c.in, got, found, c.want, c.removed, c.hiding)
		}
		if again, f := CleanInvisible(got); again != got || f.Removed != 0 {
			t.Errorf("%s: cleaning %+q again took out %d more", c.name, got, f.Removed)
		}
	}
	// A long prompt with nothing to take out comes back as the same
	// string, not a copy.
	long := strings.Repeat("plain words ", 1000)
	if got, _ := CleanInvisible(long); got != long {
		t.Errorf("a plain prompt changed")
	}
}

func FuzzCleanInvisible(f *testing.F) {
	f.Add("a\u200Bb")
	f.Add("\U0001F3F4\U000E0067\U000E007F")
	f.Add("\xe2\u200B\x80\x8b") // the space taken out would join the bytes into another
	f.Fuzz(func(t *testing.T, s string) {
		got, found := CleanInvisible(s)
		if found.Removed == 0 && got != s {
			t.Fatalf("nothing taken out, yet %+q became %+q", s, got)
		}
		if again, f := CleanInvisible(got); again != got || f.Removed != 0 {
			t.Fatalf("%+q cleaned to %+q, which cleans again to %+q", s, got, again)
		}
	})
}

// CleanInvisibleAt says where places in the text land once it is cleaned:
// a paste's bounds move back by what was taken out before them.
func TestCleanInvisibleAt(t *testing.T) {
	s := "ab\u200Bcd\u200B\u200Bef"
	got, at, found := CleanInvisibleAt(s, []int{0, 2, 5, 7, 13, len(s)})
	if got != "abcdef" || found.Removed != 3 {
		t.Fatalf("CleanInvisibleAt = %+q, %+v", got, found)
	}
	want := []int{0, 2, 2, 4, 4, 6}
	for i := range want {
		if at[i] != want[i] {
			t.Errorf("offset %d: got %v, want %v", i, at, want)
			break
		}
	}
	if _, at, _ := CleanInvisibleAt("plain", []int{0, 3}); len(at) != 2 || at[1] != 3 {
		t.Errorf("a plain text's offsets moved: %v", at)
	}
	if _, at, _ := CleanInvisibleAt("\xe2\u200Bx", []int{0}); at != nil {
		t.Errorf("offsets into text that is not UTF-8 came back: %v", at)
	}
}

// What CleanInvisible takes out, ShowInvisible writes as its code point;
// what it keeps (an emoji's joiners, Persian's non-joiner, a flag's tags)
// stays as it is.
func TestShowInvisible(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"rm -rf build", "rm -rf build"},
		{"rm -rf /tmp/\u202etxt.exe", "rm -rf /tmp/<U+202E>txt.exe"},
		{"a\u200bb\u2066c\u2069", "a<U+200B>b<U+2066>c<U+2069>"},
		{"hi\U000E0069", "hi<U+E0069>"},
		{"\U0001F468\u200d\U0001F469\u200d\U0001F467", "\U0001F468\u200d\U0001F469\u200d\U0001F467"},
		{"می\u200cخواهم", "می\u200cخواهم"},
		{"\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F", "\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F"},
		{"ab\u200d", "ab<U+200D>"},
	} {
		if got := ShowInvisible(c.in); got != c.want {
			t.Errorf("ShowInvisible(%+q) = %+q, want %+q", c.in, got, c.want)
		}
		cleaned, _ := CleanInvisible(c.in)
		if strings.Contains(c.want, "<U+") == (cleaned == c.in) {
			t.Errorf("%+q: ShowInvisible and CleanInvisible disagree on whether there is anything to show", c.in)
		}
	}
}
