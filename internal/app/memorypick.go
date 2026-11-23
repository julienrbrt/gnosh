package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/trust"
)

// MemoryFile is a context file the /memory picker offers to open in the
// person's editor (as Claude Code's /memory opens its memory files): one
// the model is given, or the place of the person's own or of the
// project's.
type MemoryFile struct {
	Path string
	// About says whose it is and how big, or that it is not there yet.
	About string
}

// MemoryFiles lists the context files /memory offers: those the model is
// given in this session, then the person's own and the project's
// GNOSH.md where they are not among them.
func (c *controls) MemoryFiles() []MemoryFile { return c.cur().memoryFiles() }

// memoryFiles is SessionControls.MemoryFiles.
func (l *live) memoryFiles() []MemoryFile {
	var out []MemoryFile
	seen := map[string]bool{}
	if l.instr != nil {
		for _, f := range l.instr.Files() {
			about := strings.ToLower(string(f.Scope)) + ", " + ByteCount(f.Size)
			if f.Late {
				about += ", read when a file under it was touched"
			}
			out = append(out, MemoryFile{Path: f.Path, About: about})
			seen[f.Path] = true
		}
	}
	add := func(path, about string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		if _, err := os.Stat(path); err != nil {
			about += ", not there yet"
		}
		out = append(out, MemoryFile{Path: path, About: about})
	}
	if len(l.cfg.Context.Global) > 0 {
		add(l.cfg.Context.Global[0].V, "yours, for every project")
	}
	// The project's own, unless it is a link that leads out of the
	// project: the instructions are not read through such a link, and the
	// editor would write, or make, the file it leads to (the plan review
	// of 2026-11-19).
	project := filepath.Join(l.cfg.Paths.ProjectRoot, "GNOSH.md")
	if _, err := os.Lstat(project); err != nil {
		add(project, "the project's, shared with whoever works on it")
	} else if _, out := trust.LeadsOut(project, projectInside(l.cfg, nil)...); !out {
		add(project, "the project's, shared with whoever works on it")
	}
	return out
}
