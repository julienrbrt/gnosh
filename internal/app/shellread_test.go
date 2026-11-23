package app

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// Which shell commands view one file, as Claude Code counts them.
func TestViewedFile(t *testing.T) {
	for line, want := range map[string]string{
		"cat notes.txt":             "notes.txt",
		"cat -n notes.txt":          "notes.txt",
		"nl notes.txt":              "notes.txt",
		"head -n 20 notes.txt":      "notes.txt",
		"head -20 notes.txt":        "notes.txt",
		"tail -c 100 notes.txt":     "notes.txt",
		"sed -n '10,20p' notes.txt": "notes.txt",
		"sed -n 5p notes.txt":       "notes.txt",
		"sed -n '10,$p' notes.txt":  "notes.txt",
		"grep -n beta notes.txt":    "notes.txt",
		"rg -i beta notes.txt":      "notes.txt",
		"/bin/cat notes.txt":        "notes.txt",
		"cat a.txt b.txt":           "",
		"cat":                       "",
		"cat -":                     "",
		"tail -f log.txt":           "",
		"sed -i 's/a/b/' notes.txt": "",
		"sed -n 'p;p' notes.txt":    "",
		"grep -e beta notes.txt":    "",
		"grep beta":                 "",
		"grep beta a.txt b.txt":     "",
		"ls notes.txt":              "",
		"python3 notes.txt":         "",
	} {
		words := strings.Fields(strings.NewReplacer("'", "").Replace(line))
		if got := viewedFile(words); got != want {
			t.Errorf("viewedFile(%q) = %q, want %q", line, got, want)
		}
	}
}

// A file viewed with cat counts as read, as in Claude Code: the model may
// edit it without a separate Read. One read through a pipe does not.
func TestEditAfterCat(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"cat notes.txt"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Edit", Args: `{"file_path":"notes.txt","old_string":"beta","new_string":"BETA"}`}}},
		apptest.Reply{Text: "Done."})
	w.Write("notes.txt", "alpha\nbeta\n")
	run(t, w, "-y", "-p", "fix it").wantCode(t, 0)
	if got := w.Read("notes.txt"); got != "alpha\nBETA\n" {
		t.Errorf("notes.txt holds %q:\n%s", got, toolResult(t, w, 2, "c2"))
	}

	w = apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"cat notes.txt | head -5"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Edit", Args: `{"file_path":"notes.txt","old_string":"beta","new_string":"BETA"}`}}},
		apptest.Reply{Text: "Could not."})
	w.Write("notes.txt", "alpha\nbeta\n")
	run(t, w, "-y", "-p", "fix it").wantCode(t, 0)
	has(t, "an edit after a pipe", toolResult(t, w, 2, "c2"), "has not read")
}

// A file viewed with cat in a folder with instruction files of its own has
// them arrive, as after a Read.
func TestCatBringsTheFoldersInstructions(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"cat api/x.go"}`}}},
		apptest.Reply{Text: "Done."})
	w.Write("api/AGENTS.md", "API RULES HERE\n")
	w.Write("api/x.go", "package api\n")
	run(t, w, "-y", "-p", "look").wantCode(t, 0)
	has(t, "the request after the cat", requestText(t, w, 1), "API RULES HERE")
}
