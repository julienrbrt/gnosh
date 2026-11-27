package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file is the automatic keep of §10.7: a digest of the work a slide
// would take, written to mem by the bridge itself when the model kept none,
// so that the slide can go on instead of the host's summary. The digest is
// made without a model call, from what the entries hold: the model's own
// words and the calls it made, each with the head of what came back.

const (
	// digestMaxChars bounds what is kept in mem, digestShowChars what the
	// model is shown of it with the slide's line.
	digestMaxChars  = 6000
	digestShowChars = 2800
	// digestMinLine and digestMaxLine bound one round's line.
	digestMinLine = 90
	digestMaxLine = 420
)

// digestOf is the digest of the entries the cut removed, oldest first. It
// is empty when they hold nothing the model said or got back.
func digestOf(entries []transcript.Entry, removed []string) string {
	in := make(map[string]bool, len(removed))
	for _, u := range removed {
		in[u] = true
	}
	results := map[string]core.Block{}
	for _, e := range entries {
		if !in[e.UUID] || e.Type != transcript.TypeUser {
			continue
		}
		for _, b := range e.Message.Blocks {
			if b.Type == core.BlockToolResult {
				results[b.ToolUseID] = b
			}
		}
	}
	var lines []string
	for _, e := range entries {
		if !in[e.UUID] || e.Type != transcript.TypeAssistant {
			continue
		}
		for _, b := range e.Message.Blocks {
			switch b.Type {
			case core.BlockText:
				if t := squash(b.Text); t != "" {
					lines = append(lines, "said: "+t)
				}
			case core.BlockToolUse:
				line := b.Name + " " + briefInput(b.Input)
				if r, ok := results[b.ID]; ok {
					head := resultHead(r)
					if r.IsError {
						head = "(error) " + head
					}
					line += " → " + head
				}
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	omitted := 0
	if len(lines)*digestMinLine > digestMaxChars {
		keep := digestMaxChars / digestMinLine
		first := keep / 3
		omitted = len(lines) - keep
		lines = append(append([]string{}, lines[:first]...), lines[len(lines)-(keep-first):]...)
	}
	per := min(max(digestMaxChars/len(lines), digestMinLine), digestMaxLine)
	var b strings.Builder
	for i, l := range lines {
		if i == len(lines)/3 && omitted > 0 && len(lines) > 2 {
			fmt.Fprintf(&b, "- … %d rounds left out …\n", omitted)
		}
		b.WriteString("- " + clip(l, per) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// squash is s on one line, its runs of space made one.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip cuts s to n runes.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// briefInput is a call's arguments on one short line: the command, the
// path or the code when it has one, else the arguments as they are.
func briefInput(in json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(in, &m) != nil {
		return clip(squash(string(in)), 140)
	}
	for _, k := range []string{"command", "cmd", "file_path", "path", "code", "pattern", "query", "url"} {
		if v, ok := m[k].(string); ok && v != "" {
			return clip(squash(v), 140)
		}
	}
	b, _ := json.Marshal(m)
	return clip(squash(string(b)), 140)
}

// resultHead is the first lines of what a call got back and its last
// line, on one line.
func resultHead(r core.Block) string {
	var ls []string
	for _, l := range strings.Split(core.TextOf(r.Content), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ls = append(ls, l)
		}
	}
	switch {
	case len(ls) == 0:
		return "(nothing)"
	case len(ls) <= 3:
		return squash(strings.Join(ls, " | "))
	}
	return squash(strings.Join(ls[:2], " | ") + " | … | " + ls[len(ls)-1])
}

// keepDigest writes text to mem as a thought, as the model's own call
// would, and reports whether mem took it. The call is made from the
// session's stack and is not one the host's model made.
func (s *Server) keepDigest(ctx context.Context, session, text string) bool {
	if s.opts.KeepCall == nil {
		return false
	}
	fail := func(why string) bool {
		if s.opts.Log != nil {
			s.opts.Log("gnosh bridge: the automatic keep failed: " + why)
		}
		return false
	}
	name, input := s.opts.KeepCall(text)
	t := s.byName[name]
	if t == nil {
		return fail("there is no tool " + name)
	}
	ss := s.session(session)
	call := core.ToolCall{ID: "gnosh-autokeep", Name: name, Input: input}
	res, err := t.Run(s.withSide(ctx), call, &core.ToolEnv{Cwd: s.opts.Project, Roots: []string{s.opts.Project}, SessionID: session, Store: ss.store})
	if err != nil {
		return fail(err.Error())
	}
	if res.IsError {
		return fail(clip(core.TextOf(res.Content), 300))
	}
	for _, l := range strings.Split(core.TextOf(res.Content), "\n") {
		if strings.HasPrefix(l, "failed · ") {
			return fail(clip(core.TextOf(res.Content), 300))
		}
	}
	return true
}

// autoKeepNote is what the model is told with the slide's line: the digest
// stands where the work stood, so it knows what mem holds of it.
func autoKeepNote(digest string) string {
	return "gnosh kept this much of the work that just left your context, as a thought in mem (mem.Show lists it), since you had kept none; it is the head of each call's result, not the whole of it, so run a call again if you need more:\n" + clip(digest, digestShowChars)
}

// digestPrefix heads what is kept in mem.
const digestPrefix = "Kept by gnosh from work that left the context, with no summary:\n"

var (
	// longTokenRe is a run of 20 or more characters with no space: a hash, a
	// key, an encoded value, or a long path.
	longTokenRe = regexp.MustCompile(`\S{20,}`)
	// secretWordRe is a word that names a secret, with its value.
	secretWordRe = regexp.MustCompile(`(?i)\b(pass(?:word|wd)?|secret|token|api[_-]?key|private[_-]?key)(\s*[:=]\s*)\S+`)
)

// shortened is the digest with what mem's secret check takes for a secret
// cut short: the value after a word that names one, and the end of any long
// unbroken run. mem refuses such text whole, so a digest it refused is kept
// again in this form.
func shortened(d string) string {
	d = secretWordRe.ReplaceAllString(d, "$1$2…")
	return longTokenRe.ReplaceAllStringFunc(d, func(t string) string { return string([]rune(t)[:12]) + "…" })
}

// keepWork keeps the digest d in mem, as is, or shortened when mem refused
// it. It returns what was kept, or "" when mem would not take it either way.
func (s *Server) keepWork(ctx context.Context, session, d string) string {
	if s.keepDigest(ctx, session, digestPrefix+d) {
		return d
	}
	if short := shortened(d); short != d && s.keepDigest(ctx, session, digestPrefix+short+"\n(long values shortened: mem would not keep them whole)") {
		return short
	}
	return ""
}
