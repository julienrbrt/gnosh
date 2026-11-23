package app

import (
	"fmt"
	"unicode/utf8"
)

// This file is what a Stop or SubagentStop hook is told of the session's
// work that may still wake it, Claude Code's background_tasks and
// session_crons (its hooks reference, "Stop input"): what runs in the
// background, and the prompts the session has scheduled, so that a hook
// can tell a session that is done from one that waits on work. Both lists
// are the session's, for a sub-agent's hook too, and are empty, never left
// out, when nothing runs or waits.

// stopListMost is how many characters of a description, a command or a
// prompt a hook is given; what is cut is said, as "… [+N chars]".
const stopListMost = 1000

// stopTask is an entry of background_tasks.
type stopTask struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
}

// stopCron is an entry of session_crons.
type stopCron struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	Recurring bool   `json:"recurring"`
	Prompt    string `json:"prompt"`
}

// stopLists are the two lists, as the fields hooks.Input.Extra adds.
func (l *live) stopLists() map[string]any {
	tasks := []stopTask{}
	if l.shell != nil {
		for _, t := range l.shell.Tasks() {
			if !t.Running {
				continue
			}
			kind := "shell"
			if t.Monitor {
				kind = "monitor"
			}
			tasks = append(tasks, stopTask{ID: t.ID, Type: kind, Status: "running",
				Description: clipped(firstOf(t.Description, firstLine(t.Command))), Command: clipped(t.Command)})
		}
	}
	for _, b := range l.backgroundAgents() {
		tasks = append(tasks, stopTask{ID: b.id, Type: "subagent", Status: "running", Description: clipped(b.desc), AgentType: b.kind})
	}
	crons := []stopCron{}
	b := &l.crons
	b.mu.Lock()
	for _, t := range b.tasks {
		c := stopCron{ID: t.id, Recurring: t.recurring && !t.wake, Prompt: clipped(t.prompt)}
		if t.sched != nil {
			c.Schedule = t.sched.String()
		} else {
			// The self-paced loop's next run has a time, not a schedule:
			// the schedule given is the one that matches that minute.
			at := t.next.Local()
			c.Schedule = fmt.Sprintf("%d %d %d %d *", at.Minute(), at.Hour(), at.Day(), int(at.Month()))
		}
		crons = append(crons, c)
	}
	b.mu.Unlock()
	return map[string]any{"background_tasks": tasks, "session_crons": crons}
}

// clipped is s cut to stopListMost characters, with how many more there
// were said after it.
func clipped(s string) string {
	n := utf8.RuneCountInString(s)
	if n <= stopListMost {
		return s
	}
	return string([]rune(s)[:stopListMost]) + fmt.Sprintf("… [+%d chars]", n-stopListMost)
}
