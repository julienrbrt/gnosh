package shellquote

import (
	"errors"
	"fmt"
	"strings"
)

// Split reads a command line as a POSIX shell reads one simple command's
// words: blanks between words, single quotes taking everything in them as
// it is, double quotes taking everything but a backslash before " \ $ or
// `, and a backslash outside quotes taking the character after it. It is
// for a command that is run with no shell: anything only a shell could
// carry out is refused, unquoted: a pipe, a list or a redirection (| & ;
// < >), a group or a subshell (parentheses), an expansion ($ or `), and a
// line break. A quote that is not closed is refused too.
func Split(line string) ([]string, error) {
	var words []string
	var w strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			if inWord {
				words, inWord = append(words, w.String()), false
				w.Reset()
			}
		case c == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("a single quote is not closed")
			}
			w.WriteString(line[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(line) && line[i] != '"'; i++ {
				switch d := line[i]; {
				case d == '\\' && i+1 < len(line) && strings.IndexByte("\"\\$`", line[i+1]) >= 0:
					i++
					w.WriteByte(line[i])
				case d == '$' || d == '`':
					return nil, fmt.Errorf("%c inside double quotes asks a shell to expand it, and this command is run with none", d)
				default:
					w.WriteByte(d)
				}
			}
			if i >= len(line) {
				return nil, errors.New("a double quote is not closed")
			}
			inWord = true
		case c == '\\':
			if i+1 >= len(line) {
				return nil, errors.New("the line ends with a backslash")
			}
			i++
			if line[i] == '\n' {
				return nil, errors.New("a line break is a shell's to read, and this command is run with none")
			}
			w.WriteByte(line[i])
			inWord = true
		case strings.IndexByte("|&;<>()$`\n\r", c) >= 0:
			what := string(c)
			if c == '\n' || c == '\r' {
				what = "a line break"
			}
			return nil, fmt.Errorf("%s is a shell's to read, and this command is run with none: give the program's words alone", what)
		default:
			w.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, w.String())
	}
	return words, nil
}
