package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A run in the background puts its permission question in its .ask file,
// says on stderr that it waits for an answer in gnosh agents, and waits:
// an answer that does not give the question's token back is not heard,
// and the right one lets the call run. Where its questions go is not in
// the environment of what it runs. gnosh agents --json says blocked while
// it waits.
func TestRunAsksTheAgentView(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"echo \"[$GNOSH_RUN_ASKS]\" > env.txt; touch made.txt"}`}}},
		apptest.Reply{Text: "Made it."})
	base := filepath.Join(w.Root, "run")
	o := testOptions(w, "-p", "make the file")
	o.Environ = append(o.Environ, AsksEnv+"="+base)
	done := make(chan result, 1)
	go func() { done <- runWith(t, context.Background(), o) }()

	var ask runAsk
	deadline := time.Now().Add(30 * time.Second)
	for {
		if a := readAsk(base + ".log"); a != nil {
			ask = *a
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run put no question in its .ask file")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ask.Title != "Allow Bash?" || !strings.Contains(ask.Text, "touch made.txt") || len(ask.Token) != 32 || len(ask.Choices) < 2 || ask.Choices[0].Key != "yes" {
		t.Fatalf("the question: %+v", ask)
	}
	// A forged answer, without the token: not heard.
	forged, _ := json.Marshal(runAnswer{ID: ask.ID, Choice: "yes"})
	if err := os.WriteFile(answerPath(base, ask.ID), forged, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * askLook)
	if w.Exists("made.txt") {
		t.Fatal("an answer without the question's token let the call run")
	}
	if readAsk(base+".log") == nil {
		t.Fatalf("the question went with the forged answer")
	}
	if err := answerAsk(base+".log", ask.ID, "yes", ""); err != nil {
		t.Fatal(err)
	}
	var r result
	select {
	case r = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not end after its question was answered")
	}
	r.wantCode(t, 0)
	if !w.Exists("made.txt") {
		t.Errorf("the answered call did not run")
	}
	if got, _ := os.ReadFile(filepath.Join(w.Dir, "env.txt")); strings.TrimSpace(string(got)) != "[]" {
		t.Errorf("the command saw where the run's questions go: %q", got)
	}
	has(t, "stderr", r.err, "waiting for an answer in gnosh agents: Allow Bash?")
	if left, _ := filepath.Glob(base + ".ask-*.json"); len(left) != 0 {
		t.Errorf("question files are still there after the answer: %q", left)
	}
	if err := answerAsk(base+".log", ask.ID, "yes", ""); err == nil {
		t.Errorf("a question answered already took another answer")
	}
}

// A run's state is blocked while its question waits, and the question's
// title is its status, in gnosh agents --json; gnosh --bg tells the run
// where its questions go.
func TestBlockedRunInTheList(t *testing.T) {
	w := apptest.NewWorld(t)
	var log string
	o := testOptions(w, "--bg", "clean the build")
	o.Detach = func(_, env []string, l string) (int, error) {
		log = l
		want := AsksEnv + "=" + strings.TrimSuffix(l, ".log")
		found := false
		for _, kv := range env {
			found = found || kv == want
		}
		if !found {
			t.Errorf("the run's environment does not say where its questions go")
		}
		return 4242, nil
	}
	runWith(t, context.Background(), o).wantCode(t, 0)
	id := strings.TrimSuffix(filepath.Base(log), ".background.log")
	// The run's process: one whose command line names its session.
	proc := exec.Command("sh", "-c", `trap "exit 0" TERM; while :; do sleep 0.1; done`, id)
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Process.Kill(); proc.Wait() })
	if err := os.WriteFile(pidFile(log), []byte(strconv.Itoa(proc.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	has(t, "gnosh agents --json before", run(t, w, "agents", "--json").wantCode(t, 0).out, `"state": "working"`)
	ask, _ := json.Marshal(runAsk{AgentQuestion{ID: "q1", Title: "Allow Bash?", Text: "  $ rm -rf build",
		Choices: []QuestionChoice{{Key: "yes", Label: "Yes, this once"}, {Key: "no", Label: "No"}}}, strings.Repeat("ab", 16)})
	if err := os.WriteFile(askPath(runBase(log), "q1"), ask, 0o600); err != nil {
		t.Fatal(err)
	}
	has(t, "gnosh agents --json", run(t, w, "agents", "--json").wantCode(t, 0).out, `"state": "blocked"`, `"status": "Allow Bash?"`, `"waitingFor": "permission prompt"`, `"sessionId": "`)
	if err := answerAsk(log, "q1", "maybe", ""); err == nil || !strings.Contains(err.Error(), "is not one of the question's answers") {
		t.Errorf("an answer the question does not offer: %v", err)
	}
	if err := answerAsk(log, "q1", "no", "use git clean"); err != nil {
		t.Fatal(err)
	}
	var a runAnswer
	data, _ := os.ReadFile(answerPath(runBase(log), "q1"))
	if json.Unmarshal(data, &a) != nil || a.ID != "q1" || a.Token != strings.Repeat("ab", 16) || a.Choice != "no" || a.Text != "use git clean" {
		t.Errorf("the answer written: %s", data)
	}
}

// Words sent to a working run from the agent view reach its model between
// its steps as the person's, and keep the run open for the turn they
// start; a message without the run's token is not heard; what it did not
// read before it ended is taken back.
func TestRunReadsItsInbox(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"sleep 3"}`}}},
		apptest.Reply{Text: "Slept, and the word is plum."})
	w.Allow("Bash(sleep *)")
	base := filepath.Join(w.Root, "run")
	log := base + ".log"
	o := testOptions(w, "-p", "sleep a while")
	o.Environ = append(o.Environ, AsksEnv+"="+base)
	done := make(chan result, 1)
	go func() { done <- runWith(t, context.Background(), o) }()
	deadline := time.Now().Add(30 * time.Second)
	for !fileExists(tokenFile(log)) {
		if time.Now().After(deadline) {
			t.Fatal("the run wrote no token")
		}
		time.Sleep(20 * time.Millisecond)
	}
	forged, _ := json.Marshal(inboxMessage{Token: "not-it", Text: "say forged"})
	if err := os.WriteFile(base+".inbox-00000000000000000001.json", forged, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tellRun(log, "and say plum", time.Now()); err != nil {
		t.Fatal(err)
	}
	var r result
	select {
	case r = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the run did not end")
	}
	r.wantCode(t, 0)
	body := requestText(t, w, 1)
	has(t, "what the model read", body, "and say plum")
	hasNot(t, "what the model read", body, "say forged")
	if left := inboxMessages(base); len(left) != 0 {
		t.Errorf("messages left in the inbox: %q", left)
	}
	if err := tellRun(log, "too late", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := unreadWords(log); len(got) != 1 || got[0] != "too late" || len(inboxMessages(base)) != 0 {
		t.Errorf("unread words: %q", got)
	}
}

// An attended session's status line counts the runs in the background, of
// any folder, that wait on the person ("agents"), as Claude Code's footer
// does.
func TestSessionCountsRunsThatWait(t *testing.T) {
	w := apptest.NewWorld(t)
	var log string
	o := testOptions(w, "--bg", "clean the build")
	o.Detach = func(_, _ []string, l string) (int, error) {
		log = l
		return 4242, nil
	}
	runWith(t, context.Background(), o).wantCode(t, 0)
	id := strings.TrimSuffix(filepath.Base(log), ".background.log")
	proc := exec.Command("sh", "-c", `trap "exit 0" TERM; while :; do sleep 0.1; done`, id)
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Process.Kill(); proc.Wait() })
	if err := os.WriteFile(pidFile(log), []byte(strconv.Itoa(proc.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ask, _ := json.Marshal(runAsk{AgentQuestion{ID: "q1", Title: "Allow Bash?", Choices: []QuestionChoice{{Key: "yes", Label: "Yes"}}}, strings.Repeat("ab", 16)})
	if err := os.WriteFile(askPath(runBase(log), "q1"), ask, 0o600); err != nil {
		t.Fatal(err)
	}
	_, r := attach(t, w, nil, func(ctx context.Context, c SessionControls, f *scripted) int {
		deadline := time.Now().Add(20 * time.Second)
		for c.Status()["agents"] != "1 run needs input" {
			if time.Now().After(deadline) {
				t.Errorf("the status line's facts: %v", c.Status())
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		c.Quit("prompt_input_exit")
		return 0
	})
	r.wantCode(t, 0)
	seen := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	if got := agentsWord(nil, seen); got != "" {
		t.Errorf("no runs: %q", got)
	}
	// Runs that ended since the person last looked count as done or
	// failed; none counts while one waits on the person.
	ended := []backgroundRun{
		{ended: "done", endedAt: seen.Add(time.Minute)}, {ended: "done", endedAt: seen.Add(2 * time.Minute)},
		{ended: "failed", failure: "it ended with exit status 1.", endedAt: seen.Add(time.Minute)},
		{ended: "done", endedAt: seen.Add(-time.Minute)}, {stopped: true, endedAt: seen.Add(time.Minute)}, {running: true},
	}
	if got := agentsWord(ended, seen); got != "2 runs done, 1 failed" {
		t.Errorf("ended since: %q", got)
	}
	if got := agentsWord(ended[2:3], seen); got != "1 run failed" {
		t.Errorf("one failed since: %q", got)
	}
	two := append([]backgroundRun{{running: true, asking: &runAsk{}}, {running: true, asking: &runAsk{}}, {running: true}, {asking: &runAsk{}}}, ended...)
	if got := agentsWord(two, seen); got != "2 runs need input" {
		t.Errorf("two waiting: %q", got)
	}
}

// Two questions a run puts at once (a sub-agent's beside the main agent's)
// each have their own files: an answer to one is the other's no matter,
// the list shows the one put first, and each goes with its own answer.
func TestTwoQuestionsAtOnce(t *testing.T) {
	base := filepath.Join(t.TempDir(), "run")
	log := base + ".log"
	clock := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	got := make(chan string, 2)
	ask := func(id string, at time.Time) {
		q := agent.Question{ID: id, Kind: "permission", Title: "Allow " + id + "?", Choices: []agent.Choice{{Key: "yes", Label: "Yes"}, {Key: "no", Label: "No"}}}
		go relayAsk(t.Context(), base, q, func() time.Time { return at }, func(a agent.Answer) error { got <- id + " " + a.Choice; return nil })
	}
	ask("q1", clock)
	ask("q2", clock.Add(time.Second))
	deadline := time.Now().Add(10 * time.Second)
	for {
		if paths, _ := filepath.Glob(base + ".ask-*.json"); len(paths) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the two questions were not both written down")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a := readAsk(log); a == nil || a.ID != "q1" {
		t.Errorf("the question listed is %+v, want the one put first", a)
	}
	if err := answerAsk(log, "q2", "yes", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-got:
		if a != "q2 yes" {
			t.Fatalf("the answer reached %q", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second question's answer was not heard")
	}
	time.Sleep(3 * askLook)
	if a := readAsk(log); a == nil || a.ID != "q1" {
		t.Fatalf("after the second was answered the first is %+v", a)
	}
	if err := answerAsk(log, "q1", "no", "not now"); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-got:
		if a != "q1 no" {
			t.Fatalf("the answer reached %q", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first question's answer was not heard")
	}
}

// Before a run starts, what an earlier run of the session left of
// questions, answers and words is cleared, and a new token is written;
// words a run did not read are taken back only if they carry its token.
func TestPrepareRunAndUnreadWords(t *testing.T) {
	base := filepath.Join(t.TempDir(), "run")
	log := base + ".log"
	for _, name := range []string{".ask-q1.json", ".answer-q1.json", ".inbox-00000000000000000001.json"} {
		if err := os.WriteFile(base+name, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(base+".token", []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := prepareRun([]string{"A=1", AsksEnv + "=elsewhere"}, log)
	if left, _ := filepath.Glob(base + ".*-*.json"); len(left) != 0 {
		t.Errorf("left from an earlier run: %q", left)
	}
	token, _ := os.ReadFile(base + ".token")
	if t1 := strings.TrimSpace(string(token)); len(t1) != 32 || t1 == "old" {
		t.Errorf("the new token: %q", token)
	}
	if strings.Join(env, " ") != "A=1 "+AsksEnv+"="+base {
		t.Errorf("the run's environment: %q", env)
	}
	if err := tellRun(log, "real words", time.Now()); err != nil {
		t.Fatal(err)
	}
	forged, _ := json.Marshal(inboxMessage{Token: "not-it", Text: "forged words"})
	if err := os.WriteFile(base+".inbox-00000000000000000002.json", forged, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unreadWords(log); len(got) != 1 || got[0] != "real words" {
		t.Errorf("unread words: %q", got)
	}
}
