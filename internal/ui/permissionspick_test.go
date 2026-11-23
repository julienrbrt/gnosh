package ui

import (
	"slices"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// /permissions with nothing after it lists the rules in force under three
// rows that add one: a rule typed into a row runs /permissions allow, ask
// or deny; a rule picked is taken out after a yes, as /permissions remove,
// and one from a file gnosh does not write is only named.
func TestPermissionsPicker(t *testing.T) {
	g := newRig(t, 140, 30, text.NoColor, func(c *controls) {
		c.rules = []app.PermissionRule{
			{List: "deny", Rule: "Bash(rm *)", Source: "/proj/.gnosh/settings.local.json", Removable: true},
			{List: "allow", Rule: "Read", Source: "/home/u/.config/gnosh/gnosh.json"},
		}
		c.info.Commands = append(c.info.Commands, "permissions")
	})
	ran := func() []string {
		g.c.mu.Lock()
		defer g.c.mu.Unlock()
		return slices.Clone(g.c.commands)
	}
	g.keys(`"/permissions" <Enter>`)
	g.liveHas("Permissions (manual mode)", "Add an allow rule", "Add an ask rule", "Add a deny rule", "deny  Bash(rm *)", "allow  Read", "Done")
	// The first row: a rule typed is added.
	g.keys(`<Enter> "Bash(go test *)" <Enter>`)
	// The fourth: the deny rule, taken out after a yes.
	g.keys(`"/permissions" <Enter> <Down> <Down> <Down> <Enter>`)
	g.liveHas("Take out the deny rule Bash(rm *)?", "Yes, take it out")
	g.keys(`<Enter>`)
	// The fifth: a rule in the person's own file is only named.
	g.keys(`"/permissions" <Enter> <Down> <Down> <Down> <Down> <Enter>`)
	g.liveHas("which gnosh does not write")
	if got := ran(); !slices.Equal(got, []string{"/permissions allow Bash(go test *)", "/permissions remove deny Bash(rm *)"}) {
		t.Fatalf("ran %q", got)
	}
}
