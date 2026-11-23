package buildinfo

import (
	_ "embed"
	"strings"
)

// changes is the list of the commits this build was made from, newest
// first, one a line: the short hash, a blank, and the subject. A build for
// the machines gnosh is installed on writes it from git log before it
// compiles; the file in the repository is empty, and a build of the tree
// as it stands carries none.
//
//go:embed changes.txt
var changes string

// Change is one commit of the history this build was made from.
type Change struct {
	ID, Subject string
}

// Changes returns the commits this build was made from, newest first, or
// none when the build was not given them.
func Changes() []Change {
	return parseChanges(changes)
}

func parseChanges(text string) []Change {
	var out []Change
	for _, line := range strings.Split(text, "\n") {
		id, subject, _ := strings.Cut(strings.TrimSpace(line), " ")
		if id == "" {
			continue
		}
		out = append(out, Change{ID: id, Subject: strings.TrimSpace(subject)})
	}
	return out
}

// Since returns the commits this build has that the build named old has
// not, newest first, and whether old is among the commits the list holds:
// when it is not (it is older than the list reaches, or another line of
// work), the whole list comes back and the count is a floor.
func Since(old string) ([]Change, bool) {
	return since(Changes(), old)
}

func since(all []Change, old string) ([]Change, bool) {
	old = strings.TrimSuffix(strings.TrimSpace(old), "+dirty")
	if old == "" {
		return all, false
	}
	for i, c := range all {
		if strings.HasPrefix(c.ID, old) || strings.HasPrefix(old, c.ID) {
			return all[:i], true
		}
	}
	return all, false
}
