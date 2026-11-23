package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// FileChanged hooks run when a watched file is made, changed or removed,
// whatever did it: the files the matchers name, in the folder the session
// started in, and the ones a SessionStart hook named (watchPaths). A group
// with no matcher runs for every watched file and adds none. An export a
// hook writes to the environment file reaches the next command.
func TestFileChangedHook(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "Hi."},
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"echo v=$FROM_FILE"}`}}},
		apptest.Reply{Text: "Done."},
	)
	other := filepath.Join(w.Home, "other.txt")
	logA, logB := w.Home+"/log-a", w.Home+"/log-b"
	a := w.Script(w.Home+"/hooks/a.sh", "cat >> "+logA+"\necho >> "+logA+"\necho 'export FROM_FILE=1' >> \"$CLAUDE_ENV_FILE\"")
	b := w.Script(w.Home+"/hooks/b.sh", "cat >> "+logB+"\necho >> "+logB)
	start := w.Script(w.Home+"/hooks/start.sh", `cat > /dev/null; echo '{"hookSpecificOutput": {"hookEventName": "SessionStart", "watchPaths": ["`+other+`"]}}'`)
	w.Settings(`{"hooks": {
		"SessionStart": [{"hooks": [{"type": "command", "command": "` + start + `"}]}],
		"FileChanged": [{"matcher": ".envrc", "hooks": [{"type": "command", "command": "` + a + `"}]}, {"hooks": [{"type": "command", "command": "` + b + `"}]}]}}`)
	s := sitWith(t, w, func(*Options) {}, "-y")
	s.see("Type a prompt and Enter.")
	s.typeLine("hi")
	s.see("Hi.")

	w.Write(".envrc", "export X=1\n")
	waitForText(t, logA, `"event":"add"`, `"file_path":"`+w.Path(".envrc")+`"`, `"hook_event_name":"FileChanged"`)
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForText(t, logB, `"file_path":"`+other+`"`)
	if data, _ := os.ReadFile(logA); strings.Contains(string(data), other) {
		t.Errorf("the .envrc group ran for %s", other)
	}

	s.typeLine("go on")
	s.see("Done.")
	has(t, "the command after the hook", toolResult(t, w, 2, "c1"), "v=1")

	if err := os.Remove(w.Path(".envrc")); err != nil {
		t.Fatal(err)
	}
	waitForText(t, logA, `"event":"unlink"`)
	s.typeLine("/exit")
	s.exit()
}

// waitForText waits, ten seconds at most, for the file at path to hold
// every one of wants.
func waitForText(t *testing.T, path string, wants ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		missing := ""
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				missing = want
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held %q:\n%s", path, missing, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
