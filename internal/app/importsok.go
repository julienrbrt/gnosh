package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/instructions"
)

// This file is the question Claude Code puts once per project when its
// instruction files bring in files from outside it, by an import or a
// link (its memory page, "Import additional files"): load them, or not.
// The answer is kept for the project in the person's own state folder,
// never in the project, and the question is not put again; a run, which
// nobody attends, loads them only once the person has said yes. Where
// keys are kept is never loaded, whatever the answer (instructions'
// secretNames).

// importsAnswer is the answer kept for one project.
type importsAnswer struct {
	Version int    `json:"version"`
	Project string `json:"project"`
	Allow   bool   `json:"allow"`
	When    string `json:"when"`
}

// importsFile is where the answer for this session's project is kept.
func (l *live) importsFile() string {
	root := firstOf(l.cfg.Context.ProjectRoot, l.cfg.Paths.ProjectRoot, l.cfg.Paths.Cwd)
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(l.cfg.Paths.StateDir, "imports", hex.EncodeToString(sum[:16])+".json")
}

// outsideImports reports what the person said of this project's files from
// outside it, and whether they said anything.
func (l *live) outsideImports() (allow, answered bool) {
	if l.cfg.Paths.StateDir == "" {
		return false, false
	}
	data, err := os.ReadFile(l.importsFile())
	if err != nil {
		return false, false
	}
	var a importsAnswer
	if json.Unmarshal(data, &a) != nil || a.Version != 1 {
		return false, false
	}
	return a.Allow, true
}

// keepOutsideImports keeps the person's answer for this project, written
// whole to a temporary name and renamed into place, the person's alone.
func (l *live) keepOutsideImports(allow bool) error {
	path := l.importsFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(importsAnswer{Version: 1, Project: firstOf(l.cfg.Context.ProjectRoot, l.cfg.Paths.ProjectRoot, l.cfg.Paths.Cwd), Allow: allow, When: l.p.now().UTC().Format("2006-01-02T15:04:05Z")})
	tmp, err := os.CreateTemp(filepath.Dir(path), ".answer-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}

// askOutsideImports puts the question, when the session is attended, has
// not been asked it for this project, and set leaves files out for want
// of a yes. It reports whether the files are to be read again, now let in.
func (l *live) askOutsideImports(set *instructions.Set) bool {
	out := set.Unapproved()
	if len(out) == 0 || !l.interactive {
		return false
	}
	if _, answered := l.outsideImports(); answered {
		return false
	}
	asker, ok := l.front.(Asker)
	if !ok {
		return false
	}
	slices.Sort(out)
	out = slices.Compact(out)
	var b strings.Builder
	b.WriteString("The project's instruction files bring in files from outside it, which gnosh loads only if you say so:")
	for i, p := range out {
		if i == 10 {
			fmt.Fprintf(&b, "\n  …and %d more", len(out)-10)
			break
		}
		b.WriteString("\n  " + l.tilde(p))
	}
	b.WriteString("\nThe answer is kept for this project, and the question is not put again.")
	ans, err := asker.AskBefore(agent.Question{
		Kind:  "imports",
		Title: "Load files from outside the project?",
		Text:  b.String(),
		Choices: []agent.Choice{
			{Key: "yes", Label: "Yes, load them, in this project's sessions"},
			{Key: "no", Label: "No, leave them out"},
		},
	})
	if err != nil {
		// Not answered (the session ended first): asked again next time.
		return false
	}
	allow := ans.Choice == "yes"
	if err := l.keepOutsideImports(allow); err != nil {
		l.notice(agent.LevelWarning, "instructions", "Your answer could not be kept ("+err.Error()+"), so the question comes again in the next session.")
	}
	return allow
}
