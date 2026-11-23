package shellquote

import "strings"

// A frame is a construct of the shell's that is open at a place in a
// command: a quote, a command substitution, backquotes, or an arithmetic
// expression. depth counts the parentheses open inside a substitution or
// an expression, which its own closing ones are not.
type frame struct {
	kind  byte // ' single quotes, $ $'...' quotes, " double quotes, ( $(...), ` backquotes, a (( )) or $(( ))
	depth int
}

// CommandStarts says, for each line of a command, whether the shell starts
// reading a command there: not the text or the end of a here-document, not
// a line that the one before goes on into (a backslash at its end), and
// not a line inside quotes opened on a line before. Quotes are followed
// the way the shell reads them, inside command substitutions and
// backquotes too, so that a << inside them opens nothing and a quote
// inside "$(...)" is a quote of its own.
func CommandStarts(command string) []bool {
	lines := strings.Split(command, "\n")
	out := make([]bool, len(lines))
	var ends []string // the words that end the open here-documents, in order
	var dash []bool   // each may stand indented by tabs (<<-)
	var stack []frame // what is open, the innermost last
	goesOn := false   // the line before ends with a backslash
	quoted := func() bool {
		for _, f := range stack {
			if f.kind != '(' && f.kind != 'a' {
				return true
			}
		}
		return false
	}
	for i, line := range lines {
		if len(ends) > 0 {
			l := line
			if dash[0] {
				l = strings.TrimLeft(l, "\t")
			}
			if l == ends[0] {
				ends, dash = ends[1:], dash[1:]
			}
			continue
		}
		out[i] = !quoted() && !goesOn
		goesOn = false
	scan:
		for j := 0; j < len(line); j++ {
			c, rest := line[j], line[j:]
			var top *frame
			if n := len(stack); n > 0 {
				top = &stack[n-1]
			}
			switch {
			case top != nil && top.kind == '\'':
				if c == '\'' {
					stack = stack[:len(stack)-1]
				}
				continue
			case top != nil && top.kind == '$':
				switch c {
				case '\\':
					j++
				case '\'':
					stack = stack[:len(stack)-1]
				}
				continue
			case top != nil && top.kind == '"':
				switch {
				case c == '\\':
					j++
				case c == '"':
					stack = stack[:len(stack)-1]
				case c == '`':
					stack = append(stack, frame{kind: '`'})
				case strings.HasPrefix(rest, "$(("):
					stack = append(stack, frame{kind: 'a'})
					j += 2
				case strings.HasPrefix(rest, "$("):
					stack = append(stack, frame{kind: '('})
					j++
				}
				continue
			case top != nil && top.kind == 'a':
				switch {
				case top.depth == 0 && strings.HasPrefix(rest, "))"):
					stack = stack[:len(stack)-1]
					j++
				case c == '(':
					top.depth++
				case c == ')' && top.depth > 0:
					top.depth--
				}
				continue
			}
			// The shell's own words: at the top, or inside $(...) or
			// backquotes.
			switch {
			case c == '\\':
				if j == len(line)-1 {
					goesOn = true
				}
				j++
			case c == '`' && top != nil && top.kind == '`':
				stack = stack[:len(stack)-1]
			case c == '`':
				stack = append(stack, frame{kind: '`'})
			case c == '\'' || c == '"':
				stack = append(stack, frame{kind: c})
			case strings.HasPrefix(rest, "$'"):
				stack = append(stack, frame{kind: '$'})
				j++
			case strings.HasPrefix(rest, "$(("), strings.HasPrefix(rest, "(("):
				stack = append(stack, frame{kind: 'a'})
				j += strings.Index(rest, "((") + 1
			case strings.HasPrefix(rest, "$("):
				stack = append(stack, frame{kind: '('})
				j++
			case c == '(' && top != nil && top.kind == '(':
				top.depth++
			case c == ')' && top != nil && top.kind == '(':
				if top.depth == 0 {
					stack = stack[:len(stack)-1]
				} else {
					top.depth--
				}
			case c == '#' && (j == 0 || strings.IndexByte(" \t;&|()`", line[j-1]) >= 0):
				break scan
			case strings.HasPrefix(rest, "<<<"):
				// A here-string: what follows is a word, not a document.
				j += 2
			case strings.HasPrefix(rest, "<<"):
				if word, dashed, n, ok := heredocWord(rest); ok {
					ends = append(ends, word)
					dash = append(dash, dashed)
					j += n - 1
				}
			}
		}
	}
	return out
}

// heredocWord reads the start of a here-document at the start of s, which
// begins with <<: whether it is <<-, the word that ends it as the line
// that ends it holds it (its quotes taken off: 'EOF', "EOF", \EOF and EOF
// are all EOF), and how long the whole is. ok is false when no word
// follows.
func heredocWord(s string) (word string, dash bool, n int, ok bool) {
	i := 2
	if i < len(s) && s[i] == '-' {
		dash = true
		i++
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	start := i
	var b strings.Builder
	for i < len(s) && strings.IndexByte(" \t;&|<>()", s[i]) < 0 {
		switch c := s[i]; {
		case c == '\'' || c == '"':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				end = len(s) - i - 1
			}
			b.WriteString(s[i+1 : i+1+end])
			i = min(len(s), i+end+2)
		case c == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	if i == start {
		return "", false, 0, false
	}
	return b.String(), dash, i, true
}
