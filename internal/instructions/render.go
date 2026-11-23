package instructions

import (
	"strings"
	"unicode"
)

// The words the model is given around the files. They are gnosh's own.
const (
	startIntro = `Standing instructions

These files hold the standing instructions of the person you are working for and of the project you are working in. Follow them for the whole session. Where they ask for something other than what you would do by default, do what they ask. What the person tells you directly still comes first.

The files run from the most general to the most specific; where two of them disagree, follow the later one. Each file begins at a line of the form "=== path (what the file is) ===".
`

	lateIntro = `More standing instructions

These instruction files cover what you have just started working on. They carry the same weight as the standing instructions you were given at the start of the session, and they hold from here on. Each file begins at a line of the form "=== path (what the file is) ===".
`

	cutNote = "[This is only the start of the file, which is too long to give whole. Read the rest with the Read tool.]"
)

// Render returns the text to hand the model at the start of a session: a
// short preamble, then every file in effect from the start, each under a
// line that gives its path and says what kind of file it is. Late files are
// not part of it. The same files always give the same text, so that a
// prompt that begins with it can be cached. With nothing to say, it returns
// the empty string.
func (s *Set) Render() string {
	s.mu.Lock()
	var files []File
	for _, f := range s.files {
		if !f.Late {
			files = append(files, f)
		}
	}
	s.mu.Unlock()
	return render(startIntro, files)
}

// RenderFiles returns the text to hand the model for files that arrived
// during the session, as ForPath returned them: a preamble that says they
// now apply, then the files, laid out as Render lays them out.
func RenderFiles(files []File) string {
	return render(lateIntro, files)
}

// Shown is how the header of a file begins in what the model is given,
// from the line break before it: what tells whether the file is still in
// a conversation. A line of a file's own text never begins so: guard sets
// such a line apart with a backslash.
func Shown(f File) string {
	return "\n=== " + plain(f.Path) + " ("
}

// render lays files out under intro. A file with no text is left out.
func render(intro string, files []File) string {
	var b strings.Builder
	for _, f := range files {
		text := strings.TrimLeft(strings.TrimRight(f.Text, " \t\n"), "\n")
		if text == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString(intro)
		}
		b.WriteString("\n=== ")
		b.WriteString(plain(f.Path))
		b.WriteString(" (")
		b.WriteString(describe(f))
		b.WriteString(") ===\n")
		b.WriteString(guard(text))
		b.WriteString("\n")
		if f.Truncated {
			b.WriteString(cutNote)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// describe says in a few words what kind of file f is: whose it is and what
// it covers.
func describe(f File) string {
	if f.Kind == KindImport {
		return "imported by " + plain(f.Parent)
	}
	var what string
	switch {
	case f.Scope == ScopeUser && f.Kind == KindRule:
		what = "one of the person's own rules"
	case f.Scope == ScopeUser:
		what = "the person's own instructions"
	case f.Scope == ScopeLocal:
		what = "the person's private notes on this project"
	case f.Kind == KindRule:
		what = "project rule"
	default:
		what = "project instructions"
	}
	switch {
	case len(f.Globs) > 0:
		globs := make([]string, len(f.Globs))
		for i, g := range f.Globs {
			globs[i] = plain(g)
		}
		what += ", for files matching " + strings.Join(globs, ", ")
		if f.Scope != ScopeUser && f.Base != "" {
			what += " under " + plain(f.Base)
		}
	case f.Reason == ReasonNested && f.Base != "":
		what += ", for work under " + plain(f.Base)
	case f.Scope == ScopeUser:
		what += ", for every project"
	}
	return what
}

// guard keeps a file's text from passing for the start of another file: a
// line that has the shape of the line that introduces a file gets a
// backslash in front of it. The shape is taken loosely, any kind of space
// or none beside the marks, as a model reads it: "===" and a no-break
// space passed for gnosh's own header of the person's file (the
// instruction-files review of 2026-11-20). A line of "=" alone, the
// underline of a heading, is left as it is.
func guard(text string) string {
	if !strings.Contains(text, "===") {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		t := strings.TrimFunc(line, unicode.IsSpace)
		if strings.HasPrefix(t, "===") && strings.HasSuffix(t, "===") &&
			strings.TrimFunc(t, func(r rune) bool { return r == '=' || unicode.IsSpace(r) }) != "" {
			lines[i] = "\\" + line
		}
	}
	return strings.Join(lines, "\n")
}
