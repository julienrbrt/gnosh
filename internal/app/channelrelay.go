package app

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/agent"
)

// This file relays permission questions to channels, as Claude Code does
// (its channels-reference page, "Relay permission prompts"; DECISIONS
// 85). A channel named in --channels whose server declares
// claude/channel/permission besides claude/channel is sent each question
// about a tool call as it opens (notifications/claude/channel/
// permission_request): a request id of five lowercase letters without l,
// the tool's name, what the call does, and its input as display text. The
// server shows it to its person on another device, and sends back a
// verdict with the id (notifications/claude/channel/permission, "allow" or
// "deny"). The question stays open at the terminal all the while: the
// first answer, from either side, is the one that counts, and the other
// is dropped. A verdict is taken only for an open question, and only from
// a channel it went to.
//
// What is sent is made fit for another screen, as Claude Code makes it:
// characters that turn text around or cannot be seen are left out, quote
// and bracket lookalikes are written plainly, blanks are folded, a value
// longer than 3,500 characters keeps its start and its end around a count
// of what was left out, and the tokens services hand out as credentials
// are sent as [REDACTED].
//
// Where gnosh differs: a request id is never issued twice in a session
// (Claude Code issues ids at random and drops verdicts for one that came
// up twice), and some questions stay at the terminal, as trust and MCP
// consent do in both: plan approvals (ExitPlanMode), and the questions
// only the person may answer (QuestionData.PersonOnly: a call that throws
// away what cannot be got back, a removal that could destroy what has to
// be kept).

// relayed is a question put to the channels that relay them.
type relayed struct {
	question string
	tool     string
	// to are the channels it went to, by name.
	to map[string]bool
}

// relayBook is the session's relayed questions.
type relayBook struct {
	mu sync.Mutex
	// open holds the questions still open by request id, and ids the
	// request id of each by question id.
	open map[string]*relayed
	ids  map[string]string
	// issued is every request id issued in the session.
	issued map[string]bool
}

// idLetters are the letters of a request id: a to z without l.
const idLetters = "abcdefghijkmnopqrstuvwxyz"

// issue makes a request id for the question that has never been issued in
// the session, and books it.
func (b *relayBook) issue(r *relayed) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open == nil {
		b.open, b.ids, b.issued = map[string]*relayed{}, map[string]string{}, map[string]bool{}
	}
	for {
		var raw [5]byte
		_, _ = rand.Read(raw[:])
		id := make([]byte, 5)
		for i, x := range raw {
			id[i] = idLetters[int(x)%len(idLetters)]
		}
		if s := string(id); !b.issued[s] {
			b.issued[s] = true
			b.open[s] = r
			b.ids[r.question] = s
			return s
		}
	}
}

// take takes the open question a verdict from the channel names, and
// reports false when there is none it went to.
func (b *relayBook) take(id, channel string) (*relayed, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.open[id]
	if r == nil || !r.to[channel] {
		return nil, false
	}
	delete(b.open, id)
	delete(b.ids, r.question)
	return r, true
}

// close forgets the question with the given id, answered or taken back.
func (b *relayBook) close(question string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id, ok := b.ids[question]; ok {
		delete(b.open, id)
		delete(b.ids, question)
	}
}

// relayers are the session's connected channels that take permission
// questions.
func (l *live) relayers() map[string]func(id, tool, description, preview string) error {
	out := map[string]func(id, tool, description, preview string) error{}
	l.mcp.mu.Lock()
	defer l.mcp.mu.Unlock()
	for _, s := range l.mcp.servers {
		if s.status != "connected" || s.client == nil || s.cfg.Type == "http" || !l.channelNamed(s.cfg.Name) {
			continue
		}
		if info := s.client.Info(); info.Channel && info.ChannelPermission {
			out[s.cfg.Name] = s.client.RelayPermission
		}
	}
	return out
}

// relayEvent follows the questions as they open and close, for the
// channels that relay them.
func (l *live) relayEvent(ev any) {
	switch ev := ev.(type) {
	case *agent.QuestionEvent:
		l.relayQuestion(ev.Question)
	case *agent.AnswerEvent:
		l.relay.close(ev.ID)
	}
}

// relayQuestion puts a question about a tool call to the channels that
// relay them.
func (l *live) relayQuestion(q agent.Question) {
	if q.Kind != "permission" || q.Tool == "" || q.Tool == exitPlanName || len(l.inv.Channels) == 0 {
		return
	}
	data, _ := q.Data.(QuestionData)
	if data.PersonOnly {
		// (What only the person may approve is asked at the terminal.)
		return
	}
	to := l.relayers()
	if len(to) == 0 {
		return
	}
	r := &relayed{question: q.ID, tool: q.Tool, to: map[string]bool{}}
	for name := range to {
		r.to[name] = true
	}
	id := l.relay.issue(r)
	description, preview := relayDescription(q, data.Input), relayPreview(data.Input)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for name := range to {
			if !yield(name) {
				return
			}
		}
	}) {
		send := to[name]
		go func() {
			if err := send(id, q.Tool, description, preview); err != nil {
				l.flog.logf("channel", "the question about %s could not be sent to %s: %v", q.Tool, name, err)
			}
		}()
	}
}

// channelVerdict takes a channel's answer to a question it was sent.
func (l *live) channelVerdict(channel, id, behavior string) {
	r, ok := l.relay.take(id, channel)
	if !ok || l.agent == nil {
		l.flog.logf("channel", "a verdict from %s for %s was dropped: no open question of that id went there", channel, id)
		return
	}
	choice := "no"
	if behavior == "allow" {
		choice = "yes"
	}
	// (An error is a question answered at the terminal a moment before.
	// The interface says, as the question ends, who answered it.)
	if err := l.agent.Answer(r.question, agent.Answer{Choice: choice, By: "the channel " + channel}); err == nil {
		l.flog.logf("channel", "%s answered the question about %s: %s", channel, r.tool, choice)
	}
}

// relayDescription says what a call does, for a channel: for a command,
// the model's description of it, never the command itself (that is in the
// preview); for another tool, the line it gives of the call.
func relayDescription(q agent.Question, input json.RawMessage) string {
	if q.Tool == "Bash" {
		var in struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(input, &in)
		return firstOf(relayText(in.Description), "Run shell command")
	}
	if q.Action != nil {
		if s := relayText(q.Action.Summary); s != "" {
			return s
		}
	}
	return relayText(firstOf(q.Title, "Use "+q.Tool))
}

// relayPreview is a call's input as display text for a channel: a JSON
// object of its fields, each value made fit with relayValue. A value's
// line ends stay in it, written as JSON writes them (\n), so a command of
// two lines does not read as one.
func relayPreview(input json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil || fields == nil {
		return relayText(string(input))
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	b.WriteByte('{')
	for i, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range fields {
			if !yield(k) {
				return
			}
		}
	}) {
		var value string
		if json.Unmarshal(fields[k], &value) != nil {
			var compact bytes.Buffer
			if json.Compact(&compact, fields[k]) == nil {
				value = compact.String()
			} else {
				value = "(value unserializable)"
			}
		}
		if i > 0 {
			b.WriteByte(',')
		}
		_ = enc.Encode(relayText(k))
		b.Truncate(b.Len() - 1) // (Encode's line end)
		b.WriteByte(':')
		_ = enc.Encode(relayValue(value))
		b.Truncate(b.Len() - 1)
	}
	b.WriteByte('}')
	return b.String()
}

// relayMax is the most characters of one value a channel is sent whole.
const relayMax = 3500

// lookalikes are the characters written plainly in what a channel is
// sent: the quotes and brackets that look like ASCII's.
var lookalikes = strings.NewReplacer(
	"\u201C", `"`, "\u201D", `"`, "\u201E", `"`, "\u201F", `"`, "\uFF02", `"`, "\u2033", `"`, "\u00AB", `"`, "\u00BB", `"`,
	"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'", "\uFF07", "'", "\u2032", "'",
	"\u2039", "<", "\u203A", ">", "\u2329", "<", "\u232A", ">", "\u27E8", "<", "\u27E9", ">", "\u3008", "<", "\u3009", ">", "\uFF1C", "<", "\uFF1E", ">", "\uFE64", "<", "\uFE65", ">",
)

// relayText makes a line fit to be sent to a channel's person: what
// relayValue does, with every run of blanks and line ends folded into one
// space.
func relayText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, relayClean(s))
	return elide(strings.Join(strings.Fields(s), " "))
}

// relayValue makes a value fit to be sent to a channel's person:
// characters that turn text around or cannot be seen left out (first, so
// that one inside a credential does not hide it), lookalikes written
// plainly, credentials masked, and the middle of a long value left out
// with a count of it. Line ends and other control characters stay, for
// the JSON they go in to write as escapes.
func relayValue(s string) string {
	return elide(relayClean(s))
}

// relayClean is relayValue but for the eliding.
func relayClean(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Bidi_Control, r), unicode.Is(unicode.Cf, r), r == '\u115F', r == '\u1160', r == '\u3164', r == '\uFFA0':
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "\uFFFD"))
	return maskCredentials(lookalikes.Replace(s))
}

// elide keeps the start and the end of a text longer than relayMax
// characters, around a count of what was left out.
func elide(s string) string {
	if n := utf8.RuneCountInString(s); n > relayMax {
		r := []rune(s)
		const keep = relayMax / 2
		s = string(r[:keep]) + fmt.Sprintf(" ⋯ %d code points elided ⋯ ", n-2*keep) + string(r[n-keep:])
	}
	return s
}

// credentialPrefixes begin the tokens services hand out as credentials.
var credentialPrefixes = []string{
	"sk-", "sk_live_", "sk_test_", "rk_live_", "rk_test_",
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
	"xoxb-", "xoxp-", "xoxa-", "xoxr-", "xapp-",
	"hf_", "npm_", "pypi-", "dop_v1_", "shpat_",
	"AKIA", "ASIA", "AIza",
}

// maskCredentials writes [REDACTED] for each token in s that begins with
// one of credentialPrefixes and has 8 characters or more after it, one of
// them a digit. A token is letters, digits, "_" and "-", so what is masked
// never holds a path's, a URL's or a shell's characters: a token that is a
// piece of a path or a file's name (a "/" or "\\" beside it, or a "." and
// a name after it) is left whole, and what a call does stays readable. A
// token in a URL's user part or query (x:TOKEN@host, ?key=TOKEN&) is
// masked, and the place it goes to is still there to read.
func maskCredentials(s string) string {
	tokenRune := func(r rune) bool {
		return r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !tokenRune(r) {
			i += size
			continue
		}
		j := i
		for j < len(s) {
			r, size := utf8.DecodeRuneInString(s[j:])
			if !tokenRune(r) {
				break
			}
			j += size
		}
		pathPiece := i > 0 && (s[i-1] == '/' || s[i-1] == '\\' || s[i-1] == '.') ||
			j < len(s) && (s[j] == '/' || s[j] == '\\' || s[j] == '.' && j+1 < len(s) && tokenRune(rune(s[j+1])))
		if tok := s[i:j]; credential(tok) && !pathPiece {
			b.WriteString(s[last:i])
			b.WriteString("[REDACTED]")
			last = j
		}
		i = j
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// credential reports whether tok is a token a service hands out as a
// credential.
func credential(tok string) bool {
	for _, p := range credentialPrefixes {
		rest, ok := strings.CutPrefix(tok, p)
		if ok && len(rest) >= 8 && strings.ContainsAny(rest, "0123456789") {
			return true
		}
	}
	return false
}
