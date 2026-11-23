package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// FromRunes is the protocol's position of the 1-based line and character
// a model counts in a file's text (characters as Unicode code points),
// given the text of that line. A character past the line's end is its
// end; one below 1 is its start.
func FromRunes(line, char int, text string) Position {
	units := 0
	for i := 1; i < char && text != ""; i++ {
		// (A byte that begins no character decodes as U+FFFD, one unit.)
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]
		units += utf16.RuneLen(r)
	}
	return Position{Line: max(line-1, 0), Character: units}
}

// ToRunes is the 1-based line and character of a protocol position, given
// the text of its line.
func ToRunes(p Position, text string) (line, char int) {
	units, char := 0, 1
	for units < p.Character && text != "" {
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]
		units += utf16.RuneLen(r)
		char++
	}
	return p.Line + 1, char
}

// FileURI is the file:// URI of an absolute path.
func FileURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// URIPath is the path of a file:// URI, or "" for another kind.
func URIPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if strings.HasPrefix(p, "/") && len(p) > 2 && p[2] == ':' {
		// (file:///C:/x on Windows.)
		p = p[1:]
	}
	return filepath.FromSlash(p)
}
