package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/skill"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// /skill-doctor (DECISIONS 377), as Claude Code's: what each skill costs
// in the model's window and how often it ran, for the person to turn off
// the ones that cost and are not used. The uses are counted in the newest
// session files of every folder (and their sub-agents'): the model's
// Skill calls, and the skills the person typed.

// skillUse is how often a skill ran, and when last.
type skillUse struct {
	runs int
	last time.Time
}

// slashSkillDoctor is /skill-doctor.
func (l *live) slashSkillDoctor(ctx context.Context, c *SlashContext) error {
	if strings.TrimSpace(c.Args) != "" {
		return errors.New("/skill-doctor takes nothing after it")
	}
	all := l.skills().All()
	if len(all) == 0 {
		c.Say("There are no skills to look at.")
		return nil
	}
	cost, _, _ := l.skillCosts()
	uses, sessions := l.skillUses(ctx)
	// Those never run, the costliest first: the ones to look at; then the
	// rest, the most run first.
	slices.SortStableFunc(all, func(a, b skill.Skill) int {
		ua, ub := uses[a.Name].runs, uses[b.Name].runs
		switch {
		case (ua == 0) != (ub == 0):
			if ua == 0 {
				return -1
			}
			return 1
		case ua == 0:
			return cost[b.Name] - cost[a.Name]
		}
		return ub - ua
	})
	c.Say("Your skills: what each costs in the model's window, with the first prompt and after each summary, and how often it ran in your last %s (the model's Skill calls and the ones you typed):", lastSessions(sessions))
	idle, idleCost := 0, 0
	for _, sk := range all {
		n, u := cost[sk.Name], uses[sk.Name]
		where := "not in the model's list"
		if n > 0 {
			where = "about " + countWords(n, "token", "tokens")
		}
		ran := "never ran"
		if u.runs > 0 {
			ran = "ran " + countWords(u.runs, "time", "times") + ", last on " + u.last.Local().Format("Jan 2")
		}
		line := fmt.Sprintf("  /%s · %s · %s · %s", sk.Name, firstOf(sk.Scope, "gnosh's"), where, ran)
		if u.runs == 0 && n > 0 {
			idle++
			idleCost += n
			line += ": /skills " + sk.Name + " off takes it out, name-only keeps only its name"
		}
		c.Say("%s", visible(line))
	}
	if idle > 0 {
		verb := "take"
		if idle == 1 {
			verb = "takes"
		}
		c.Say("%s never ran and %s about %s of the model's window.", countWords(idle, "skill in the model's list", "skills in the model's list"), verb, countWords(idleCost, "token", "tokens"))
	}
	return nil
}

// skillUses counts the runs of each skill in the newest session files of
// every folder: Skill calls, and the skills a typed line ran.
func (l *live) skillUses(ctx context.Context) (map[string]skillUse, int) {
	files, sessions := recentSessionFiles(l.store.ProjectDirs())
	uses := map[string]skillUse{}
	note := func(name string, at time.Time) {
		u := uses[name]
		u.runs++
		if at.After(u.last) {
			u.last = at
		}
		uses[name] = u
	}
	for _, f := range files {
		for e, err := range transcript.ReadFile(ctx, f, nil) {
			if err != nil || ctx.Err() != nil {
				break
			}
			switch e.Type {
			case transcript.TypeAssistant:
				for _, b := range e.Message.ToolUses() {
					if b.Name != skillToolName {
						continue
					}
					var in skillInput
					if json.Unmarshal(b.Input, &in) == nil && in.Skill != "" {
						note(strings.TrimPrefix(strings.TrimSpace(in.Skill), "/"), e.Timestamp)
					}
				}
			case transcript.TypeUser:
				for _, name := range typedSkills(e.Message.Text()) {
					note(name, e.Timestamp)
				}
			}
		}
	}
	return uses, sessions
}

// typedSkills are the skills a line the person typed ran, as the words
// gnosh sends ahead of their text name them ("The user ran the skill
// "deploy" by typing: ...").
func typedSkills(text string) []string {
	for _, lead := range []string{"The user ran the skills ", "The user ran the skill "} {
		i := strings.Index(text, lead)
		if i < 0 {
			continue
		}
		rest := text[i+len(lead):]
		var names []string
		for {
			q, err := strconv.QuotedPrefix(rest)
			if err != nil {
				break
			}
			if name, err := strconv.Unquote(q); err == nil && name != "" {
				names = append(names, name)
			}
			rest = strings.TrimPrefix(rest[len(q):], ", ")
		}
		return names
	}
	return nil
}
