package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/permission"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/tools/fstools"
)

// This file is the @ mentions of a prompt (spec/cc/interactive.md 2.5): a
// path written after @ in what a person sends puts what Read returns for
// that file, or a listing of a directory, in front of the model with the
// prompt, without a tool call, and the file then counts as read, so that
// it can be edited at once; the instruction files of its folders come with
// it, as after a Read. Read deny rules hold: a path one refuses is
// named and left out. A word after @ that names nothing on disk is left as
// it is: it may be a handle or an address. "@server:scheme://path" names a
// resource of a connected MCP server (spec/cc/mcp.md 5), and what it holds
// comes with the prompt. CLAUDE_CODE_DISABLE_FILE_MENTIONS=1 turns this
// off.

// MetaPasted, in a prompt's Meta, is where its text holds what the person
// pasted and the prompt box showed folded (a paste of more than ten lines
// or 800 characters), as [][2]int byte ranges of Text. The interface sets
// it; the model is told which lines were pasted (pastedLines).
const MetaPasted = "gnosh.pasted"

// metaVerbatim, set true in a prompt's Meta, delivers it as it is: no @
// mention brings anything (stream-json input's client_composed).
const metaVerbatim = "gnosh.verbatim"

// mentionWord finds "@path" and "@\"a path\"" at the start of a word.
var mentionWord = regexp.MustCompile(`(?:^|\s)@("[^"\n]+"|[^\s"]+)`)

// resourceMention is "@server:scheme://rest", a resource of an MCP server.
var resourceMention = regexp.MustCompile(`^([A-Za-z0-9_.-]+):([A-Za-z][A-Za-z0-9+.-]*://\S+)$`)

// maxMentions is how many paths one prompt brings, maxMentionWords how
// many of its @ words are looked up at all (a prompt of thousands of them
// is not held up by as many lookups, as in Claude Code), and maxListing
// how many names of a directory are listed.
const (
	maxMentions     = 10
	maxMentionWords = 100
	maxListing      = 200
)

// mentionLines is a PromptLines provider: the paths a typed prompt
// mentions, each with what it brings.
func (l *live) mentionLines(ctx context.Context, info agent.LineInfo) ([]agent.Part, error) {
	if verbatim, _ := info.Prompt.Meta[metaVerbatim].(bool); verbatim || info.Prompt.Hidden ||
		l.envOn("CLAUDE_CODE_DISABLE_FILE_MENTIONS") || !strings.Contains(info.Prompt.Text, "@") {
		return nil, nil
	}
	read := l.readTool()
	if read == nil {
		return nil, nil // no Read in this session: nothing is read for the model
	}
	readName := read.Spec().Name
	cwd := l.cwd()
	seen := map[string]bool{}
	var parts []agent.Part
	for _, m := range mentionWord.FindAllStringSubmatch(info.Prompt.Text, maxMentionWords) {
		raw := m[1]
		if strings.HasPrefix(raw, `"`) {
			raw = strings.Trim(raw, `"`)
		} else {
			raw = strings.TrimRight(raw, ".,;:!?)'") // the end of a sentence
		}
		if raw == "" || seen[raw] || len(seen) >= maxMentions {
			continue
		}
		if name, ok := strings.CutPrefix(raw, "agent-"); ok && l.agentKindOffered(name) {
			// A kind of sub-agent, named: the task goes to it.
			seen[raw] = true
			parts = append(parts, agent.Part{Source: mentionSource("agent", name), Text: l.texts.AgentMention(name)})
			continue
		}
		if m := resourceMention.FindStringSubmatch(raw); m != nil {
			// A resource of a connected MCP server that offers them.
			if servers, _ := l.resourceServers(m[1]); len(servers) > 0 {
				seen[raw] = true
				rctx, cancel := context.WithTimeout(ctx, l.cfg.Limits.MCPTimeout.V)
				res, err := servers[0].client.ReadResource(rctx, m[2])
				cancel()
				if err != nil {
					parts = append(parts, agent.Part{Source: mentionSource("failed", raw), Text: l.texts.Mention(prompt.MentionFailed, raw, err.Error())})
					continue
				}
				parts = append(parts, agent.Part{Source: mentionSource("resource", raw), Text: l.texts.Mention(prompt.MentionResource, raw, "") + "\n" + prompt.Untrusted(resourceText(res, m[2]))})
				continue
			}
		}
		path := raw
		switch {
		case l.pastedPicture(raw) != "":
			// A picture pasted into the box (picture.go).
			path = l.pastedPicture(raw)
		case path == "~" || strings.HasPrefix(path, "~/"):
			path = filepath.Join(l.cfg.Paths.Home, path[1:])
		case !filepath.IsAbs(path):
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		seen[raw] = true
		input, _ := json.Marshal(map[string]string{"file_path": path})
		call := core.ToolCall{ID: newCallID(), Name: readName, Input: input}
		act := core.Action{Kind: core.ActionRead, Paths: []string{path}}
		if d := l.engine.DecideAt(cwd, readName, call, act); d.Behavior == permission.Deny {
			parts = append(parts, agent.Part{Source: mentionSource("denied", raw), Text: l.texts.Mention(prompt.MentionDenied, raw, "")})
			continue
		}
		if st.IsDir() {
			text, err := listing(path)
			if err != nil {
				parts = append(parts, agent.Part{Source: mentionSource("failed", raw), Text: l.texts.Mention(prompt.MentionFailed, raw, err.Error())})
				continue
			}
			parts = append(parts, agent.Part{Source: mentionSource("dir", raw), Text: l.texts.Mention(prompt.MentionDir, raw, "") + "\n" + prompt.Untrusted(text)})
			continue
		}
		res, err := read.Run(ctx, call, l.env())
		switch {
		case err != nil:
			parts = append(parts, agent.Part{Source: mentionSource("failed", raw), Text: l.texts.Mention(prompt.MentionFailed, raw, err.Error())})
		case res.IsError:
			parts = append(parts, agent.Part{Source: mentionSource("failed", raw), Text: l.texts.Mention(prompt.MentionFailed, raw, core.TextOf(res.Content))})
		default:
			// (What the file holds is not the person's word: gnosh's own
			// tags in it are broken, as in what Read returns to a call.) A
			// picture Read returns goes along as the picture: "what is in
			// @shot.png?" shows the model the image, as a call of Read would.
			var images []core.Block
			for _, b := range res.Content {
				if b.Type == core.BlockImage {
					images = append(images, b)
				}
			}
			parts = append(parts, agent.Part{Source: mentionSource("file", raw), Text: l.texts.Mention(prompt.MentionFile, raw, "") + "\n" + prompt.Untrusted(core.TextOf(res.Content)), Images: images})
			// The instruction files of the file's folders come with it, as
			// they would after a Read.
			parts = append(parts, l.lateInstructions(ctx, agent.ToolDone{Call: call, Tool: read, Result: res})...)
		}
	}
	return parts, nil
}

// mentionSource is the Source of a mention's part: "mention:", what came
// of it (file, dir, denied, failed), ":" and the path as written, so that
// a front end can say under the prompt what each mention brought.
func mentionSource(kind, path string) string { return "mention:" + kind + ":" + path }

// agentKindOffered says the session has the Agent tool and a kind of
// sub-agent by that name.
func (l *live) agentKindOffered(name string) bool {
	if !slices.Contains(l.toolNames(), agentToolName) {
		return false
	}
	_, ok := l.agentDefs().Get(name)
	return ok
}

// AgentKinds names the kinds of sub-agent a prompt may name with
// @agent-, for a front end to offer: none when the session has no Agent
// tool.
func (c *controls) AgentKinds() []string {
	l := c.cur()
	if !slices.Contains(l.toolNames(), agentToolName) {
		return nil
	}
	return l.agentDefs().Names()
}

// readTool is the session's Read tool, or nil when it has none.
func (l *live) readTool() core.Tool {
	name := fstools.Defaults().ReadName
	for _, t := range l.tools {
		if t.Spec().Name == name {
			return t
		}
	}
	return nil
}

// listing lists a directory: its names, a directory's with a slash after
// it, at most maxListing of them.
func listing(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	more := ""
	if len(names) > maxListing {
		more = fmt.Sprintf("\n(and %d more)", len(names)-maxListing)
		names = names[:maxListing]
	}
	if len(names) == 0 {
		return "(empty)", nil
	}
	return strings.Join(names, "\n") + more, nil
}

// pastedLines tells the model which lines of a prompt the person pasted
// rather than typed: material they brought, such as a log, a file or
// someone else's words, whose requests are not theirs unless they say so.
// The prompt itself goes as it was typed. (Claude Code 2.1.328 marks
// pasted text for the model too.)
func (l *live) pastedLines(_ context.Context, info agent.LineInfo) ([]agent.Part, error) {
	ranges, _ := info.Prompt.Meta[MetaPasted].([][2]int)
	if len(ranges) == 0 || info.Prompt.Hidden {
		return nil, nil
	}
	text := info.Prompt.Text
	var where []string
	for _, r := range ranges {
		if r[0] < 0 || r[1] > len(text) || r[0] >= r[1] {
			continue
		}
		first := strings.Count(text[:r[0]], "\n") + 1
		last := first + strings.Count(strings.TrimRight(text[r[0]:r[1]], "\n"), "\n")
		if last == first {
			where = append(where, fmt.Sprintf("line %d", first))
		} else {
			where = append(where, fmt.Sprintf("lines %d to %d", first, last))
		}
	}
	if len(where) == 0 {
		return nil, nil
	}
	return []agent.Part{{Source: "pasted", Text: l.texts.Pasted(joinWords(where))}}, nil
}

// joinWords joins phrases as a sentence does: "a", "a and b", "a, b and c".
func joinWords(ws []string) string {
	if len(ws) < 2 {
		return strings.Join(ws, "")
	}
	return strings.Join(ws[:len(ws)-1], ", ") + " and " + ws[len(ws)-1]
}
