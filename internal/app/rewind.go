package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file is the rewind: taking a conversation back to before one of
// the person's prompts (spec/cc/sessions.md, section 5; spec/gnosh
// frames.md, 12.5).
//
// The points are the person's own prompts, as the session's file holds
// them after earlier rewinds; one inside a frame that was popped since is
// still a point. A rewind writes one entry into the session's file, which
// says the conversation goes on from the entry before the prompt, and
// then opens the session again from its file behind the same controls,
// exactly as leaving and resuming it would: so everything that is worked
// out from the conversation (what the model is sent, the frames the
// session stands in, what the features hold of it, whether the first
// prompt's facts have been given) is worked out afresh, by the code that
// a resume runs. Nothing is taken out of the file: a rewind that was a
// mistake is itself in the record.
//
// The files: those Write and Edit changed, as they were before the prompt,
// can be put back with the conversation ("/rewind N all") or alone
// ("/rewind N files"), from the copies checkpoint.go keeps; "/rewind N"
// takes the conversation alone, and the files stay as they are. A file
// only shell commands changed is not followed, so it is never put back. Because the session is opened again, which
// would stop what it runs in the background, a rewind waits until nothing
// does (rewindHeldBy).

// RewindPoint is one of the person's prompts that the conversation can be
// taken back to.
type RewindPoint struct {
	// ID names the point for Rewind.
	ID string
	// Text is the prompt, and Time when it was sent.
	Text string
	Time time.Time
	// Files are the files that changed from this prompt on, of those
	// Write and Edit changed, as the person is shown them: what a rewind
	// to before it can put back, as they were then. FilesWhy says why
	// none can be, when that is so: the copies from before it were let go
	// (only the most recent prompts' are kept) or are not found, or none
	// are kept here.
	Files    []string
	FilesWhy string
	// why is FilesWhy's error.
	why error
	// InView says the model still reads the prompt: a summary of what
	// came before it, or of what followed, can be made.
	InView bool
}

// rewindGroup finds the person's prompts in what is left of the
// conversation, and for each the entry the conversation would go on from
// if it were taken back to before that prompt: the one before the prompt
// and everything that went out with it. That is where the prompt went
// out, not where it was typed: one typed while a turn ran waited in the
// queue, and the files that turn changed meanwhile are the turn's, as its
// conversation is. queued names, by point, the loop's id of a prompt that
// waited so: its record of waiting stays in the conversation, and the
// rewind must say it will not go out.
func (l *live) rewindGroup() (points []RewindPoint, before, queued map[string]string) {
	entries := transcript.Conversation(l.ts.Entries())
	before, queued = map[string]string{}, map[string]string{}
	// first is where each prompt's own entries begin: the parts that rode
	// on it may stand before the prompt itself.
	first := map[string]int{}
	waited := map[string]bool{}
	for i := range entries {
		for _, e := range decode(&entries[i]) {
			id := ""
			switch e.Kind {
			case agent.EntryPrompt:
				id = e.ID
			case agent.EntryQueued:
				waited[e.ID] = true
			case agent.EntryNote:
				id = e.Prompt
			}
			if _, seen := first[id]; id != "" && !seen {
				first[id] = i
			}
		}
	}
	for i := range entries {
		for _, e := range decode(&entries[i]) {
			if e.Kind != agent.EntryPrompt || e.Hidden {
				continue
			}
			text := strings.TrimSpace(core.TextOf(e.Blocks))
			if text == "" {
				continue
			}
			points = append(points, RewindPoint{ID: entries[i].UUID, Text: text, Time: e.Time})
			if at, ok := first[e.ID]; ok && at > 0 {
				before[entries[i].UUID] = entries[at-1].UUID
			}
			if waited[e.ID] {
				queued[entries[i].UUID] = e.ID
			}
		}
	}
	return points, before, queued
}

// rewindPoints is SessionControls.RewindPoints. The session's file is
// read once for all of them.
func (l *live) rewindPoints() []RewindPoint {
	points, _, _ := l.rewindGroup()
	order := l.promptsInOrder()
	_, sources := l.log.viewTraced()
	read := map[string]bool{}
	for _, src := range sources {
		for _, u := range src {
			read[u] = true
		}
	}
	for i := range points {
		points[i].InView = read[points[i].ID]
		cps, err := l.filesSince(order, points[i].ID)
		if err == nil && len(cps) == 0 && !l.cfg.Options.FileCheckpointingEnabled.V {
			err = errNoCheckpoints
		}
		points[i].Files, points[i].why = make([]string, len(cps)), err
		for j, cp := range cps {
			points[i].Files[j] = l.showPath(cp.Path)
		}
		if err != nil {
			points[i].FilesWhy = err.Error()
		}
	}
	return points
}

// rewindHeldBy lists what a rewind must wait for: the work in the
// background, which opening the session again would stop, and which could
// change the files again as they are put back; a /subtask run, whose answer
// is to come back to this conversation; and, when files are put back, a
// /fork run, which works in the same folder.
func (l *live) rewindHeldBy(files bool) []string {
	held := l.runningWork()
	var runs []string
	own := l.ts.ID()
	l.p.runs.Range(func(id, v any) bool {
		if r := v.(*watchedRun); r.session == own && (r.what == "subtask" || files) {
			runs = append(runs, fmt.Sprintf("the %s %s", r.what, RunShort(id.(string))))
		}
		return true
	})
	slices.Sort(runs)
	return append(held, runs...)
}

// rewindWaits is the refusal of a rewind while held runs.
func rewindWaits(held []string) error {
	return fmt.Errorf("this is still running, and going back now would stop it, or it could change the files again: %s. Go back once it has ended, or stop it (/tasks stop <id>, or ← ← for a run)", strings.Join(held, ", "))
}

// RewindPoints lists the person's prompts of this conversation, the
// oldest first.
func (c *controls) RewindPoints() []RewindPoint { return c.cur().rewindPoints() }

// Rewind takes the conversation back to before the prompt that point
// names, and returns the prompt's text. The session must be idle.
func (c *controls) Rewind(ctx context.Context, point string) (string, error) {
	text, _, err := c.rewindWith(ctx, point, false, "")
	return text, err
}

// rewindWith is Rewind, which with files puts back what Write and Edit
// changed too, and says how that went. It waits for nothing in the
// background (rewindHeldBy), and the files are put back only once the
// rewind is in the session's file. kept,
// when not empty, is the model's summary of what followed the prompt
// (/rewind N summarize), which goes into the conversation after the
// rewind, for the model to read with the next prompt.
func (c *controls) rewindWith(ctx context.Context, point string, files bool, kept string) (text, filesSaid string, err error) {
	old := c.cur()
	if old.busy(ctx) {
		return "", "", errors.New("a turn is running. Stop it first (Esc, or Ctrl+C in the plain session), then go back")
	}
	if held := old.rewindHeldBy(files); len(held) > 0 {
		return "", "", rewindWaits(held)
	}
	points, before, queued := old.rewindGroup()
	for _, p := range points {
		if p.ID == point {
			text = p.Text
		}
	}
	to, ok := before[point]
	switch {
	case text == "":
		return "", "", errors.New("that prompt is not in this conversation any more")
	case !ok:
		return "", "", errors.New("that is the very start of this session's file, and there is nothing before it to go back to. /clear starts a fresh session")
	}
	var restore []fileCheckpoint
	if files {
		if restore, err = old.rewindFiles(point); err != nil {
			return "", "", fmt.Errorf("%v. Nothing was changed: /rewind N takes the conversation alone", err)
		}
	}
	inForce := partSummary(old.ts.Entries())
	if _, err := old.ts.Append(transcript.RewindEntry(to)); err != nil {
		return "", "", fmt.Errorf("the rewind could not be written into the session's file (%v), so nothing was changed", err)
	}
	old.keepSummary(inForce)
	if kept != "" {
		note := agent.Entry{Kind: agent.EntryNote, Source: "rewind", Blocks: []core.Block{
			core.Text(old.texts.SummaryFromNote(text)), core.Text(prompt.Untrusted(kept))}}
		if err := old.log.Append(ctx, note); err != nil {
			old.flog.logf("turn", "the summary of what followed the rewound prompt could not be kept: %v", err)
		}
	}
	if id := queued[point]; id != "" {
		// The prompt waited in the queue while a turn ran: its record of
		// waiting is still in the conversation, and must not make the
		// session send it again when it opens.
		if err := old.log.Append(ctx, agent.Entry{Kind: agent.EntryUnqueued, Prompt: id}); err != nil {
			old.flog.logf("turn", "a rewound prompt that had waited in the queue could not be marked as taken back: %v", err)
		}
	}
	id := old.ts.ID()
	old.flog.logf("turn", "the conversation was taken back to before the prompt in entry %s", point)
	old.close("other")
	if files {
		done, failed := old.checks.restore(restore, old.restoreHow())
		old.flog.logf("turn", "the files were put back as they were before the prompt in entry %s: %d put back, %d not", point, len(done), len(failed))
		filesSaid = restoreSaid(done, failed)
	}
	next, err := c.p.openSession(ctx, c.st, c.front, "rewind", id)
	if err != nil {
		return "", filesSaid, fmt.Errorf("the conversation was taken back, but the session could not be opened again: %w", err)
	}
	c.mu.Lock()
	c.l = next
	c.mu.Unlock()
	return text, filesSaid, nil
}

// rewindNote is what a person is told wherever a rewind is offered.
const rewindNote = "The files that this session's Write and Edit changed can be put back with the conversation, or alone, as they were before that prompt, whatever changed them since. What a file holds when it is put back is kept first, beside the session, and the rewind says where. Files that only shell commands or sub-agents changed are not followed, so they are not put back."

// slashRewind is /rewind: with no number it lists the points, and with
// one it asks for the conversation to be taken back to before that
// prompt, which the session's controls then do.
func (l *live) slashRewind(ctx context.Context, c *SlashContext) error {
	points := l.rewindPoints()
	if len(points) == 0 {
		c.Say("There is no prompt of yours in this conversation to go back to.")
		return nil
	}
	args := strings.Fields(c.Args)
	if len(args) == 0 {
		c.Say("Your prompts in this conversation:")
		for i, p := range points {
			files := ""
			switch {
			case errors.Is(p.why, errFilesGone):
				files = "  (the copies of the files from before it were let go)"
			case errors.Is(p.why, errFilesLost):
				files = "  (the copies of the files from before it are not found)"
			case len(p.Files) > 0:
				files = fmt.Sprintf("  (%s changed since)", countWords(len(p.Files), "file", "files"))
			}
			c.Say("  %d. %s  %s%s", i+1, p.Time.Local().Format("15:04"), oneLine(p.Text, 100), files)
		}
		c.Say("/rewind N takes the conversation back to before prompt N, which you can then send again or change; /rewind N all puts the files back too, and /rewind N files the files alone. %s", rewindNote)
		c.Say("/rewind N summarize goes back to before prompt N and keeps a summary of what followed it; /rewind N summarize-before makes everything before prompt N a summary, and what followed stays. Words after either say what the summary is to keep.")
		if !l.cfg.Options.FileCheckpointingEnabled.V {
			c.Say("What Write and Edit change is not kept now (fileCheckpointingEnabled is false): only what was kept before can be put back.")
		}
		return nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 || n > len(points) {
		return fmt.Errorf("%q is not the number of one of your prompts: /rewind lists them, 1 to %d", oneLine(args[0], 40), len(points))
	}
	how, guide := "", ""
	if len(args) > 1 {
		how, guide = args[1], strings.Join(args[2:], " ")
	}
	switch {
	case how == "summarize" || how == "summarize-before":
	case guide != "" || how != "" && how != "all" && how != "files":
		return errors.New("/rewind takes the number of one of your prompts, and then all (the files too), files (the files alone), summarize or summarize-before (with what the summary is to keep), or nothing")
	}
	if l.busy(ctx) {
		return errors.New("a turn is running. Stop it first, then go back")
	}
	point := points[n-1]
	switch how {
	case "summarize-before":
		return l.summarizeBefore(c, point, guide)
	case "summarize":
		return l.summarizeAfter(ctx, c, n, point, guide)
	}
	if how == "files" {
		// The files alone: the conversation stays where it is, and the
		// session is not opened again. What works in the background could
		// change the files again as they are put back.
		if held := l.rewindHeldBy(true); len(held) > 0 {
			return rewindWaits(held)
		}
		cps, ferr := l.rewindFiles(point.ID)
		if ferr != nil {
			return ferr
		}
		done, failed := l.checks.restore(cps, l.restoreHow())
		l.flog.logf("turn", "the files were put back as they were before the prompt in entry %s: %d put back, %d not", point.ID, len(done), len(failed))
		if len(done) > 0 {
			c.Say("The files are back as they were before your prompt: %s", oneLine(point.Text, 100))
		}
		c.Say("%s", restoreSaid(done, failed))
		c.Say("The conversation is as it was; the model is told with its next request.")
		_ = l.agent.Tell(agent.Note{Source: "rewind",
			Text: prompt.Wrap("The user put the files that Write and Edit changed since the prompt \"" + oneLine(point.Text, 200) + "\" back as they were before it. " + restoreSaid(done, failed) + " What you know of those files from later in this conversation is out of date: read them again before you change them.")})
		return nil
	}
	l.rewindTo, l.rewindAll = point.ID, how == "all"
	return nil
}

// summarizeBefore is /rewind N summarize-before, the rewind's "Summarize up
// to here": the conversation before the prompt becomes a summary, and what
// came after it stays as it was. The summary is made as /compact's is, and
// its events say how it went.
func (l *live) summarizeBefore(c *SlashContext, point RewindPoint, guide string) error {
	if _, at := l.viewAt(point.ID); at < 0 {
		return errNotInView
	}
	_, before, _ := l.rewindGroup()
	covers, ok := before[point.ID]
	msgs := l.log.viewUpTo(covers)
	if !ok || !slices.ContainsFunc(msgs, func(m core.Message) bool { return m.Role == core.RoleAssistant }) {
		return errors.New("there is nothing before that prompt to summarize: the model had not answered yet")
	}
	intro, err := l.texts.Format("summary-part-intro", nil)
	if err != nil {
		return err
	}
	part := agent.SummaryPart{Messages: msgs, Ask: l.texts.SummaryPart(prompt.SummaryWords, l.hasGno()), Intro: intro, Covers: covers}
	if err := c.Agent.CompactPart(guide, part); err != nil {
		return fmt.Errorf("no summary was asked for: %w", err)
	}
	return nil
}

// summarizeAfter is /rewind N summarize, the rewind's "Summarize from here":
// a summary is made of what followed the prompt, and the conversation goes
// back to before it, with the summary kept in its place (rewindKept) and
// the prompt back in the box. The files stay as they are.
func (l *live) summarizeAfter(ctx context.Context, c *SlashContext, n int, point RewindPoint, guide string) error {
	if held := l.rewindHeldBy(false); len(held) > 0 {
		return rewindWaits(held)
	}
	msgs, at := l.viewAt(point.ID)
	if at < 0 {
		return errNotInView
	}
	// The prompt is marked where the model reads it, so that one written
	// twice ("go on") is told apart, and the request keeps it whole.
	marked := msgs[at]
	marked.Blocks = append(slices.Clone(marked.Blocks), core.Text(prompt.Wrap(setAsideMark)))
	msgs = slices.Clone(msgs)
	msgs[at] = marked
	type made struct {
		text string
		err  error
	}
	done := make(chan made, 1)
	part := agent.SummaryPart{Messages: msgs, Keep: at, Ask: l.texts.SummaryFrom(point.Text, setAsideMark, prompt.SummaryWords, l.hasGno()),
		Done: func(text string, err error) { done <- made{text, err} }}
	if err := c.Agent.CompactPart(guide, part); err != nil {
		return fmt.Errorf("no summary was asked for: %w", err)
	}
	var r made
	select {
	case r = <-done:
	case <-ctx.Done():
		return errors.New("the summary was let go, and the conversation is as it was")
	}
	if r.err != nil {
		return fmt.Errorf("no summary was made (%v), so the conversation is as it was", r.err)
	}
	// The summary's command ends now. A prompt of the person's sent while
	// it was made runs after it: the rewind would take that off unseen, so
	// then there is none.
	for {
		switch st := c.Agent.State(); {
		case st.Queued > 0 || st.Turn != "":
			return fmt.Errorf("a prompt of yours went out while the summary was made, so the conversation was not taken back: /rewind %d summarize again once it is answered", n)
		case !st.Busy:
			l.rewindTo, l.rewindAll, l.rewindKept = point.ID, false, r.text
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("the summary was let go, and the conversation is as it was")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// setAsideMark marks, for "Summarize from here", the prompt the part set
// aside begins with.
const setAsideMark = "[the part set aside begins here]"

// errNotInView is the refusal of a summary of part of the conversation
// about a prompt the model no longer reads.
var errNotInView = errors.New("that prompt is no longer in what the model reads (a summary or a slide took it out), so there is no part of the conversation before or after it to summarize; /rewind N alone goes back to before it")

// viewAt returns the conversation as the model reads it now, and the
// place in it of the message the prompt in the session-file entry point
// went out in: -1 when the model no longer reads that prompt (a summary
// stands for it, or it slid out).
func (l *live) viewAt(point string) ([]core.Message, int) {
	msgs, sources := l.log.viewTraced()
	for i, src := range sources {
		if slices.Contains(src, point) {
			return msgs, i
		}
	}
	return msgs, -1
}

// keepSummary writes again, after a rewind, a summary of part of the
// conversation that was in force before it (one "Summarize up to here"
// made), when the part it stands for is still in the conversation:
// written at the end of the file, it was behind the point the rewind went
// back to, and the whole of that part would have come back.
func (l *live) keepSummary(was *transcript.Entry) {
	if was == nil {
		return
	}
	conv := transcript.Conversation(l.ts.Entries())
	has := func(uuid string) bool {
		return slices.ContainsFunc(conv, func(e transcript.Entry) bool { return e.UUID == uuid })
	}
	if has(was.UUID) || !has(was.Covers) {
		return
	}
	again := *was
	again.UUID, again.ParentUUID, again.Timestamp = "", "", time.Time{}
	if _, err := l.ts.Append(again); err != nil {
		l.flog.logf("turn", "the summary of the conversation before an earlier prompt could not be kept through the rewind: %v", err)
	}
}

// partSummary is the summary the conversation starts with, when it is one
// of part of it (Covers names an entry), or nil.
func partSummary(entries []transcript.Entry) *transcript.Entry {
	conv := transcript.Conversation(entries)
	for i := len(conv) - 1; i >= 0; i-- {
		if conv[i].Type == transcript.TypeSummary {
			if conv[i].Covers == "" {
				return nil
			}
			e := conv[i]
			return &e
		}
	}
	return nil
}

// hasGno says the session's model runs Gno (mem), whose summary leaves out
// what mem hands back.
func (l *live) hasGno() bool {
	return slices.Contains(l.toolNames(), gnoRunTool)
}

// countWords counts things in words: "1 file", "3 files".
func countWords(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
