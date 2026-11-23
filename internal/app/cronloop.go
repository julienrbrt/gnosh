package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/cron"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file is the rest of the session's scheduled prompts (crontool.go,
// DECISIONS 81): the self-paced /loop, and the tasks kept in the
// session's file so that --resume and --continue bring them back.
//
// /loop <prompt> with no interval runs the prompt now and lets the model
// choose when it runs again, as Claude Code's does: at the end of each run
// it calls ScheduleWakeup with a delay from one minute to an hour, or with
// stop when the task is done. A run that schedules nothing gets one more,
// 20 minutes on; when that one schedules nothing either, the loop ends.
// /noloop ends it too. The next run is a one-shot task CronList shows.
//
// The tasks are written to the session's file as they change (a meta
// entry, metaCrons, holding them all). A session opened again brings back
// the recurring ones younger than 7 days, from their next time on, and
// the one-shot ones whose time has not come; a self-paced loop is not
// brought back, as in Claude Code.

const (
	scheduleWakeupName = "ScheduleWakeup"
	// metaCrons is the key of the meta entries that hold the session's
	// tasks, as JSON; the last one is in force.
	metaCrons = "crons"
	// loopFallback is when a self-paced loop runs again after a run that
	// scheduled nothing.
	loopFallback = 20 * time.Minute
)

// selfLoop is a self-paced /loop: its prompt, and whether the run that is
// pending is the fallback, after a run that scheduled nothing.
type selfLoop struct {
	prompt   string
	fallback bool
	// run says one of the loop's runs is under way: only its end is
	// followed (loopTurnEnded), not that of a turn the person started.
	run bool
	// scheduled is the next run the turn under way scheduled, if it did:
	// a turn schedules once (box 1, 18 November: Qwen called
	// ScheduleWakeup and checked the clock every few seconds, each call
	// moving the run a minute on, so that it never came).
	scheduled time.Time
}

// loopNote is what the model is told as a self-paced /loop starts, and
// loopRunNote with each run after.
const (
	loopNote    = "This prompt runs as a self-paced /loop: it runs again when you say. When this run's work is done, call ScheduleWakeup with delaySeconds from 60 to 3600 (short while something is about to change, long when nothing is pending) and a one-line reason, or with stop true when the task is finished and the loop should end."
	loopRunNote = "This is a run of the self-paced /loop. When its work is done, schedule the next run with ScheduleWakeup, or end the loop with stop true."
)

// startSelfLoop is /loop <prompt> with no interval.
func (l *live) startSelfLoop(c *SlashContext, text string) error {
	if text == "" {
		return fmt.Errorf("/loop %s has no prompt", c.Args)
	}
	l.loopMu.Lock()
	l.self = &selfLoop{prompt: text, run: true}
	l.loopMu.Unlock()
	l.dropWakes()
	_ = l.agent.Tell(agent.Note{Source: "loop", Text: prompt.Wrap(loopNote)})
	c.Say("The prompt runs now, and again when the model says: it schedules each next run with ScheduleWakeup, from a minute to an hour on, and ends the loop when the task is done. /noloop ends it.")
	return c.Send(agent.Prompt{Text: text, Source: "loop"})
}

// currentLoop is the self-paced loop, or nil.
func (l *live) currentLoop() *selfLoop {
	l.loopMu.Lock()
	defer l.loopMu.Unlock()
	return l.self
}

// endSelfLoop ends the self-paced loop, and its pending run, saying why.
// It reports whether one was running.
func (l *live) endSelfLoop(why string) bool {
	l.loopMu.Lock()
	on := l.self != nil
	l.self = nil
	l.loopMu.Unlock()
	if !on {
		return false
	}
	l.dropWakes()
	l.notice(agent.LevelInfo, "loop", "The self-paced /loop is over: "+why+".")
	return true
}

// dropWakes takes the self-paced loop's pending run off the books.
func (l *live) dropWakes() {
	b := &l.crons
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tasks = slices.DeleteFunc(b.tasks, func(t *cronTask) bool { return t.wake })
}

// wakeAt puts the self-paced loop's next run on the books, at at, in place
// of any pending one.
func (l *live) wakeAt(at time.Time, text string) *cronTask {
	t := &cronTask{id: newTaskID(), prompt: text, created: l.p.now(), next: at, loop: true, wake: true}
	b := &l.crons
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tasks = slices.DeleteFunc(b.tasks, func(t *cronTask) bool { return t.wake })
	b.tasks = append(b.tasks, t)
	return t
}

// loopRunStarts notes that one of the self-paced loop's runs goes out now.
func (l *live) loopRunStarts() {
	l.loopMu.Lock()
	defer l.loopMu.Unlock()
	if l.self != nil {
		l.self.run = true
	}
}

// loopEndsNote is what to tell the person when the session starts again
// with a self-paced loop running, which the new start does not take up:
// "" when none runs.
func (l *live) loopEndsNote() string {
	if l.currentLoop() == nil {
		return ""
	}
	return " The self-paced /loop does not go on in the new start: /loop starts it again."
}

// hasWake reports whether the self-paced loop has a run pending.
func (l *live) hasWake() bool {
	b := &l.crons
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.ContainsFunc(b.tasks, func(t *cronTask) bool { return t.wake })
}

// loopTurnEnded follows a self-paced loop as a turn ends: a run that
// scheduled nothing gets one more, 20 minutes on, and a loop whose
// fallback run scheduled nothing either is over.
func (l *live) loopTurnEnded() {
	self := l.currentLoop()
	if self == nil {
		return
	}
	l.loopMu.Lock()
	run := self.run
	self.run, self.scheduled = false, time.Time{}
	l.loopMu.Unlock()
	if !run || l.hasWake() {
		return
	}
	l.loopMu.Lock()
	fallback := self.fallback
	self.fallback = true
	l.loopMu.Unlock()
	if fallback {
		l.endSelfLoop("its last run scheduled no next one")
		return
	}
	t := l.wakeAt(l.p.now().Add(loopFallback), self.prompt)
	l.notice(agent.LevelInfo, "loop", "The /loop run scheduled no next one: it runs once more at "+whenText(t.next, l.p.now())+", and the loop ends if that run schedules none either.")
}

// wakeTool is ScheduleWakeup.
type wakeTool struct{ l *live }

func (t *wakeTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name: scheduleWakeupName,
		Description: "In a self-paced /loop, schedules the loop's next run, once this run's work is done: delaySeconds from 60 to 3600, short while something is about to change and long when nothing is pending, with a one-line reason. " +
			"With stop true it ends the loop instead, when the task is finished. Outside a self-paced /loop it does nothing.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"delaySeconds":{"type":"number","description":"Seconds until the next run, from 60 to 3600."},"reason":{"type":"string","description":"Why that delay, in one line."},"prompt":{"type":"string","description":"The loop's prompt, as it was given; it may be left out."},"stop":{"type":"boolean","description":"End the loop: the task is done."}},"additionalProperties":false}`),
	}
}

func (t *wakeTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	return core.Action{Kind: core.ActionOther, Summary: "Schedule the loop's next run"}, nil
}

func (t *wakeTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	l := t.l
	var in struct {
		Delay  float64 `json:"delaySeconds"`
		Reason string  `json:"reason"`
		Prompt string  `json:"prompt"`
		Stop   bool    `json:"stop"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return core.ErrorResult("The input could not be read: " + err.Error()), nil
	}
	self := l.currentLoop()
	if self == nil {
		return core.ErrorResult("No self-paced /loop runs in this session, so nothing was scheduled. To send a prompt later, use CronCreate."), nil
	}
	reason := oneLine(strings.TrimSpace(in.Reason), 200)
	if in.Stop {
		l.endSelfLoop(firstOf(reason, "the model ended it"))
		return core.TextResult("The loop is over."), nil
	}
	if math.IsNaN(in.Delay) || in.Delay < 60 {
		in.Delay = 60
	}
	in.Delay = min(in.Delay, 3600)
	now := l.p.now()
	l.loopMu.Lock()
	already := self.scheduled
	l.loopMu.Unlock()
	if !already.IsZero() {
		return core.ErrorResult(fmt.Sprintf("The loop's next run is scheduled already, at %s, and was not moved: a run schedules the next one once. End this turn now, with no more tool calls; the next run starts by itself, and nothing needs to wait for it or watch the clock.", whenText(already, now))), nil
	}
	task := l.wakeAt(now.Add(time.Duration(in.Delay)*time.Second), self.prompt)
	l.loopMu.Lock()
	self.fallback, self.scheduled = false, task.next
	l.loopMu.Unlock()
	when, wait := whenText(task.next, now), waitWords(task.next.Sub(now))
	text := fmt.Sprintf("The loop's next run is at %s, in %s. This turn ends here: the run starts by itself then.", when, wait)
	// The turn ends with the round, as the run's work is done when it
	// schedules the next: a model that went on would only wait.
	res := core.TextResult(text)
	res.Meta = map[string]any{agent.MetaHalt: "the loop's next run is at " + when, agent.MetaPlanned: true, "nextRun": when, "wait": wait}
	if reason != "" {
		// The model's reason, under the call (where a notice of its own
		// said the next run's time again, seen on the devbox's screen).
		res.Notes = []string{reason}
	}
	return res, nil
}

// waitWords says how long until a run, to the minute: "10 minutes",
// "1 hour".
func waitWords(d time.Duration) string {
	return everyWords(max(d.Round(time.Minute), time.Minute))
}

// savedTask is a task as the session's file keeps it.
type savedTask struct {
	ID        string    `json:"id"`
	Cron      string    `json:"cron"`
	Prompt    string    `json:"prompt"`
	Recurring bool      `json:"recurring"`
	Created   time.Time `json:"created"`
	Loop      bool      `json:"loop,omitempty"`
}

// saveCronsLocked writes the session's tasks, but a self-paced loop's
// next run, to its file. It is called with the book's lock held.
func (l *live) saveCronsLocked() {
	if l.ts == nil {
		return
	}
	saved := []savedTask{}
	for _, t := range l.crons.tasks {
		if t.wake || t.sched == nil {
			continue
		}
		saved = append(saved, savedTask{ID: t.id, Cron: t.sched.String(), Prompt: t.prompt, Recurring: t.recurring, Created: t.created, Loop: t.loop})
	}
	data, _ := json.Marshal(saved)
	if _, err := l.ts.Append(transcript.MetaEntry(metaCrons, string(data))); err != nil {
		l.flog.logf("cron", "the scheduled tasks could not be kept in the session's file: %v", err)
	}
}

// restoreCrons brings back the tasks the session's file keeps: recurring
// ones younger than 7 days, from their next time on, and one-shot ones
// whose time has not come. It says what came back.
func (l *live) restoreCrons() {
	if l.ts == nil {
		return
	}
	entries := l.ts.Entries()
	var saved []savedTask
	found := false
	for i := len(entries) - 1; i >= 0; i-- {
		if e := entries[i]; e.Type == transcript.TypeMeta && e.Key == metaCrons {
			found = json.Unmarshal([]byte(e.Value), &saved) == nil
			break
		}
	}
	if !found || len(saved) == 0 {
		return
	}
	now := l.p.now()
	var back []*cronTask
	for _, s := range saved {
		sched, err := cron.Parse(s.Cron)
		if err != nil || strings.TrimSpace(s.Prompt) == "" {
			continue
		}
		var next time.Time
		if s.Recurring {
			if now.Sub(s.Created) >= cronExpiry {
				continue
			}
			if next, err = sched.Next(now); err != nil {
				continue
			}
		} else {
			// The one time it was to run.
			if next, err = sched.Next(s.Created); err != nil || !next.After(now) {
				continue
			}
		}
		back = append(back, &cronTask{id: s.ID, sched: sched, prompt: s.Prompt, recurring: s.Recurring, created: s.Created, next: next, loop: s.Loop})
	}
	b := &l.crons
	b.mu.Lock()
	b.tasks = append(b.tasks, back...)
	if len(b.tasks) > maxCronTasks {
		b.tasks = b.tasks[:maxCronTasks]
	}
	if len(back) != len(saved) {
		l.saveCronsLocked()
	}
	b.mu.Unlock()
	if len(back) > 0 {
		l.notice(agent.LevelInfo, "cron", countWords(len(back), "scheduled task is", "scheduled tasks are")+" back from the session's file. CronList lists them.")
	}
}
