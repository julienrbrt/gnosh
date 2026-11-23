package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/cron"
	"github.com/gnosh-ai/gnosh/internal/prompt"
)

// This file is a session's scheduled prompts, as Claude Code has them (its
// scheduled-tasks page; spec/DECISIONS.md 81): CronCreate schedules a
// prompt on a five-field cron schedule in local time, recurring or once;
// CronList lists what is scheduled and CronDelete cancels it; and /loop
// with an interval (/loop 5m check the deploy) schedules its prompt every
// so often and sends it at once. A scheduled prompt goes in between turns,
// never into one: when it comes due while the session works, it waits for
// the session to be idle, and goes once however many of its times passed
// meanwhile. A recurring task goes 7 days after it was made, after one
// last run; a one-shot task goes when it has run. A session holds 50 at
// most. They are an attended session's: a run ends with its prompt.
// CLAUDE_CODE_DISABLE_CRON=1 switches them off.
//
// Where gnosh differs from Claude Code: no jitter (it spreads Anthropic's
// fleet's requests out, and a local model server has none), and the tasks
// are not yet brought back by --resume.

const (
	cronCreateName = "CronCreate"
	cronDeleteName = "CronDelete"
	cronListName   = "CronList"
	// maxCronTasks is how many tasks a session may hold at once, and
	// cronExpiry how long a recurring one lasts.
	maxCronTasks = 50
	cronExpiry   = 7 * 24 * time.Hour
)

// cronTask is one scheduled prompt.
type cronTask struct {
	id        string
	sched     *cron.Schedule
	prompt    string
	recurring bool
	created   time.Time
	next      time.Time
	// loop says /loop made it, and wake that it is a self-paced loop's
	// next run (ScheduleWakeup): no schedule, once, at next.
	loop, wake bool
}

// cronBook is the session's scheduled prompts.
type cronBook struct {
	mu    sync.Mutex
	tasks []*cronTask
}

// cronOff says why the session has no scheduled prompts: "" when it has.
func (l *live) cronOff() string {
	switch {
	case !l.interactive:
		return "Scheduled prompts are an attended session's: a run ends with its prompt."
	case l.envOn("CLAUDE_CODE_DISABLE_CRON"):
		return "Scheduled prompts are off (CLAUDE_CODE_DISABLE_CRON)."
	}
	return ""
}

// schedule adds a task: the schedule expr, the prompt it sends, whether it
// recurs. It returns the task, or an error in words for the model.
func (l *live) schedule(expr, prompt string, recurring, loop bool) (*cronTask, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("the prompt is empty: a scheduled task sends a prompt")
	}
	sched, err := cron.Parse(expr)
	if err != nil {
		return nil, err
	}
	now := l.p.now()
	next, err := sched.Next(now)
	if err != nil {
		return nil, fmt.Errorf("%q never comes due", sched)
	}
	b := &l.crons
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.tasks) >= maxCronTasks {
		return nil, fmt.Errorf("the session holds %d scheduled tasks, the most it may: delete one first", maxCronTasks)
	}
	t := &cronTask{id: newTaskID(), sched: sched, prompt: prompt, recurring: recurring, created: now, next: next, loop: loop}
	b.tasks = append(b.tasks, t)
	l.saveCronsLocked()
	return t, nil
}

// newTaskID is a task's id: 8 hex digits.
func newTaskID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// unschedule removes the task id, and reports whether there was one, and
// whether it was the self-paced loop's next run.
func (l *live) unschedule(id string) (found, wake bool) {
	b := &l.crons
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.tasks)
	b.tasks = slices.DeleteFunc(b.tasks, func(t *cronTask) bool {
		if t.id == id {
			wake = wake || t.wake
			return true
		}
		return false
	})
	if len(b.tasks) == n {
		return false, false
	}
	l.saveCronsLocked()
	return true, wake
}

// scheduled returns the session's tasks, the next to run first.
func (l *live) scheduled() []cronTask {
	b := &l.crons
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]cronTask, 0, len(b.tasks))
	for _, t := range b.tasks {
		out = append(out, *t)
	}
	slices.SortStableFunc(out, func(a, b cronTask) int { return a.next.Compare(b.next) })
	return out
}

// runCron sends the session's scheduled prompts as they come due, between
// turns, until ctx ends. It looks once a second.
func (l *live) runCron(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		l.fireDue()
	}
}

// fireDue sends the prompt of the task most overdue, when the session is
// idle with nothing of the person's waiting and no command of theirs
// running: one at a time, so that each is a turn of its own. A recurring task's next run is the first after now
// (none is made up for times missed); one past its 7 days, and a one-shot
// task, goes after this run.
func (l *live) fireDue() {
	if l.agent == nil || l.closing.Load() || l.slash != nil && l.slash.running.Load() > 0 {
		return
	}
	if st := l.agent.State(); st.Busy || st.Queued > 0 {
		return
	}
	now := l.p.now()
	b := &l.crons
	b.mu.Lock()
	var due *cronTask
	for _, t := range b.tasks {
		if !t.next.After(now) && (due == nil || t.next.Before(due.next)) {
			due = t
		}
	}
	if due == nil {
		b.mu.Unlock()
		return
	}
	last := !due.recurring || due.sched == nil || now.Sub(due.created) >= cronExpiry
	if !last {
		next, err := due.sched.Next(now)
		if err != nil {
			last = true
		} else {
			due.next = next
		}
	}
	if last {
		b.tasks = slices.DeleteFunc(b.tasks, func(t *cronTask) bool { return t == due })
		l.saveCronsLocked()
	}
	task := *due
	b.mu.Unlock()
	why := "Scheduled task " + task.id + " runs"
	switch {
	case task.wake:
		why = "The self-paced /loop runs again"
		l.loopRunStarts()
		_ = l.agent.Tell(agent.Note{Source: "loop", Text: prompt.Wrap(loopRunNote)})
	case !task.recurring:
		why += ", once"
	case last:
		why += " for the last time: its 7 days are over"
	}
	l.notice(agent.LevelInfo, "cron", why+": "+oneLine(task.prompt, 100))
	if _, err := l.agent.Submit(agent.Prompt{Text: task.prompt, Source: "cron"}); err != nil {
		l.flog.logf("cron", "the prompt of task %s could not be sent: %v", task.id, err)
	}
}

// whenText is a time as a person reads it here: the hour and minute, with
// the date when it is not today.
func whenText(t, now time.Time) string {
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Mon Jan 2 15:04")
}

// cronCreateTool is CronCreate.
type cronCreateTool struct{ l *live }

func (t *cronCreateTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name: cronCreateName,
		Description: "Schedules a prompt to be sent to you later in this session, between turns: on a recurring schedule, or once (a reminder). " +
			"The schedule is five cron fields in local time: minute hour day-of-month month day-of-week (\"*/5 * * * *\" every 5 minutes, \"0 9 * * 1-5\" weekdays at 9, \"30 14 18 11 *\" on November 18 at 14:30). " +
			"For \"remind me at X\" or \"in N minutes\", pin the minute, hour, day and month and set recurring false. " +
			"A recurring task expires after 7 days; say so when you schedule one. To watch a command's output as it comes, use Monitor instead. The result gives the task's id, for CronDelete.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"cron":{"type":"string","description":"Five cron fields, local time."},"prompt":{"type":"string","description":"The prompt to send each time it comes due."},"recurring":{"type":"boolean","description":"true (the default): on every match until deleted or 7 days pass. false: once, at the next match, then it is deleted."},"durable":{"type":"boolean","description":"Ignored: tasks are this session's alone."}},"required":["cron","prompt"],"additionalProperties":false}`),
	}
}

func (t *cronCreateTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	return core.Action{Kind: core.ActionOther, Summary: "Schedule a prompt"}, nil
}

func (t *cronCreateTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	l := t.l
	var in struct {
		Cron      string `json:"cron"`
		Prompt    string `json:"prompt"`
		Recurring *bool  `json:"recurring"`
		Durable   bool   `json:"durable"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return core.ErrorResult("The input could not be read: " + err.Error()), nil
	}
	if why := l.cronOff(); why != "" {
		return core.ErrorResult(why), nil
	}
	recurring := in.Recurring == nil || *in.Recurring
	task, err := l.schedule(in.Cron, in.Prompt, recurring, false)
	if err != nil {
		return core.ErrorResult("Nothing was scheduled: " + err.Error() + "."), nil
	}
	now := l.p.now()
	text := fmt.Sprintf("Scheduled task %s: %q, next run %s.", task.id, task.sched, whenText(task.next, now))
	if recurring {
		text += " It runs between turns until deleted, and expires after 7 days (" + whenText(task.created.Add(cronExpiry), now) + "), after one last run."
	} else {
		text += " It runs once, then is deleted."
	}
	return core.TextResult(text + " CronDelete " + task.id + " cancels it."), nil
}

// cronListTool is CronList.
type cronListTool struct{ l *live }

func (t *cronListTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        cronListName,
		Description: "Lists the prompts scheduled in this session (CronCreate, /loop): each task's id, schedule, next run and prompt.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func (t *cronListTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	return core.Action{Kind: core.ActionOther, Summary: "List the scheduled prompts"}, nil
}

func (t *cronListTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	return core.TextResult(t.l.scheduleList()), nil
}

// scheduleList is the session's tasks in words, one line each.
func (l *live) scheduleList() string {
	tasks := l.scheduled()
	if len(tasks) == 0 {
		return "No prompt is scheduled in this session."
	}
	now := l.p.now()
	var b strings.Builder
	fmt.Fprintf(&b, "%d scheduled:", len(tasks))
	for _, t := range tasks {
		if t.wake {
			fmt.Fprintf(&b, "\n%s  the self-paced /loop's next run, at %s: %s", t.id, whenText(t.next, now), oneLine(t.prompt, 100))
			continue
		}
		how := "recurring"
		if !t.recurring {
			how = "once"
		}
		fmt.Fprintf(&b, "\n%s  %q  %s, next run %s: %s", t.id, t.sched, how, whenText(t.next, now), oneLine(t.prompt, 100))
	}
	return b.String()
}

// cronDeleteTool is CronDelete.
type cronDeleteTool struct{ l *live }

func (t *cronDeleteTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        cronDeleteName,
		Description: "Cancels a prompt scheduled in this session, by the id CronCreate or CronList gave.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
	}
}

func (t *cronDeleteTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	return core.Action{Kind: core.ActionOther, Summary: "Cancel a scheduled prompt"}, nil
}

func (t *cronDeleteTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return core.ErrorResult("The input could not be read: " + err.Error()), nil
	}
	id := strings.TrimSpace(in.ID)
	found, wake := t.l.unschedule(id)
	if !found {
		return core.ErrorResult(fmt.Sprintf("No task %q is scheduled. %s", id, t.l.scheduleList())), nil
	}
	if wake {
		// The self-paced loop's next run: cancelling it ends the loop.
		t.l.endSelfLoop("its next run was cancelled")
		return core.TextResult("Task " + id + ", the self-paced /loop's next run, is cancelled, and the loop is over."), nil
	}
	return core.TextResult("Task " + id + " is cancelled."), nil
}

// loopEvery reads /loop's interval, as Claude Code's /loop reads it: a
// first word like 5m, 30s, 2h or 1d, or a last clause like "every 20m" or
// "every 2 hours". It returns the interval and the prompt without it; ok
// is false when there is no interval.
func loopEvery(args string) (every time.Duration, prompt string, ok bool) {
	args = strings.TrimSpace(args)
	first, rest, _ := strings.Cut(args, " ")
	if d, ok := intervalWord(first); ok {
		return d, strings.TrimSpace(rest), true
	}
	if m := everyClause.FindStringSubmatchIndex(args); m != nil {
		n, _ := strconv.Atoi(args[m[2]:m[3]])
		if d, ok := intervalWord(strconv.Itoa(n) + strings.ToLower(args[m[4]:m[4]+1])); ok {
			return d, strings.TrimSpace(args[:m[0]]), true
		}
	}
	return 0, args, false
}

// everyClause is a last clause "every <N><unit>" or "every <N> <unit>".
var everyClause = regexp.MustCompile(`(?i)\s+every\s+(\d+)\s*(s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?|d|days?)\s*$`)

// intervalWord reads a word like 5m: a number and one of s, m, h, d.
func intervalWord(w string) (time.Duration, bool) {
	if len(w) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(w[:len(w)-1])
	if err != nil || n < 1 || n > 100000 {
		return 0, false
	}
	unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour}[w[len(w)-1]]
	if unit == 0 {
		return 0, false
	}
	return time.Duration(n) * unit, true
}

// slashLoopEvery is /loop with an interval and a prompt: the prompt is
// scheduled every so often (CronCreate's way, with the interval rounded
// to a step cron can take) and sent at once.
func (l *live) slashLoopEvery(c *SlashContext) error {
	if why := l.cronOff(); why != "" {
		return errors.New(why + " /loop with nothing after it keeps the session working on its task")
	}
	every, prompt, ok := loopEvery(c.Args)
	switch {
	case !ok:
		return l.startSelfLoop(c, prompt)
	case prompt == "":
		return fmt.Errorf("/loop %s has no prompt: /loop 5m <prompt> sends the prompt every 5 minutes", c.Args)
	}
	expr, step, rounded := cron.Every(every)
	task, err := l.schedule(expr, prompt, true, true)
	if err != nil {
		return err
	}
	how := "every " + everyWords(step)
	if rounded {
		how += " (" + every.String() + " rounded to a step cron takes)"
	}
	c.Say("Scheduled task %s: %q %s, %q, between turns, from %s; it expires after 7 days. CronDelete %s, or asking to cancel it, stops it.",
		task.id, oneLine(prompt, 80), how, expr, whenText(task.next, l.p.now()), task.id)
	return c.Send(agent.Prompt{Text: prompt, Source: "loop"})
}

// everyWords says an interval cron.Every made: "5 minutes", "2 hours",
// "1 day".
func everyWords(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return countWords(int(d/(24*time.Hour)), "day", "days")
	case d%time.Hour == 0:
		return countWords(int(d/time.Hour), "hour", "hours")
	}
	return countWords(int(d/time.Minute), "minute", "minutes")
}
