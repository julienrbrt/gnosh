package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// gnosh prompt path names the folder of the person's own texts. gnosh
// prompt export writes gnosh's own text there, which changes nothing until
// it is edited, and keeps a file that is there unless --force is given.
// gnosh prompt show lists the texts and marks the person's, and prints
// one, with what it is on standard error. A file that cannot stand in for
// its text is refused, as a session refuses it.
func TestPromptCommands(t *testing.T) {
	w := apptest.NewWorld(t)
	dir := filepath.Join(w.ConfigDir(), "prompts")
	if got := strings.TrimSpace(run(t, w, "prompt", "path").wantCode(t, 0).out); got != dir {
		t.Fatalf("gnosh prompt path: %q, want %q", got, dir)
	}
	r := run(t, w, "prompt", "show").wantCode(t, 0)
	has(t, "the list", r.out, "gnosh's ", " texts to the model", "  core ", "the system prompt", "  mem-line ", "a line of gnosh's on your side", "gnosh prompt show <name> prints one")
	hasNot(t, "the list", r.out, "  yours", "texts are yours")

	has(t, "export", run(t, w, "prompt", "export", "mem-line").wantCode(t, 0).out, "Wrote ~/.config/gnosh/prompts/mem-line.md: gnosh's own words, which change nothing until you change them")
	file := filepath.Join(dir, "mem-line.md")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	hasNot(t, "the list after an export", run(t, w, "prompt", "show").wantCode(t, 0).out, "  yours", "texts are yours")
	// Edited, it is the person's: listed so, and printed as it is sent.
	edited := strings.Replace(string(data), "Use mem and Gno", "Use mem, and Gno,", 1)
	if edited == string(data) {
		t.Fatalf("the exported file does not hold the text:\n%s", data)
	}
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	has(t, "the list after an edit", run(t, w, "prompt", "show").wantCode(t, 0).out, "  yours", "1 of the texts are yours, from ~/.config/gnosh/prompts.")
	r = run(t, w, "prompt", "show", "mem-line").wantCode(t, 0)
	if !strings.HasPrefix(r.out, "Use mem, and Gno,") || strings.Contains(r.out, "gnosh-text") {
		t.Errorf("gnosh prompt show mem-line printed:\n%s", r.out)
	}
	has(t, "what it is", r.err, "mem-line: your own words, from ~/.config/gnosh/prompts/mem-line.md, about ", "Last on every prompt a person types")
	has(t, "a text with holes", run(t, w, "prompt", "show", "frame-line").wantCode(t, 0).err, "It is given {{id}}, {{purpose}}", "a text of yours must keep {{id}}.")

	// Kept unless --force; every name is checked before anything is written.
	has(t, "export again", run(t, w, "prompt", "export", "mem-line", "core").wantCode(t, 0).out,
		"Wrote ~/.config/gnosh/prompts/core.md", "Kept, as they are there already: mem-line.md in ~/.config/gnosh/prompts (--force writes over them).")
	if data, _ := os.ReadFile(file); string(data) != edited {
		t.Error("an export without --force wrote over the person's file")
	}
	run(t, w, "prompt", "export", "--force", "mem-line").wantCode(t, 0)
	if data, _ := os.ReadFile(file); string(data) == edited {
		t.Error("an export with --force kept the person's file")
	}
	has(t, "an unknown name", run(t, w, "prompt", "export", "core", "nosuch").wantCode(t, 1).err, `gnosh has no text called "nosuch"`, "Nothing was written.")
	has(t, "show of an unknown name", run(t, w, "prompt", "show", "nosuch").wantCode(t, 1).err, `gnosh has no text called "nosuch"`)

	// A file that is no text's stops a session, so gnosh prompt show says so.
	if err := os.WriteFile(filepath.Join(dir, "nosuch.md"), []byte("words"), 0o644); err != nil {
		t.Fatal(err)
	}
	has(t, "a file that cannot be used", run(t, w, "prompt", "show").wantCode(t, 1).err, "your own texts in ~/.config/gnosh/prompts cannot be used", "nosuch.md")
}
