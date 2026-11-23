package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/cli"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file is gnosh --bg, gnosh agents and gnosh stop, as Claude Code has
// claude --bg, claude agents and claude stop: a session started in the
// background from a shell, listed and stopped from one, from any folder;
// gnosh logs <session> (more.go) shows what it printed.
// The run is the kind /background and /fork start (detach.go): gnosh -p
// in a process of its own, its output in <session>.background.log beside
// the session's file, its process id in <session>.background.pid.

// stopWait is how long gnosh stop waits for a run to end after it was
// asked to stop, before it is made to: long enough for what a stopped turn
// waits for at the end of a session (a mem call under way, sub-agents in
// the background, hooks), so that it ends in good order.
const stopWait = time.Minute

// maxPipedPrompt is the most a prompt of gnosh --bg may hold with what was
// piped in: it goes to the run on its command line, where one argument
// may be no longer than 128 KiB on Linux.
const maxPipedPrompt = 100 << 10

// backgroundCommand is gnosh --bg: a new session on the prompt, as a run in
// the background, and how to follow it.
func (p *program) backgroundCommand(ctx context.Context, inv cli.Invocation) int {
	// The run makes its own work tree for --worktree: this process only
	// starts it, in the folder it was started in.
	here := inv
	here.Worktree, here.WorktreeName = false, ""
	st, code := p.prepared(ctx, here)
	if st == nil {
		return code
	}
	if p.o.Detach == nil {
		p.errf("this gnosh cannot start a session in the background, so nothing was started.")
		return cli.ExitFailure
	}
	prompt := inv.Prompt
	if inv.ReadStdin {
		// What was piped in goes with the prompt, as for gnosh -p; the
		// run itself reads nothing.
		piped, err := p.readPiped(ctx, true)
		if err != nil {
			p.errf("what was piped in could not be read (%v), so nothing was started.", err)
			return cli.ExitFailure
		}
		if prompt = joinPrompt(piped, prompt); len(prompt) > maxPipedPrompt {
			p.errf("what was piped in is %s with the prompt, more than a run in the background can be handed (%s), so nothing was started. Save it to a file and name the file in the prompt.", ByteCount(int64(len(prompt))), ByteCount(maxPipedPrompt))
			return cli.ExitFailure
		}
	}
	store := st.sessions()
	id := inv.SessionID
	// resumed is the file of the session -c or -r names, which the run
	// goes on with.
	resumed := ""
	if inv.Conversation != cli.ConversationNew {
		var err error
		if id, resumed, err = p.bgSession(ctx, st, inv); err != nil {
			p.errf("%v, so nothing was started.", err)
			return cli.ExitFailure
		}
		if transcript.InUse(resumed) {
			p.errf("the session %s is open in another gnosh, and one session has one writer, so nothing was started. %s.", id, heldByRun(id))
			return cli.ExitFailure
		}
	}
	if id == "" {
		var err error
		if id, err = store.NewID(); err != nil {
			p.errf("the session could not be given an ID (%v), so nothing was started.", err)
			return cli.ExitFailure
		}
	}
	args, err := p.table.RelaunchArgs(p.o.Args, cli.Relaunch{Session: id, Fresh: resumed == ""})
	if err != nil {
		p.errf("the command line for the run could not be made (%v), so nothing was started.", err)
		return cli.ExitFailure
	}
	if inv.Name != "" {
		args = append(args, "--name="+inv.Name)
	}
	run := append(args, "--print", "--", prompt)
	dir := store.ProjectDir(st.cfg.Paths.Cwd)
	if resumed != "" {
		// (Its runs' logs are beside the session's file.)
		dir = filepath.Dir(resumed)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		p.errf("the folder for the session's files, %s, could not be made (%v), so nothing was started.", dir, err)
		return cli.ExitFailure
	}
	log := filepath.Join(dir, id+".background.log")
	file := firstOf(resumed, filepath.Join(dir, id+".jsonl"))
	if inv.Worktree && resumed == "" {
		file = "" // (under the work tree's folder, which the run makes)
	}
	name := inv.Name
	if name == "" && resumed != "" {
		// A session sent back to work keeps the name its runs had.
		name = readRunLog(log, id, time.Time{}).name
	}
	p.logHead(log, id, name, st.cfg.Paths.Cwd, file, prompt)
	pid, err := p.o.Detach(run, prepareRun(p.o.Environ, log), log)
	if err != nil {
		p.errf("the session could not be started in the background (%v).", err)
		return cli.ExitFailure
	}
	writeRunPID(log, pid)
	short := RunShort(id)
	fmt.Fprintf(p.stdout, "%s%s · process %d\n", bgStarted, id, pid)
	hints := [][2]string{
		{"gnosh agents", "lists the sessions in the background"},
		{"gnosh attach " + short, "watches it"},
		{"gnosh logs " + short, "what it prints (-f follows it)"},
		{"gnosh stop " + short, "stops it"},
		{"gnosh -r " + short, "opens it once it is done"},
	}
	width := 0
	for _, h := range hints {
		width = max(width, len(h[0]))
	}
	for _, h := range hints {
		fmt.Fprintf(p.stdout, "  %-*s  %s\n", width, h[0], h[1])
	}
	return cli.ExitOK
}

// bgSession is the session gnosh --bg -c or -r <session> sends to the
// background: its id and its file. -c is the newest session of the
// folder that no gnosh holds open; -r is a session's id, the start of
// one, its file, or its name among the folder's.
func (p *program) bgSession(ctx context.Context, st *startup, inv cli.Invocation) (id, path string, err error) {
	store := st.sessions()
	cwd := st.cfg.Paths.Cwd
	idOf := func(path string) string { return strings.TrimSuffix(filepath.Base(path), ".jsonl") }
	if inv.Conversation == cli.ConversationContinue {
		list, err := store.List(ctx, cwd)
		if err != nil {
			return "", "", fmt.Errorf("the sessions of this folder could not be listed: %w", err)
		}
		for _, info := range list {
			if !transcript.InUse(info.Path) {
				return info.ID, info.Path, nil
			}
		}
		return "", "", fmt.Errorf("there is no session of %s to continue that no gnosh holds open", cwd)
	}
	word := strings.TrimSpace(inv.Resume)
	if inv.ResumeIsFile {
		if !fileExists(word) {
			return "", "", fmt.Errorf("there is no session file %s", word)
		}
		// Only one of gnosh's own: a run's log, its process id and the
		// token its questions are answered with go beside its session's
		// file, where gnosh agents, stop and logs look, and the run goes
		// on by the session's id (the relay review of 2026-11-19: one
		// from elsewhere had them put beside it, and went on with
		// another session or none).
		id := idOf(word)
		own, err := store.Path(id)
		a, aerr := os.Stat(own)
		b, berr := os.Stat(word)
		if err != nil || aerr != nil || berr != nil || !os.SameFile(a, b) {
			return "", "", fmt.Errorf("%s is not one of gnosh's own session files: gnosh -r %s brings a copy of it in, and gnosh --bg -r <that copy's id> then goes on with it in the background", word, word)
		}
		return id, own, nil
	}
	if path, err := store.Path(word); err == nil {
		return idOf(path), path, nil
	} else if ambiguous := (*transcript.AmbiguousError)(nil); errors.As(err, &ambiguous) {
		return "", "", fmt.Errorf("%q is the start of more than one session's id (%s): give more of it", word, strings.Join(ambiguous.IDs, ", "))
	}
	list, err := store.List(ctx, cwd)
	if err != nil {
		return "", "", fmt.Errorf("the sessions of this folder could not be listed: %w", err)
	}
	var found []transcript.Info
	for _, info := range list {
		if info.Title != "" && strings.EqualFold(strings.TrimSpace(info.Title), word) {
			found = append(found, info)
		}
	}
	switch len(found) {
	case 0:
		return "", "", fmt.Errorf("no session has the ID or the name %q", word)
	case 1:
		return found[0].ID, found[0].Path, nil
	}
	return "", "", fmt.Errorf("%d sessions of this folder are named %q: give the ID of one", len(found), word)
}

// RunShort is the start of a session's id that names its run in the
// folder's runs: up to the end of its second group, the time to the
// millisecond in gnosh's ids, since two runs started within the same
// minute share the first eight characters.
func RunShort(id string) string {
	if len(id) > 13 && id[8] == '-' {
		return id[:13]
	}
	return id
}

// pidFile is the file beside a run's log that holds its process id.
func pidFile(log string) string {
	return strings.TrimSuffix(log, ".log") + ".pid"
}

// writeRunPID keeps a run's process id beside its log, for gnosh stop. A
// run whose id was not kept can still be followed and opened again.
func writeRunPID(log string, pid int) {
	_ = os.WriteFile(pidFile(log), []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

// findRun finds the run in the background that word names, among those
// whose logs are in dirs: a session's id, or the start of one that a
// single session's has. A session sent off from two folders has a log in
// each: the one found is the log of the run that works, or else the
// newer.
func findRun(dirs []string, word string) (id, log string, err error) {
	word = strings.TrimSpace(word)
	if word == "" || strings.ContainsAny(word, `/\`) {
		return "", "", fmt.Errorf("%q names no session", word)
	}
	var ids, logs []string
	for _, r := range runsIn(dirs, time.Time{}) {
		if r.session == word {
			return r.session, r.log, nil // the whole id: no other is meant
		}
		if strings.HasPrefix(r.session, word) {
			ids, logs = append(ids, r.session), append(logs, r.log)
		}
	}
	switch len(ids) {
	case 0:
		return "", "", fmt.Errorf("no session ran in the background under the ID %s (gnosh agents lists them; gnosh --bg, /background and /fork start one)", word)
	case 1:
		return ids[0], logs[0], nil
	}
	slices.Sort(ids)
	return "", "", fmt.Errorf("%s is the start of the IDs of %d sessions that ran in the background (%s): give more of it", word, len(ids), strings.Join(ids, ", "))
}

// runSession is the session file a run's log names, or the one beside it.
func runSession(id, log string) string {
	return firstOf(readRunLog(log, id, time.Time{}).file, filepath.Join(filepath.Dir(log), id+".jsonl"))
}

// runAlive reports whether the run of the session id whose log is log
// still works: its process, kept beside the log, is there and is gnosh at
// work on that session. A session open in some other gnosh is not its run.
// A run whose process id was not kept (an older gnosh's) is told by the
// hold on its session's file instead.
func runAlive(id, log, file string) bool {
	if pid := runPID(log); pid != 0 {
		return runIsFor(pid, id)
	}
	return file != "" && transcript.InUse(file)
}

// stopCommand is gnosh stop <session>: the run is asked to stop, as Esc
// stops a turn, and made to when it has not ended in stopWait. What it
// did so far stays in its session.
func (p *program) stopCommand(ctx context.Context, inv cli.Invocation) int {
	st, code := p.prepared(ctx, inv)
	if st == nil {
		return code
	}
	id, log, err := findRun(st.sessions().ProjectDirs(), inv.Args[0])
	if err != nil {
		p.errf("%v.", err)
		return cli.ExitFailure
	}
	proc, said, err := askStop(id, log)
	if err == nil && proc != nil {
		fmt.Fprintf(p.stderr, "gnosh: the session %s was asked to stop; it ends what it was doing first (up to %s).\n", id, roundTime(stopWait))
		said, err = awaitStop(ctx, p.now, id, log, proc)
	}
	if err != nil {
		p.errf("%v.", err)
		return cli.ExitFailure
	}
	fmt.Fprintf(p.stdout, "gnosh: %s\n", said)
	return cli.ExitOK
}

// askStop asks the run of a session in the background to stop, as Esc
// stops a turn, for gnosh stop and /tasks stop. It returns the run's
// process, for awaitStop, or, when the run does not work, what to say of
// it, or why nothing was stopped.
func askStop(id, log string) (*os.Process, string, error) {
	file := runSession(id, log)
	if !runAlive(id, log, file) {
		return nil, fmt.Sprintf("the session %s is not running: gnosh -r %s opens it.", id, id), nil
	}
	pid := runPID(log)
	if pid == 0 {
		return nil, "", fmt.Errorf("the session %s is open, and gnosh has no process ID for a run of it (an older gnosh started it, or it is open in a gnosh of its own), so nothing was stopped", id)
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		err = proc.Signal(syscall.SIGTERM)
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return nil, "", fmt.Errorf("process %d could not be asked to stop: %v", pid, err)
	}
	return proc, "", nil
}

// awaitStop waits for a run askStop asked to stop to end, and ends it when
// it has not in stopWait. Its log then says it was stopped. It says what
// came of it.
func awaitStop(ctx context.Context, now func() time.Time, id, log string, proc *os.Process) (string, error) {
	deadline := now().Add(stopWait)
	for runIsFor(proc.Pid, id) && now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	said := fmt.Sprintf("the session %s was stopped. What it did so far is kept: gnosh -r %s opens it.", id, id)
	if runIsFor(proc.Pid, id) {
		_ = proc.Kill()
		said = fmt.Sprintf("the session %s did not stop within %s, and its run was ended; commands it left running in the background may still run. What it did so far is kept: gnosh -r %s opens it.", id, roundTime(stopWait), id)
	}
	if f, err := os.OpenFile(log, os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		fmt.Fprintf(f, "gnosh: %s%s\n", now().UTC().Format(time.RFC3339), runStopped)
		f.Close()
	}
	return said, nil
}

// heldByRun says what to do about a session that may be held by a run in
// the background: it lets go once it is done. (gnosh stop and /tasks stop
// find a run of any folder.)
func heldByRun(id string) string {
	short := RunShort(id)
	return fmt.Sprintf("If it is a run in the background, it lets go of the session when it is done: gnosh logs -f %s follows it, and gnosh stop %s stops it", short, short)
}

// runPID is the process id kept beside a run's log, or 0.
func runPID(log string) int {
	data, err := os.ReadFile(pidFile(log))
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || perr != nil || pid <= 1 {
		return 0
	}
	return pid
}

// runState is how a run stands, in the words Claude Code's list has for
// it: working while it holds its session (blocked while a question waits
// for the agent view), then stopped when gnosh stop or a signal stopped
// it, failed when it ended with an error or without a word (killed), or
// else done.
func runState(r backgroundRun) string {
	switch {
	case r.running && r.asking != nil:
		return "blocked"
	case r.running:
		return "working"
	case r.stopped || r.ended == "stopped":
		return "stopped"
	case r.ended == "failed" || r.died:
		return "failed"
	}
	return "done"
}

// runEnding is how a run that failed, or that a signal stopped, ended, in
// a few words, or "" for any other.
func runEnding(r backgroundRun) string {
	switch runState(r) {
	case "failed":
		if r.died {
			return "it ended without a word: it was killed, or its machine stopped"
		}
		return strings.TrimSuffix(r.failure, ".")
	case "stopped":
		if !r.stopped {
			return "it was stopped by a signal"
		}
	}
	return ""
}

// agentsCommand is gnosh agents, as Claude Code has claude agents: the
// sessions in the background (gnosh --bg, /background, /fork) of every
// folder, or, with --cwd, of that folder and the folders under it. On a
// terminal it opens the agent view to work in (agentview.go), as Claude
// Code's does. Elsewhere it prints the list, the working ones first: those
// done in the last week follow, or with --all every one whose log is
// kept. --json prints them as JSON, the working ones only unless --all is
// given too, as Claude Code's does.
func (p *program) agentsCommand(ctx context.Context, inv cli.Invocation) int {
	st, code := p.prepared(ctx, inv)
	if st == nil {
		return code
	}
	if !inv.JSON && p.o.AgentView != nil {
		return p.agentViewOn(st, inv, "")
	}
	if p.o.StdoutIsTerminal {
		// The list names runs by their prompts, which may be a model's
		// words: on a terminal they are printed plain, as other
		// subcommands' output is (printsFromOutside; the relay review of
		// 2026-11-19).
		p.stdout.w = plainWriter{p.stdout.w}
	}
	now := p.now()
	since := now.Add(-runsKept)
	if inv.All {
		since = time.Time{}
	}
	var working, done []backgroundRun
	for _, r := range runsIn(st.sessions().ProjectDirs(), since) {
		switch {
		case inv.Cwd != "" && !within(r.cwd, st.cfg.Paths.Cwd):
		case r.running:
			working = append(working, r)
		case !inv.JSON || inv.All:
			done = append(done, r)
		}
	}
	runs := append(working, done...)
	if inv.JSON {
		type row struct {
			ID        string    `json:"id"`
			Kind      string    `json:"kind"`
			Cwd       string    `json:"cwd,omitempty"`
			StartedAt time.Time `json:"startedAt"`
			State     string    `json:"state"`
			PID       int       `json:"pid,omitempty"`
			Status    string    `json:"status,omitempty"`
			// WaitingFor and SessionID are Claude Code's: what a blocked
			// run waits on, and the id gnosh -r takes (here its id).
			WaitingFor string `json:"waitingFor,omitempty"`
			SessionID  string `json:"sessionId"`
			Name       string `json:"name,omitempty"`
			Prompt     string `json:"prompt,omitempty"`
			Log        string `json:"log"`
		}
		rows := []row{}
		for _, r := range runs {
			w := row{ID: r.session, Kind: "background", Cwd: r.cwd, StartedAt: r.started.UTC(), State: runState(r),
				Status: oneLine(r.last, 400), SessionID: r.session, Name: r.name, Prompt: r.prompt, Log: r.log}
			if r.asking != nil {
				w.Status, w.WaitingFor = oneLine(r.asking.Title, 400), "permission prompt"
			}
			if pid := runPID(r.log); r.running && pid != 0 && runIsFor(pid, r.session) {
				w.PID = pid
			}
			rows = append(rows, w)
		}
		enc := json.NewEncoder(p.stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
		return cli.ExitOK
	}
	if len(runs) == 0 {
		said := "No session runs in the background"
		if inv.Cwd != "" {
			said += " from " + homeTilde(st.cfg.Paths.Home, st.cfg.Paths.Cwd) + " or a folder in it"
		}
		if inv.All {
			said += ", and no log of one that ran is kept"
		} else {
			said += ", and none ran in the last week"
		}
		fmt.Fprintf(p.stdout, "%s: gnosh --bg <prompt> starts one.\n", said)
		return cli.ExitOK
	}
	type cells struct{ name, state, dir, prompt string }
	rows := make([]cells, len(runs))
	wState, wDir := 0, 0
	for i, r := range runs {
		state := runState(r)
		if r.running {
			state += " " + roundTime(now.Sub(r.started))
		}
		rows[i] = cells{RunShort(r.session) + "  " + r.started.Local().Format("2006-01-02 15:04"), state,
			homeTilde(st.cfg.Paths.Home, firstOf(r.cwd, "(folder not known)")), oneLine(firstOf(r.name, r.prompt, "(its prompt is not in its log)"), 80)}
		wState, wDir = max(wState, utf8.RuneCountInString(state)), max(wDir, utf8.RuneCountInString(rows[i].dir))
	}
	for _, c := range rows {
		fmt.Fprintf(p.stdout, "%s  %-*s  %-*s  %s\n", c.name, wState, c.state, wDir, c.dir, c.prompt)
	}
	fmt.Fprintln(p.stdout, "gnosh logs <name> prints what one printed (-f follows it), gnosh stop <name> stops one, and gnosh -r <name> opens one.")
	return cli.ExitOK
}

// runIsFor reports whether process pid is a gnosh run of the session id:
// its command line names the session. A process id is used again once
// its process has ended, and a stop must not reach another program.
func runIsFor(pid int, id string) bool {
	// (An id that names no session is no run's: "" is in every command
	// line. The relay review of 2026-11-19.)
	if !transcript.ValidID(id) {
		return false
	}
	// (A process that is gone is told so without running anything: most
	// runs listed are over, and the agent view looks every second.)
	if p, err := os.FindProcess(pid); err != nil || p.Signal(syscall.Signal(0)) != nil {
		return false
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		return strings.Contains(string(data), id)
	}
	// -ww: the whole command line. Without a terminal to fit, ps may cut
	// it to 79 columns, before the session's id, which a run's command
	// line names last (the relay review of 2026-11-19).
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && strings.Contains(string(out), id)
}
