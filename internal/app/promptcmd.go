package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/cli"
	"github.com/gnosh-ai/gnosh/internal/prompt"
)

// This file is gnosh prompt, which Jae asked for (spec/gnosh/harness-cli.md,
// CLI-12): gnosh's own words to the model, read without a session, and
// made the person's own. Each text is a file of internal/prompt; a file of
// the person's of its name, in the config's prompts folder, stands in for
// it in every session from then on (prompt.Load, which a session reads at
// its start).

// kindWords says where a text of each kind goes, for gnosh prompt show.
var kindWords = map[prompt.Kind]string{
	prompt.KindSystem: "the system prompt",
	prompt.KindLine:   "a line of gnosh's on your side",
	prompt.KindResult: "inside a tool's result",
	prompt.KindSide:   "a small request of gnosh's own",
}

// promptCommand is gnosh prompt show, path and export.
func (p *program) promptCommand(ctx context.Context, inv cli.Invocation) int {
	st, code := p.prepared(ctx, inv)
	if st == nil {
		return code
	}
	dir := filepath.Join(st.cfg.Paths.ConfigDir, "prompts")
	home := st.cfg.Paths.Home
	switch inv.Command {
	case cli.CmdPromptPath:
		fmt.Fprintln(p.stdout, dir)
		return cli.ExitOK
	case cli.CmdPromptExport:
		return p.exportTexts(dir, home, inv.Args, inv.Force)
	}
	set, _, err := prompt.Load(dir)
	if err != nil {
		// (A session would not start with them either.)
		p.errf("your own texts in %s cannot be used, and no session starts while they are there:\n%v", homeTilde(home, dir), err)
		return cli.ExitFailure
	}
	if len(inv.Args) == 1 {
		info, ok := set.Text(inv.Args[0])
		if !ok {
			p.errf("gnosh has no text called %q. gnosh prompt show lists them.", inv.Args[0])
			return cli.ExitFailure
		}
		from := "gnosh's own words"
		if info.Replaced {
			from = "your own words, from " + homeTilde(home, info.Path)
			if info.Stale {
				from += " (gnosh's own text has changed since it was exported)"
			}
		}
		fmt.Fprintf(p.stderr, "%s: %s, about %s tokens. %s\n", info.Name, from, thousands(info.Tokens), info.When)
		if holes := append(info.Required, info.Optional...); len(holes) > 0 {
			fmt.Fprintf(p.stderr, "It is given %s; a text of yours must keep %s.\n", braces(holes), firstOf(braces(info.Required), "none of them"))
		}
		fmt.Fprintln(p.stdout, info.Body)
		return cli.ExitOK
	}
	texts := set.Texts()
	total, mine := 0, 0
	width := 0
	for _, t := range texts {
		width = max(width, utf8.RuneCountInString(t.Name))
		if t.Kind == prompt.KindSystem {
			total += t.Tokens
		}
		if t.Replaced {
			mine++
		}
	}
	fmt.Fprintf(p.stdout, "gnosh's %d texts to the model. Where each goes, about how many tokens it is, and whose:\n", len(texts))
	for _, t := range texts {
		whose := ""
		if t.Replaced {
			whose = "  yours"
			if t.Stale {
				whose += " (gnosh's own has changed since)"
			}
		}
		fmt.Fprintf(p.stdout, "  %-*s  %-32s %6s%s\n", width, t.Name, kindWords[t.Kind], thousands(t.Tokens), whose)
	}
	fmt.Fprintf(p.stdout, "The system prompt's parts come to about %s tokens, but no session is sent them all: each is sent only when it applies. ", thousands(total))
	if mine > 0 {
		fmt.Fprintf(p.stdout, "%d of the texts are yours, from %s. ", mine, homeTilde(home, dir))
	}
	fmt.Fprintln(p.stdout, "gnosh prompt show <name> prints one, and gnosh prompt export <name> writes it there to make it yours.")
	return cli.ExitOK
}

// exportTexts writes gnosh's own texts named, or all of them, into dir as
// files to edit. A file that is there is kept unless force is set. Every
// name is checked before anything is written.
func (p *program) exportTexts(dir, home string, names []string, force bool) int {
	if len(names) == 0 {
		names = prompt.Names()
	}
	bodies := make([]string, len(names))
	for i, name := range names {
		body, err := prompt.Export(name)
		if err != nil {
			p.errf("%v. Nothing was written.", err)
			return cli.ExitFailure
		}
		bodies[i] = body
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.errf("the folder %s could not be made: %v. Nothing was written.", dir, err)
		return cli.ExitFailure
	}
	var wrote, kept []string
	for i, name := range names {
		path := filepath.Join(dir, name+".md")
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if force {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		f, err := os.OpenFile(path, flags, 0o644)
		if errors.Is(err, fs.ErrExist) {
			kept = append(kept, name)
			continue
		}
		if err == nil {
			_, err = f.WriteString(bodies[i])
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			p.errf("%s could not be written: %v.", path, err)
			return cli.ExitFailure
		}
		wrote = append(wrote, name)
	}
	where := homeTilde(home, dir)
	switch {
	case len(wrote) == 1:
		fmt.Fprintf(p.stdout, "Wrote %s: gnosh's own words, which change nothing until you change them; from then on, every new session is sent yours.\n", homeTilde(home, filepath.Join(dir, wrote[0]+".md")))
	case len(wrote) > 1:
		fmt.Fprintf(p.stdout, "Wrote %d texts into %s: gnosh's own words, which change nothing until you change them; from then on, every new session is sent yours.\n", len(wrote), where)
	}
	if len(kept) > 0 {
		files := make([]string, len(kept))
		for i, name := range kept {
			files[i] = name + ".md"
		}
		fmt.Fprintf(p.stdout, "Kept, as they are there already: %s in %s (--force writes over them).\n", strings.Join(files, ", "), where)
	}
	return cli.ExitOK
}

// braces writes names as a text writes them, {{name}}, in a list.
func braces(names []string) string {
	if len(names) == 0 {
		return ""
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "{{" + n + "}}"
	}
	return strings.Join(out, ", ")
}
