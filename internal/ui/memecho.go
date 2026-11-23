package ui

import (
	"go/scanner"
	"go/token"
	"strconv"
	"strings"
	"unicode/utf8"
)

// withoutEchoes takes out of a Gno call's receipt, as the person is shown
// it, the quoted texts that the call's own code holds. mem's receipt names
// what a call made by its name and its title, and the title is words the
// code just above gave it: mem.Ask("x", "a long question") was shown with
// the question in the code, then again in "asked `x/a-long-question` "a
// long question…"" (Jae, 21 November 2026: "see clipboard... there's
// redundancy"). What mem decided stays: the name it chose, where the item
// went, what waits on it and what is next, with their titles, which the
// code does not hold. The model's copy of the receipt is not touched.
func withoutEchoes(receipt, code string) string {
	if !strings.Contains(receipt, `"`) {
		return receipt
	}
	lits := stringLiterals(code)
	if len(lits) == 0 {
		return receipt
	}
	var b strings.Builder
	for i := 0; i < len(receipt); {
		j := strings.IndexByte(receipt[i:], '"')
		if j < 0 {
			b.WriteString(receipt[i:])
			break
		}
		j += i
		q, err := strconv.QuotedPrefix(receipt[j:])
		if err != nil {
			b.WriteString(receipt[i : j+1])
			i = j + 1
			continue
		}
		v, err := strconv.Unquote(q)
		if err == nil && echoes(v, lits) {
			// The quoted text goes, and the space before it.
			b.WriteString(strings.TrimSuffix(receipt[i:j], " "))
		} else {
			b.WriteString(receipt[i : j+len(q)])
		}
		i = j + len(q)
	}
	return b.String()
}

// echoes reports whether v, a text a receipt quotes, is one of the code's
// own texts: the same, or its beginning when the receipt cut it short
// ("…"). Line breaks and runs of spaces count as one space, as a receipt's
// line shows them (it writes a line break as " ⏎ "). A text of fewer than
// three letters is left: it says too little to be sure of.
func echoes(v string, lits []string) bool {
	cut := strings.HasSuffix(v, "…")
	v = oneSpaced(strings.TrimSuffix(v, "…"))
	if utf8.RuneCountInString(v) < 3 {
		return false
	}
	for _, l := range lits {
		l = oneSpaced(l)
		if l == v || cut && strings.HasPrefix(l, v) {
			return true
		}
	}
	return false
}

// oneSpaced is s with its line breaks and runs of spaces made single spaces.
func oneSpaced(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "⏎", " ")), " ")
}

// stringLiterals is the texts of the string literals in Gno code, which is
// Go's syntax. Code that does not scan whole gives what scanned.
func stringLiterals(code string) []string {
	if !strings.ContainsAny(code, "\"`") {
		return nil
	}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(code))
	var s scanner.Scanner
	s.Init(file, []byte(code), func(token.Position, string) {}, 0)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		if tok != token.STRING {
			continue
		}
		if v, err := strconv.Unquote(lit); err == nil {
			out = append(out, v)
		}
	}
}
