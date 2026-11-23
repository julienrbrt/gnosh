package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app/agentdef"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/trust"
)

// This file is the output styles: Claude Code's outputStyle setting and its
// /output-style command. A style is instructions on how the model replies,
// sent in the system prompt right after gnosh's core, from the next message
// on. gnosh has Claude Code's four besides the default (prompt's
// BuiltinStyles), and a person or a trusted project adds more as Markdown
// files in an output-styles folder, beside their skills: frontmatter (name,
// description, keep-coding-instructions) and the instructions.

// defaultStyle is the name of no style at all.
const defaultStyle = "default"

// outputStyle is a style and where it comes from: a file, or gnosh.
type outputStyle struct {
	prompt.Style
	source string
	// project says the style is the project's own: its text is put to the
	// model as text from outside.
	project bool
}

// maxStyleBytes is the most of a style file that is read.
const maxStyleBytes = 64 << 10

// loadOutputStyles reads the styles there are: the person's, then the
// project's, when the folder is trusted, the closest folder first, then
// gnosh's own. The first of a name wins, in any case: a project's style
// does not stand in for one of the person's own (the status line review
// of 2026-11-19: a repository's concise.md replaced the person's
// Concise). A project's file that leads out of the project is not read,
// as a project's skill is not. Files that cannot be used are said in
// problems.
func loadOutputStyles(texts *prompt.Set, cfg *config.Config, trusted bool, more []string) (styles []outputStyle, problems []string) {
	seen := map[string]bool{defaultStyle: true}
	var dirs []string
	project := map[string]bool{}
	if !cfg.Switches.SafeMode.V {
		dirs = append(dirs, userDefDirs(cfg, "output-styles")...)
	}
	if trusted && !cfg.Switches.SafeMode.V {
		for _, d := range projectDefDirs(cfg, "output-styles", more) {
			dirs, project[d] = append(dirs, d), true
		}
	}
	inside := projectInside(cfg, more)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			path := filepath.Join(dir, n)
			if project[dir] {
				if target, out := trust.LeadsOut(path, inside...); out {
					problems = append(problems, fmt.Sprintf("the output style %s is not read: it leads out of the project, to %s", path, firstOf(target, "a place that is not there")))
					continue
				}
			}
			st, why := readOutputStyle(path)
			if why != "" {
				problems = append(problems, fmt.Sprintf("the output style %s cannot be used: %s", path, why))
				continue
			}
			st.project = project[dir]
			if key := strings.ToLower(st.Name); !seen[key] {
				seen[key] = true
				styles = append(styles, st)
			}
		}
	}
	for _, b := range texts.BuiltinStyles() {
		if key := strings.ToLower(b.Name); !seen[key] {
			seen[key] = true
			styles = append(styles, outputStyle{Style: b, source: "gnosh"})
		}
	}
	return styles, problems
}

// readOutputStyle reads one style file. Settings that cannot be read are
// no settings, as Claude Code has it: the style loads under its file's
// name with none.
func readOutputStyle(path string) (outputStyle, string) {
	st := outputStyle{source: path}
	// A regular file, and no more of it read than a style may hold: one
	// linked to /dev/zero was read until memory ran out (the status line
	// review of 2026-11-19).
	info, err := os.Stat(path)
	if err != nil {
		return st, err.Error()
	}
	if !info.Mode().IsRegular() {
		return st, "it is not a file"
	}
	f, err := os.Open(path)
	if err != nil {
		return st, err.Error()
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStyleBytes+1))
	f.Close()
	if err != nil {
		return st, err.Error()
	}
	if len(data) > maxStyleBytes {
		return st, fmt.Sprintf("it is larger than %d KiB", maxStyleBytes>>10)
	}
	fields, body, err := agentdef.Split(string(data))
	if err != nil {
		fields, body = nil, string(data)
	}
	st.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	for _, f := range fields {
		switch f.Key {
		case "name":
			if v := strings.TrimSpace(f.Scalar); v != "" {
				st.Name = v
			}
		case "description":
			st.Description = strings.TrimSpace(f.Scalar)
		case "keep-coding-instructions":
			st.KeepCoding = strings.EqualFold(strings.TrimSpace(f.Scalar), "true")
		}
	}
	st.Text = strings.TrimSpace(body)
	switch {
	case st.Text == "":
		return st, "it holds no instructions"
	case strings.EqualFold(st.Name, defaultStyle):
		return st, "\"default\" is the name of no style at all"
	case !printableName(st.Name):
		return st, "its name holds a control character"
	}
	return st, ""
}

// printableName reports whether a name holds no control character.
func printableName(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// findStyle finds a style by its name: exactly, or with exact set off,
// in any case (as /output-style takes it).
func findStyle(styles []outputStyle, name string, exact bool) (outputStyle, bool) {
	for _, s := range styles {
		if s.Name == name || !exact && strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return outputStyle{}, false
}

// currentStyle is the style in force, or nil for the default (and for a
// name no style has, which the session said when it started).
func (l *live) currentStyle() *prompt.Style {
	l.styleMu.Lock()
	defer l.styleMu.Unlock()
	if l.styleName == "" || l.styleName == defaultStyle {
		return nil
	}
	if st, ok := findStyle(l.styles, l.styleName, true); ok {
		if st.project {
			// A repository's text, as its GNOSH.md is: a <gnosh> or <mem>
			// block in it is not read as gnosh's own (the status line
			// review of 2026-11-19).
			st.Text = prompt.Untrusted(st.Text)
		}
		return &st.Style
	}
	return nil
}

// styleNow is the name of the style in force, as the status line command is
// told it.
func (l *live) styleNow() string {
	if st := l.currentStyle(); st != nil {
		return st.Name
	}
	return defaultStyle
}

// loadStyles reads the styles at the start of a session and takes the one
// the configuration names. A name no style has is said, and the default is
// used.
func (l *live) loadStyles() {
	styles, problems := loadOutputStyles(l.texts, l.cfg, l.st.trusted, l.defRoots())
	for _, p := range problems {
		l.notice(agent.LevelWarning, "output-style", p+".")
	}
	name := strings.TrimSpace(l.cfg.Options.OutputStyle.V)
	l.styleMu.Lock()
	l.styles, l.styleName = styles, name
	l.styleMu.Unlock()
	if name != "" && name != defaultStyle {
		if _, ok := findStyle(styles, name, true); !ok {
			l.notice(agent.LevelWarning, "output-style", fmt.Sprintf("There is no output style named %q (set in %s), so the default is used. /output-style lists the styles there are.", name, l.cfg.Options.OutputStyle.Origin.String()))
		}
	}
}

// setStyle puts a style in force from the next request on: the system
// prompt is built again with it.
func (l *live) setStyle(name string) error {
	l.styleMu.Lock()
	l.styleName = name
	l.styleMu.Unlock()
	parts, err := l.systemPrompt()
	if err != nil {
		return err
	}
	l.putSystem(parts)
	return nil
}

// putSystem makes parts the system prompt, from the next request on.
func (l *live) putSystem(parts []string) {
	l.systemMu.Lock()
	l.system = parts
	l.systemMu.Unlock()
	if l.agent != nil {
		l.agent.SetSystem(parts)
	}
}

// systemNow is the system prompt in force.
func (l *live) systemNow() []string {
	l.systemMu.Lock()
	defer l.systemMu.Unlock()
	return l.system
}

// slashOutputStyle is /output-style: with nothing after it, the styles
// there are, the one in force marked; with a name, that style from the
// next message on, kept in the project's local settings file as Claude
// Code keeps it.
func (l *live) slashOutputStyle(ctx context.Context, c *SlashContext) error {
	l.styleMu.Lock()
	styles := append([]outputStyle(nil), l.styles...)
	l.styleMu.Unlock()
	now := l.styleNow()
	want := strings.TrimSpace(c.Args)
	if want == "" {
		c.Say("Output styles (/output-style <name> switches; it is kept in %s):", l.tilde(l.cfg.Paths.LocalSettings))
		mark := func(name string) string {
			if name == now {
				return "*"
			}
			return " "
		}
		c.Say("%s %s: gnosh's own instructions alone", mark(defaultStyle), defaultStyle)
		for _, s := range styles {
			from := ""
			if s.source != "gnosh" {
				from = " (" + l.tilde(s.source) + ")"
			}
			c.Say("%s %s: %s%s", mark(s.Name), s.Name, firstOf(s.Description, "no description"), from)
		}
		return nil
	}
	name := defaultStyle
	if !strings.EqualFold(want, defaultStyle) {
		st, ok := findStyle(styles, want, false)
		if !ok {
			return fmt.Errorf("there is no output style named %q; /output-style lists the styles there are", want)
		}
		name = st.Name
	}
	err := l.editLocalSettings(func(doc map[string]json.RawMessage) error {
		opts := map[string]json.RawMessage{}
		if raw, ok := doc["options"]; ok {
			if err := json.Unmarshal(raw, &opts); err != nil {
				return fmt.Errorf("its \"options\" is not an object (%v)", err)
			}
		}
		// "default" is kept as a choice like any other: taken off, a
		// style the person's gnosh.json or /config names won the next
		// session (the settings-commands review of 2026-11-20).
		raw, _ := json.Marshal(name)
		opts["outputStyle"] = raw
		if len(opts) == 0 {
			delete(doc, "options")
			return nil
		}
		raw, err := json.Marshal(opts)
		doc["options"] = raw
		return err
	})
	if err != nil {
		return fmt.Errorf("the output style was not changed: %w", err)
	}
	if err := l.setStyle(name); err != nil {
		return err
	}
	c.Say("The output style is %s from your next message on (kept in %s).", name, l.tilde(l.cfg.Paths.LocalSettings))
	if !l.st.trusted {
		// A project's files count for nothing in a folder that is not
		// trusted, the one gnosh writes too.
		c.Say("This folder is not trusted, so the choice holds for this session only; trust it (gnosh trust) for it to hold in later ones.")
	}
	return nil
}
