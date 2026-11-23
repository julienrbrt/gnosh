package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// clock is a time a test moves on by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestLoopEvery(t *testing.T) {
	for _, c := range []struct {
		args   string
		every  time.Duration
		prompt string
		ok     bool
	}{
		{"5m check the deploy", 5 * time.Minute, "check the deploy", true},
		{"30s ping", 30 * time.Second, "ping", true},
		{"2h /review", 2 * time.Hour, "/review", true},
		{"1d say hi", 24 * time.Hour, "say hi", true},
		{"check the deploy every 20m", 20 * time.Minute, "check the deploy", true},
		{"run tests every 5 minutes", 5 * time.Minute, "run tests", true},
		{"look again every 2 hours", 2 * time.Hour, "look again", true},
		{"check every PR", 0, "check every PR", false},
		{"check the deploy", 0, "check the deploy", false},
		{"5m", 5 * time.Minute, "", true},
		{"0m nothing", 0, "0m nothing", false},
		{"5x nothing", 0, "5x nothing", false},
	} {
		every, prompt, ok := loopEvery(c.args)
		if every != c.every || prompt != c.prompt || ok != c.ok {
			t.Errorf("loopEvery(%q) = %v, %q, %v; want %v, %q, %v", c.args, every, prompt, ok, c.every, c.prompt, c.ok)
		}
	}
}

// CronCreate schedules a prompt; it is sent between turns once it comes
// due, and a one-shot task is gone after its run. /loop with an interval
// sends its prompt at once and again every so often; with no prompt, it
// says what it takes.
func TestScheduledPrompts(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "CronCreate", Args: `{"cron":"* * * * *","prompt":"say pong","recurring":false}`}}},
		apptest.Reply{Text: "Scheduled."},
		apptest.Reply{Text: "pong"},
		apptest.Reply{Text: "The build is fine."},
		apptest.Reply{Text: "Still fine."},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "CronList", Args: `{}`}}},
		apptest.Reply{Text: "Listed."},
	)
	clk := &clock{now: time.Date(2026, 11, 18, 10, 0, 30, 0, time.Local)}
	s := sitWith(t, w, func(o *Options) { o.Now = clk.Now })
	s.see("Type a prompt and Enter.")
	s.typeLine("remind me to say pong in a minute")
	s.see("Scheduled.")
	clk.add(time.Minute)
	s.see("Scheduled task ")
	s.see("pong")

	s.typeLine("/loop 5m")
	s.see("has no prompt")
	s.typeLine("/loop 5m check the build")
	s.see(`"*/5 * * * *"`)
	s.see("The build is fine.")
	clk.add(5 * time.Minute)
	s.see("Still fine.")
	s.typeLine("what is scheduled?")
	s.see("Listed.")
	s.typeLine("/exit")
	s.exit()

	reqs := w.Model.Requests()
	has(t, "the tool's result", string(reqs[1].Body), "Scheduled task ", "next run 10:01", "It runs once, then is deleted.")
	has(t, "the scheduled prompt", string(reqs[2].Body), "say pong")
	has(t, "the loop's first run", string(reqs[3].Body), "check the build")
	has(t, "the loop's next run", string(reqs[4].Body), "check the build")
	list := string(reqs[6].Body)
	has(t, "the list", list, "1 scheduled:", "*/5 * * * *", "recurring", "check the build")
	if strings.Contains(list, "once, next run") {
		t.Errorf("the one-shot task is still listed after its run:\n%s", list)
	}
}

// The scheduling tools are an attended session's: a run is not offered
// them, and CLAUDE_CODE_DISABLE_CRON=1 takes them away.
func TestScheduledPromptsOffered(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "ok"}, apptest.Reply{Text: "ok"})
	run(t, w, "-p", "hi").wantCode(t, 0)
	if body := string(w.Model.Requests()[0].Body); strings.Contains(body, `"CronCreate"`) {
		t.Errorf("a run was offered CronCreate")
	}
	s := sitWith(t, w, func(o *Options) { o.Environ = append(o.Environ, "CLAUDE_CODE_DISABLE_CRON=1") })
	s.see("Type a prompt and Enter.")
	s.typeLine("hi")
	s.see("ok")
	s.typeLine("/loop 5m check")
	s.see("Scheduled prompts are off (CLAUDE_CODE_DISABLE_CRON).")
	s.typeLine("/exit")
	s.exit()
	if body := string(w.Model.Requests()[1].Body); strings.Contains(body, `"CronCreate"`) {
		t.Errorf("CLAUDE_CODE_DISABLE_CRON=1 left CronCreate offered")
	}
}

// /loop <prompt> with no interval runs it now and again when the model
// says (ScheduleWakeup); a run that schedules nothing gets one more, 20
// minutes on; stop ends the loop.
func TestSelfPacedLoop(t *testing.T) {
	w := apptest.NewWorld(t,
		// The turn ends with the round that schedules the next run: a
		// second call in it does not run.
		apptest.Reply{Calls: []apptest.Call{
			{ID: "c1", Name: "ScheduleWakeup", Args: `{"delaySeconds":120,"reason":"waiting on CI"}`},
			{ID: "c3", Name: "ScheduleWakeup", Args: `{"delaySeconds":60,"reason":"sooner"}`},
		}},
		apptest.Reply{Text: "Checked again."},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "ScheduleWakeup", Args: `{"stop":true,"reason":"the build is green"}`}}},
		apptest.Reply{Text: "Done."},
	)
	clk := &clock{now: time.Date(2026, 11, 18, 10, 0, 30, 0, time.Local)}
	s := sitWith(t, w, func(o *Options) { o.Now = clk.Now })
	s.see("Type a prompt and Enter.")
	s.typeLine("/loop check the build")
	s.see("The prompt runs now, and again when the model says")
	s.see("  waiting on CI")
	s.see("-- done (the loop's next run is at 10:02)") // (planned: not "ended")
	clk.add(2 * time.Minute)
	s.see("The self-paced /loop runs again")
	s.see("Checked again.")
	s.see("The /loop run scheduled no next one: it runs once more at 10:22")
	clk.add(20 * time.Minute)
	s.see("The self-paced /loop is over: the build is green.")
	s.see("Done.")
	s.typeLine("/exit")
	s.exit()
	reqs := w.Model.Requests()
	has(t, "the loop's first run", string(reqs[0].Body), "check the build", "This prompt runs as a self-paced /loop")
	has(t, "the first schedule", toolResult(t, w, 1, "c1"), "The loop's next run is at 10:02, in 2 minutes. This turn ends here")
	has(t, "the second schedule", toolResult(t, w, 1, "c2"), "This call did not run: the turn was ended first (the loop's next run is at 10:02)")
	has(t, "the loop's second run", string(reqs[1].Body), "This is a run of the self-paced /loop")
}

// Tasks are kept in the session's file: -c brings back a recurring one,
// from its next time on, and it runs; one 7 days old does not come back.
func TestScheduledPromptsComeBack(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "Fine."},
		apptest.Reply{Text: "Still fine."},
		apptest.Reply{Text: "ok"},
	)
	clk := &clock{now: time.Date(2026, 11, 18, 10, 0, 30, 0, time.Local)}
	s := sitWith(t, w, func(o *Options) { o.Now = clk.Now })
	s.see("Type a prompt and Enter.")
	s.typeLine("/loop 5m check the build")
	s.see("Fine.")
	s.see("-- done in ")
	s.typeLine("/exit")
	s.exit()

	clk.add(time.Hour)
	s = sitWith(t, w, func(o *Options) { o.Now = clk.Now }, "-c")
	s.see("1 scheduled task is back from the session's file.")
	clk.add(5 * time.Minute)
	s.see("Still fine.")
	s.see("-- done in ")
	s.typeLine("/exit")
	s.exit()
	has(t, "the run after -c", string(w.Model.Requests()[1].Body), "check the build")

	clk.add(8 * 24 * time.Hour)
	s = sitWith(t, w, func(o *Options) { o.Now = clk.Now }, "-c")
	s.see("Type a prompt and Enter.")
	s.typeLine("hi")
	s.see("ok")
	s.typeLine("/exit")
	s.exit()
	if out := s.out.String(); strings.Contains(out, "back from the session's file") {
		t.Errorf("a task 8 days old came back:\n%s", out)
	}
}

// Only the end of one of the loop's own runs is followed: a turn the
// person starts while the loop waits does not count as a run that
// scheduled nothing.
func TestSelfPacedLoopIgnoresOtherTurns(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "ScheduleWakeup", Args: `{"delaySeconds":600,"reason":"nothing pending"}`}}},
		apptest.Reply{Text: "Four."},
	)
	clk := &clock{now: time.Date(2026, 11, 18, 10, 0, 30, 0, time.Local)}
	s := sitWith(t, w, func(o *Options) { o.Now = clk.Now })
	s.see("Type a prompt and Enter.")
	s.typeLine("/loop check the build")
	s.see("the loop's next run is at 10:10")
	s.typeLine("what is two and two?")
	s.see("Four.")
	s.see("-- done in ")
	s.typeLine("/noloop")
	s.see("The self-paced /loop is over: /noloop ended it.")
	s.typeLine("/exit")
	s.exit()
	if out := s.out.String(); strings.Contains(out, "scheduled no next one") {
		t.Errorf("a turn of the person's was taken for a run of the loop:\n%s", out)
	}
}

// A scheduled prompt waits while a command of the person's runs: one that
// came due under /cd's trust question started a turn the move then had to
// refuse (the cd review of 2026-11-19), and one between the summary
// "/rewind N summarize" waits for and its rewind would have the rewind
// refused.
func TestScheduledPromptWaitsForACommand(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "CronCreate", Args: `{"cron":"* * * * *","prompt":"say pong","recurring":false}`}}},
		apptest.Reply{Text: "Scheduled."},
		apptest.Reply{Text: "pong"},
	)
	lib := filepath.Join(w.Root, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "gnosh.json"), []byte(`{"permissions": {"allow": ["Bash(make *)"]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clk := &clock{now: time.Date(2026, 11, 18, 10, 0, 30, 0, time.Local)}
	s := sitWith(t, w, func(o *Options) {
		o.Now = clk.Now
		o.Relaunch = func(args, env []string) error { return os.ErrPermission }
	})
	s.see("Type a prompt and Enter.")
	s.typeLine("remind me to say pong in a minute")
	s.see("Scheduled.")
	// (/cd refuses while the turn is still ending: the reply is on the
	// screen a moment before the turn's last line.)
	s.see("-- done in")
	s.typeLine("/cd ../lib")
	s.see("Trust " + lib + "?")
	clk.add(time.Minute)
	// (The schedule is looked at once a second.)
	time.Sleep(2500 * time.Millisecond)
	if n := len(w.Model.Requests()); n != 2 {
		t.Fatalf("the model was asked %d times while /cd's question waited: a scheduled prompt went out under it", n)
	}
	s.typeLine("1")
	s.see("was not trusted")
	s.see("Scheduled task ")
	s.see("pong")
	s.typeLine("/exit")
	s.exit()
}

// /tasks lists the prompts scheduled in the session, as Claude Code
// 2.1.329's does, and /tasks stop with one's id cancels it.
func TestTasksListsScheduledPrompts(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "CronCreate", Args: `{"cron":"*/5 * * * *","prompt":"check the build"}`}}},
		apptest.Reply{Text: "Scheduled."},
	)
	clk := &clock{now: time.Date(2026, 11, 18, 10, 0, 30, 0, time.Local)}
	s := sitWith(t, w, func(o *Options) { o.Now = clk.Now })
	s.see("Type a prompt and Enter.")
	s.typeLine("check the build every five minutes")
	s.see("Scheduled.")
	s.see("-- done in")
	s.typeLine("/tasks")
	s.see(`a scheduled prompt, "*/5 * * * *" recurring, next run`)
	s.see("check the build")
	s.see("/tasks stop <id> stops one.")
	m := regexp.MustCompile(`(\S+)  a scheduled prompt`).FindStringSubmatch(s.out.String())
	if m == nil {
		t.Fatalf("no id in:\n%s", s.out.String())
	}
	s.typeLine("/tasks stop " + m[1])
	s.see("The scheduled prompt " + m[1] + " is cancelled.")
	s.typeLine("/tasks")
	s.see("No command has run in the background in this session.")
	s.typeLine("/exit")
	s.exit()
}
