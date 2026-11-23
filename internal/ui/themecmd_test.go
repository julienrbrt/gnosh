package ui

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// The theme option: auto follows what the terminal said of its background,
// dark and light say it whatever the terminal said, and ansi takes the
// terminal's own sixteen colours.
func TestThemeFor(t *testing.T) {
	darkReply, lightReply := DefaultPalette(text.TrueColor).Reply, DefaultPalette(text.TrueColor).OnLight(text.TrueColor).Reply
	for _, c := range []struct {
		name  string
		light bool
		want  text.Color
	}{
		{"auto", false, darkReply},
		{"auto", true, lightReply},
		{"", true, lightReply},
		{"dark", true, darkReply},
		{"light", false, lightReply},
		{"ansi", false, text.BrightGreen},
		{"ansi", true, text.Green},
	} {
		if got := themeFor(c.name, text.TrueColor, c.light).Palette.Reply; got != c.want {
			t.Errorf("themeFor(%q, light %v): the reply is %v, want %v", c.name, c.light, got, c.want)
		}
	}
	if got := themeFor("ansi", text.NoColor, false).Palette; got != DefaultPalette(text.NoColor) {
		t.Errorf("ansi without colours: %+v", got)
	}
}

// /theme and /config theme= put the colours in force at once, and keep the
// colour /color gave the prompt mark.
func TestThemeCommandChangesColours(t *testing.T) {
	g := newRig(t, 100, 24, text.TrueColor)
	g.s.themed, g.s.profile, g.s.light = true, text.TrueColor, false
	g.s.setTheme("auto")
	if g.s.th.Palette.Reply != DefaultPalette(text.TrueColor).Reply {
		t.Fatalf("auto on a dark terminal gave %+v", g.s.th.Palette.Reply)
	}
	g.s.ran("/theme light", app.CommandResult{Name: "theme", Handled: true}, nil)
	if g.s.th.Palette.Reply != DefaultPalette(text.TrueColor).OnLight(text.TrueColor).Reply {
		t.Errorf("/theme light gave the reply %v", g.s.th.Palette.Reply)
	}
	g.s.ran("/config theme=ansi", app.CommandResult{Name: "config", Handled: true}, nil)
	if g.s.th.Palette.Reply != text.BrightGreen {
		t.Errorf("/config theme=ansi gave the reply %v", g.s.th.Palette.Reply)
	}
	// A theme a test gave the interface is kept.
	g.s.themed = false
	g.s.setTheme("light")
	if g.s.th.Palette.Reply != text.BrightGreen {
		t.Errorf("a given theme was changed")
	}
}

// A thought the server cut at its budget says so on its folded line; one
// that ended of itself does not.
func TestThoughtCutAtItsBudget(t *testing.T) {
	g := newRig(t, 120, 24, text.NoColor)
	note := "\n\n<gnosh>Thinking time is up. Go on with what is worked out so far.</gnosh>\n"
	g.s.info.ThinkingBudget, g.s.info.BudgetNote = 4096, note
	g.ev(busy(), request(1), thought("Let me work it out."+note), said("4001"))
	g.committedHas("thought for", "up to its budget of 4096 tokens")
	g.ev(request(2), thought("Short."), said("done"))
	if n := strings.Count(g.scroll(), "up to its budget"); n != 1 {
		t.Errorf("the budget was said %d times, want once (for the thought it cut)", n)
	}
}
