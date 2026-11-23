package prompt

import (
	"strconv"
	"strings"
	"unicode"
)

// SummaryWords is the most words a summary is asked to have when the
// caller names no size: about 700 tokens, a minute's writing for a model
// on the person's own machine, and small enough for the room a 32K window
// leaves a summary.
const SummaryWords = 500

// SummaryInput is what the request for a summary depends on.
type SummaryInput struct {
	// Words is the most words the summary may have. Zero means
	// SummaryWords.
	Words int
	// Instructions is what the person typed after /compact, with anything
	// a hook added.
	Instructions string
	// Mem says the session has mem. The summary then leaves out what mem
	// holds, since gnosh hands that back itself afterwards.
	Mem bool
}

// Summary returns the request for a summary. It goes at the end of the
// session's own request, after the same system prompt, tools and messages,
// so that a model server reads only this much anew. That is also why it
// says to call no tool: the request still carries them.
func (s *Set) Summary(in SummaryInput) string {
	words := in.Words
	if words <= 0 {
		words = SummaryWords
	}
	vals := values{"words": strconv.Itoa(words)}
	vals.flag("mem", in.Mem)
	ask := s.bare(textSummary, vals)
	if extra := lines(in.Instructions, maxReason); extra != "" {
		ask += "\n" + s.bare(textSummaryExtra, values{"instructions": extra})
	}
	return Wrap(ask)
}

// SummaryPart returns the request for a summary of the conversation up to
// one of the person's prompts, which the summary is to take the place of,
// while what follows it stays (the rewind's "Summarize up to here"). It
// goes at the end of a request that holds that part alone. It is not in
// the marker: the loop puts it there, with the person's instructions.
func (s *Set) SummaryPart(words int, mem bool) string {
	if words <= 0 {
		words = SummaryWords
	}
	vals := values{"words": strconv.Itoa(words)}
	vals.flag("mem", mem)
	return s.bare(textSummaryPart, vals)
}

// SummaryFrom returns the request for a summary of the conversation from
// the person's prompt that begins with prompt on, which they are setting
// aside (the rewind's "Summarize from here"): mark is what stands at the
// end of that message, in gnosh's marker, so that a prompt written twice
// ("go on") is told apart. It goes at the end of the session's own
// request; the loop puts it in the marker.
func (s *Set) SummaryFrom(prompt, mark string, words int, mem bool) string {
	if words <= 0 {
		words = SummaryWords
	}
	vals := values{"words": strconv.Itoa(words), "prompt": oneLine(prompt, maxQuote), "mark": oneLine(mark, maxName)}
	vals.flag("mem", mem)
	return s.bare(textSummaryFrom, vals)
}

// SideRequest is a small request gnosh makes for itself, to a model that
// sees nothing else of the session: no tools, and as little thinking as
// the model allows.
type SideRequest struct {
	// System is the request's instructions: one part of a system prompt.
	// It is the same for every request of its kind.
	System string
	// Prompt is the one user message.
	Prompt string
}

// Command is a command the agent ran inside a todo, as the harness itself
// recorded it.
type Command struct {
	// Line is the command as it was run.
	Line string
	// Exit is its exit code.
	Exit int
	// NotRun says it could not be started at all.
	NotRun bool
}

// CloseReviewInput is what the reviewer of a close is shown.
type CloseReviewInput struct {
	// Todo is the todo's title, or its id when it has none. The question
	// at the end names it.
	Todo string
	// Thoughts are the thoughts kept on it, one a line: "name: text".
	Thoughts string
	// Ask is what the person asked in this turn. It is given only when
	// Transcript holds none of their words, and is cut at 2,000
	// characters.
	Ask string
	// Files are the files the agent's edits and writes changed in the
	// todo, and Commands the commands it ran there. The harness lists both
	// itself, so that a long transcript, cut short, cannot hide them.
	Files    []string
	Commands []Command
	// Result is what the agent is closing the todo with: its claim.
	Result string
	// Code is the call's own code, when that one call entered the todo
	// and closed it. It is cut at 4,000 characters.
	Code string
	// Transcript is what happened in the todo: what the agent was told,
	// what it said and called, what its tools returned. The caller lays it
	// out and cuts it to size.
	Transcript string
}

// CloseReview returns the request that checks a close before mem keeps it:
// did the agent do the todo it says is done? The reviewer answers with one
// JSON object, {"ok": true or false, "reason": "..."}.
//
// The instructions are the ones the earlier gnosh arrived at by replaying
// every real close it had on record. The question comes last and names the
// todo, asking first whether the result is this todo's own: asked the
// other way round, a reviewer passed a close that recorded another todo's
// work half the time.
func (s *Set) CloseReview(in CloseReviewInput) SideRequest {
	var cmds []string
	keep := keepCommands(in.Commands, in.Result)
	for i, c := range in.Commands {
		if !keep[i] {
			continue
		}
		vals := values{"line": oneLine(c.Line, maxQuote)}
		if c.NotRun {
			vals.flag("not_run", true)
		} else {
			vals["exit"] = strconv.Itoa(c.Exit)
		}
		cmds = append(cmds, s.fill(textCloseCommand, vals))
	}
	files, moreFiles := keepFiles(in.Files, in.Result)
	vals := values{
		"todo":       oneLine(in.Todo, maxTitle),
		"thoughts":   Indented(lines(in.Thoughts, maxBlock)),
		"ask":        clipChars(strings.TrimSpace(in.Ask), 2000),
		"files":      strings.Join(files, ", "),
		"commands":   strings.Join(cmds, "\n"),
		"result":     Indented(in.Result),
		"code":       Indented(clipChars(strings.TrimSpace(in.Code), 4000)),
		"transcript": strings.TrimSpace(in.Transcript),
	}
	for _, name := range []string{"thoughts", "files", "commands", "result", "transcript"} {
		vals.flag("no_"+name, vals[name] == "")
	}
	if n := len(in.Commands) - len(cmds); n > 0 {
		vals["left_out"] = strconv.Itoa(n)
	}
	if moreFiles > 0 {
		vals["more_files"] = strconv.Itoa(moreFiles)
	}
	return SideRequest{
		System: s.fill(textCloseReview, nil),
		Prompt: s.fill(textCloseRequest, vals),
	}
}

// How much of the commands and files of a todo a review is shown. Every
// review is a prompt the model server has not seen, read whole before a
// word comes back, and a todo of a long session may have run hundreds of
// commands: on Jae's AI box, reviews of such todos took two minutes and
// more, with the session waiting on each.
const (
	reviewCommands     = 40   // the newest commands shown
	reviewCommandChars = 8000 // and the most characters they take
	reviewNamed        = 20   // earlier commands shown because the result names them
	reviewFiles        = 50   // files shown
)

// keepCommands says which commands a review shows: the newest, as many
// as fit, where what backs a close usually is (the last test run, the
// last check), and the earlier ones the result names, so that a result
// such as "ran the migration first" keeps what it stands on.
func keepCommands(cmds []Command, result string) []bool {
	keep := make([]bool, len(cmds))
	n, size := 0, 0
	for i := len(cmds) - 1; i >= 0; i-- {
		l := len(oneLine(cmds[i].Line, maxQuote))
		if n == reviewCommands || n > 0 && size+l > reviewCommandChars {
			break
		}
		keep[i] = true
		n++
		size += l
	}
	named := 0
	for i := len(cmds) - 1; i >= 0 && named < reviewNamed; i-- {
		if !keep[i] && names(result, cmds[i].Line) {
			keep[i] = true
			named++
		}
	}
	return keep
}

// keepFiles returns the files a review shows, those the result names
// first, and how many more there are.
func keepFiles(paths []string, result string) (shown []string, more int) {
	var named, rest []string
	for _, p := range paths {
		p = oneLine(p, maxName)
		switch {
		case p == "":
		case names(result, p):
			named = append(named, p)
		default:
			rest = append(rest, p)
		}
	}
	all := append(named, rest...)
	if len(all) > reviewFiles {
		return all[:reviewFiles], len(all) - reviewFiles
	}
	return all, 0
}

// names reports whether text names something in s: a word of s that is
// not a flag or one of the shell's common words, and is long or holds a
// path's marks (a test's name, a script, a file), found in text whole, or
// as the last part of a path.
func names(text, s string) bool {
	if text == "" {
		return false
	}
	words := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("\"'`|&;<>(){}$=,:", r)
	})
	for _, w := range words {
		w = strings.Trim(w, ".")
		if strings.HasPrefix(w, "-") || commonWords[w] {
			continue
		}
		candidates := []string{w}
		if i := strings.LastIndex(w, "/"); i >= 0 {
			candidates = append(candidates, w[i+1:])
		}
		for _, c := range candidates {
			marked := strings.ContainsAny(c, "./_-0123456789")
			if (len(c) >= 8 || marked && len(c) >= 4) && strings.Contains(text, c) {
				return true
			}
		}
	}
	return false
}

// commonWords are words of shell lines that name nothing of the work.
var commonWords = map[string]bool{
	"/dev/null": true, "/tmp": true, "2>&1": true, "./...": true, "...": true,
	"printf": true, "readlink": true, "realpath": true, "basename": true, "dirname": true,
}

// GoalJudgeInput is what the judge of a goal is shown.
type GoalJudgeInput struct {
	// Condition is the goal as the person typed it, or the title of a
	// task that was closed as done.
	Condition string
	// Mem is what mem shows of the goal's todo: its todos, their results
	// and thoughts. These are the agent's claims.
	Mem string
	// Conversation is the end of the conversation, laid out and cut to
	// size by the caller. It is the full conversation, with what a pop
	// removed from the model's view: that is where the evidence is.
	Conversation string
}

// GoalJudge returns the request that judges whether a goal, or a task
// closed as done, really holds. The judge answers with one JSON object,
// {"ok": true, "basis": "conversation" or "user", "reason": "..."} or
// {"ok": false, "reason": "..."}. The basis is a field, and not a phrase
// the judge writes, because a small model asked for a phrase did not give
// it.
func (s *Set) GoalJudge(in GoalJudgeInput) SideRequest {
	vals := values{
		"condition":    strings.TrimSpace(in.Condition),
		"mem":          Indented(in.Mem),
		"conversation": strings.TrimSpace(in.Conversation),
	}
	vals.flag("no_mem", vals["mem"] == "")
	vals.flag("no_conversation", vals["conversation"] == "")
	return SideRequest{
		System: s.fill(textGoalJudge, nil),
		Prompt: s.fill(textGoalRequest, vals),
	}
}

// maxTitleSource is how much of a conversation's beginning the title
// request shows, in characters.
const maxTitleSource = 2000

// Title returns the request that gives a session its title. beginning is
// how the conversation begins: its first prompt, and the first answer if
// the caller has it. The request is to be made after the session's first
// turn has ended, never beside the turn's first request, and never for a
// sub-agent's session: asked earlier, it pushed the conversation itself
// out of a model server's cache. With nothing to read there is nothing to
// ask, and both fields are empty.
func (s *Set) Title(beginning string) SideRequest {
	beginning = strings.TrimSpace(beginning)
	if beginning == "" {
		return SideRequest{}
	}
	return SideRequest{
		System: s.fill(textTitle, nil),
		Prompt: Untrusted(clipChars(beginning, maxTitleSource)),
	}
}

// Namer returns the request that names the todos, thoughts and facts a
// mem call is about to make from their texts alone. One text is asked for
// one name; several are asked for in one request, numbered, and the answer
// is one name a line in the same order. With no texts there is nothing to
// ask, and both fields are empty.
func (s *Set) Namer(texts []string) SideRequest {
	if len(texts) == 0 {
		return SideRequest{}
	}
	var prompt string
	if len(texts) == 1 {
		prompt = Untrusted(oneLine(texts[0], maxQuote))
	} else {
		list := make([]string, len(texts))
		for i, t := range texts {
			list[i] = strconv.Itoa(i+1) + ". " + Untrusted(oneLine(t, maxQuote))
		}
		prompt = strings.Join(list, "\n")
	}
	return SideRequest{System: s.fill(textNamer, nil), Prompt: prompt}
}
