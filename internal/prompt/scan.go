package prompt

import (
	"regexp"
	"sort"
	"strings"
)

// This file is the scan of a sub-agent's report (spec/cc/subagents.md 8)
// before the model that started the sub-agent reads it. A sub-agent reads
// files and web pages, and what it read can find its way into its report:
// a tag written like a harness's notes (gnosh's own two, or the
// <system-reminder> of another harness), a chat template's marker of a
// turn, or a line that begins as a turn of a conversation does ("Human:",
// "Assistant:"). Each gets a backslash in front, so that it reads as
// quoted text, and the scan says what it found, so that a note can stand
// in front of the report; it also finds the names of what turns permission
// checks off, which it leaves as they are. Nothing is taken out. It is a
// speed bump, not a filter: what keeps a report from speaking for gnosh is
// Untrusted, which breaks gnosh's tags on the way, and the word of the
// system prompt that what a tool returns is data.

// Imitation is what ScanReport found in a report, each thing once.
type Imitation struct {
	// Marked says what was written like a harness's notes or a
	// conversation's turns, and got a backslash, in the order it first
	// came: "gnosh tag", "im_start marker", `"Human:" line`. Each is plain
	// words, which may stand inside one of gnosh's own lines.
	Marked []string
	// Bypass names what turns permission checks off, as gnosh writes it:
	// "bypassPermissions", "--dangerously-skip-permissions", "--yolo".
	Bypass []string
}

// Found reports whether the scan found anything.
func (m Imitation) Found() bool { return len(m.Marked) > 0 || len(m.Bypass) > 0 }

// harnessTags are the names of the tags that read as a harness's notes.
var harnessTags = []string{tagName, memTag, agentMemoryTag, "system-reminder"}

// turnLabels begin a line that reads as a turn of a conversation.
var turnLabels = []string{"Human:", "Assistant:"}

// turnWords are the bar-less markers of a turn in chat templates.
var turnWords = []string{"start_of_turn>", "end_of_turn>"}

// barMarker is a chat template's marker between bars, after its "<":
// "|im_start|>".
var barMarker = regexp.MustCompile(`^\|([A-Za-z0-9_.-]{1,40})\|>`)

// bypassNames are what turns permission checks off: the name to look for,
// in any letter case, and the name to say.
var bypassNames = [][2]string{
	{"bypasspermissions", "bypassPermissions"},
	{"dangerously-skip-permissions", "--dangerously-skip-permissions"},
	{"--yolo", "--yolo"},
}

// ScanReport puts a backslash in front of each place in s, a sub-agent's
// report, that is written like a harness's notes or a conversation's
// turns, and says what it found. A place that already has a backslash in
// front is left as it is and not counted, so scanning twice is the same as
// scanning once. Text that holds none of these comes back as it was.
func ScanReport(s string) (string, Imitation) {
	type spot struct {
		at   int
		what string
	}
	var spots []spot
	// Lines that begin as a turn does, after any blanks.
	for start := 0; start < len(s); {
		end := strings.IndexByte(s[start:], '\n')
		if end < 0 {
			end = len(s)
		} else {
			end += start
		}
		line := s[start:end]
		lead := len(line) - len(strings.TrimLeft(line, " \t"))
		for _, label := range turnLabels {
			if strings.HasPrefix(line[lead:], label) {
				spots = append(spots, spot{start + lead, `"` + label + `" line`})
			}
		}
		start = end + 1
	}
	// Tags and markers, each from its "<".
	for i := 0; ; {
		at := strings.IndexByte(s[i:], '<')
		if at < 0 {
			break
		}
		i += at + 1
		if what := imitates(s[i:]); what != "" {
			spots = append(spots, spot{i - 1, what})
		}
	}
	var m Imitation
	lower := strings.ToLower(s)
	for _, n := range bypassNames {
		if strings.Contains(lower, n[0]) {
			m.Bypass = append(m.Bypass, n[1])
		}
	}
	sort.SliceStable(spots, func(a, b int) bool { return spots[a].at < spots[b].at })
	var b strings.Builder
	kept, changed := 0, false
	for _, p := range spots {
		if p.at > 0 && s[p.at-1] == '\\' || changed && p.at == kept {
			continue
		}
		if !changed {
			b.Grow(len(s) + 8)
			changed = true
		}
		b.WriteString(s[kept:p.at])
		b.WriteByte('\\')
		kept = p.at
		if !contains(m.Marked, p.what) {
			m.Marked = append(m.Marked, p.what)
		}
	}
	if !changed {
		return s, m
	}
	b.WriteString(s[kept:])
	return b.String(), m
}

// imitates says what rest, the text that follows a "<", is written like:
// a harness's tag ("gnosh tag") or a chat template's marker of a turn
// ("im_start marker"), or "" for neither.
func imitates(rest string) string {
	if m := barMarker.FindStringSubmatch(rest); m != nil {
		return m[1] + " marker"
	}
	if strings.HasPrefix(rest, fullBar) {
		inner := rest[len(fullBar):]
		if end := strings.Index(inner, fullBar+">"); end > 0 && end <= 120 {
			return plainName(inner[:end]) + " marker"
		}
	}
	for _, w := range turnWords {
		if strings.HasPrefix(rest, w) {
			return strings.TrimSuffix(w, ">") + " marker"
		}
	}
	for _, name := range harnessTags {
		if tagNamed(rest, name) {
			return name + " tag"
		}
	}
	return ""
}

// plainName is the name between the full-width bars of a marker, in
// letters, digits and "_" only, so that it can stand in gnosh's own words:
// "begin▁of▁sentence" becomes "begin_of_sentence".
func plainName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 40 {
			break
		}
	}
	return b.String()
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
