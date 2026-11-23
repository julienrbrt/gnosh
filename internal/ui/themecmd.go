package ui

import (
	"strings"

	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// The theme option and /theme, as Claude Code's: the interface's colours
// for the background the terminal says it has (auto), for a dark or a
// light one whatever it says (over ssh or in tmux a terminal often says
// nothing), or the terminal's own sixteen colours (ansi), which its owner
// chose. Bare /theme opens the picker of the option; /theme <name> and
// /config theme=<name> set it, and the colours change at once.

// themeWords are the lines that open the picker.
var themeWords = map[string]bool{"/theme": true}

// themeFor returns the interface's theme for the theme option's value,
// with the terminal's colours p and what it said of its background.
func themeFor(name string, p text.Profile, light bool) Theme {
	switch name {
	case "dark":
		light = false
	case "light":
		light = true
	case "ansi":
		if p != text.NoColor {
			p = text.ANSI16
		}
	}
	pal := DefaultPalette(p)
	if light {
		pal = pal.OnLight(p)
	}
	return NewTheme(p, pal)
}

// setTheme puts the theme option's value in force: "" is auto. The colour
// /color gave the prompt mark stays.
func (s *session) setTheme(name string) {
	if !s.themed {
		return
	}
	s.th = themeFor(strings.ToLower(strings.TrimSpace(name)), s.profile, s.light)
	if s.color != "" {
		s.th.tintPrompt(s.color)
	}
	s.ed.SetTheme(s.th.Editor)
	s.dirty = true
}

// themeSet returns the name /theme was given, when it was given one.
func themeSet(line string) (string, bool) {
	words := strings.Fields(line)
	if len(words) != 2 || !strings.EqualFold(words[0], "/theme") {
		return "", false
	}
	return words[1], true
}

// chooseTheme opens the picker of the theme option, as /config's opens on
// it. It reports false when the session has no options to choose from.
func (s *session) chooseTheme(line string) bool {
	cc, ok := s.c.(configChooser)
	if !ok {
		return false
	}
	for _, ch := range cc.ConfigChoices() {
		if ch.Key == "theme" {
			s.ed.AddHistory(editor.Draft{Text: line})
			s.changeOption(ch)
			return true
		}
	}
	return false
}
