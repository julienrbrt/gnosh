package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// Text that reaches the screen comes from three places: gnosh's own words,
// the model, and everything a tool read or ran. Only the first can be
// trusted not to hold a control sequence. The model's answer goes through
// the Markdown renderer, which lets none out. Everything else goes through
// the functions in this file before it is styled: every control character
// becomes a visible stand-in ("^[" for Esc), so that nothing a tool printed
// can move the cursor, change a colour or repaint the screen. The renderer
// would drop such sequences anyway; here they are shown, because a person
// approving a command should see what is in it.

// tabStop is how far apart the tab stops are in text shown as it is.
const tabStop = 8

// safeLine makes one line of outside text safe to draw: tabs become
// spaces up to the next tab stop, a carriage return at its end is dropped,
// and every other control character becomes its stand-in.
func safeLine(s string) string {
	s = strings.TrimSuffix(s, "\r")
	if strings.IndexByte(s, '\t') >= 0 {
		s = expandTabs(s)
	}
	return editor.Visible(s)
}

// expandTabs replaces each tab with spaces up to the next tab stop. An
// escape character is counted as the two cells its stand-in will take, so
// that the stops stay where they are once the line is made visible.
func expandTabs(s string) string {
	var b strings.Builder
	col := 0
	for rest := s; rest != ""; {
		cluster, w, after := text.NextGrapheme(rest)
		rest = after
		if cluster == "\t" {
			n := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		if w == 0 && len(cluster) == 1 && (cluster[0] < 0x20 || cluster[0] == 0x7f) {
			w = 2
		}
		b.WriteString(cluster)
		col += w
	}
	return b.String()
}

// safeLines splits outside text into lines and makes each safe to draw.
// Line feeds at the very end begin no further line.
func safeLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = safeLine(p)
	}
	return parts
}

// oneLine puts outside text on one line, safely: runs of blanks and line
// breaks become single spaces.
func oneLine(s string) string {
	return editor.Visible(strings.Join(strings.Fields(s), " "))
}

// firstLine is the first line of outside text that is not blank, made
// safe, and whether there is more after it.
func firstLine(s string) (line string, more bool) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			return safeLine(strings.TrimSpace(l)), i+1 < len(lines)
		}
	}
	return "", false
}

// wrap breaks a styled line into rows of at most width cells, with first
// in front of the first row and rest in front of the others. A row that
// could not be made to fit (a wide character in a terminal one cell wide)
// is cut.
func wrap(m text.Metrics, s string, width int, first, rest string) []string {
	width = max(width, 1)
	rows := m.Wrap(s, width, text.WrapOptions{First: first, Rest: rest})
	for i, r := range rows {
		if m.VisibleWidth(r) > width {
			rows[i] = m.Truncate(r, width, "")
		}
	}
	return rows
}

// fit cuts a styled line to width cells. Nothing is put where it was cut:
// the lines that are cut are the few-row previews of the live region, and
// what they stand for is shown whole when it is committed.
func fit(m text.Metrics, s string, width int) string {
	if m.VisibleWidth(s) <= width {
		return s
	}
	return m.Truncate(s, max(width, 0), "")
}

// statusText makes a line a status line command printed safe to draw. Its
// colours (SGR) and links (OSC 8) stay, as Claude Code allows; every other
// escape sequence and control character goes, a tab becomes a space, and
// a colour or a link that is open at the end is closed there, so that the
// line stands on its own.
func statusText(s string) string {
	var b strings.Builder
	styled, linked := false, false
	for rest := s; rest != ""; {
		tok, after := text.NextToken(rest)
		rest = after
		switch {
		case tok.Kind == text.Plain:
			for _, r := range tok.Text {
				switch {
				case r == '\t':
					b.WriteByte(' ')
				case r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0, bidiMark(r):
				default:
					b.WriteRune(r)
				}
			}
		case tok.Complete && tok.IsSGR():
			b.WriteString(tok.Text)
			styled = true
		case tok.IsHyperlink() && plainLink(tok.Text):
			b.WriteString(tok.Text)
			uri := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(tok.Text, "\x07"), "\x1b\\"), "\x9c")
			if i := strings.IndexByte(uri[4:], ';'); i >= 0 {
				linked = uri[4+i+1:] != ""
			}
		}
	}
	if linked {
		b.WriteString("\x1b]8;;\x1b\\")
	}
	if styled {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// plainLink reports whether a link's sequence holds nothing between its
// start and its end but printable characters: a control or C1 character
// inside it (U+009C) is one a terminal may take for the end, and what
// follows then acts on the screen (the status line review of 2026-11-19).
func plainLink(seq string) bool {
	body := strings.TrimPrefix(seq, "\x1b]8;")
	switch {
	case strings.HasSuffix(body, "\x1b\\"):
		body = strings.TrimSuffix(body, "\x1b\\")
	case strings.HasSuffix(body, "\x07"):
		body = strings.TrimSuffix(body, "\x07")
	}
	for _, r := range body {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 || bidiMark(r) {
			return false
		}
	}
	return true
}

// bidiMark reports whether r changes the order text is drawn in.
func bidiMark(r rune) bool {
	return r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x200e || r == 0x200f || r == 0x061c
}

// count writes a number with its noun: "1 line", "3 lines".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return grouped(n) + " " + many
}

// grouped writes a number with commas between its thousands.
func grouped(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

// tokens writes a count of tokens the short way: "812", "1.2k", "14k",
// "262k", "1.2M".
func tokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	case n < 1_000_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
}

// tokenCount is a count of tokens with its noun: "1 token", "14k tokens".
func tokenCount(n int) string {
	if n == 1 {
		return "1 token"
	}
	return tokens(n) + " tokens"
}

// span writes a length of time the way a person says it: "0s", "42s",
// "1m 05s", "2h 03m". Parts of a second are left out, except under one
// second, where a tool's time is worth a tenth.
func span(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		if d < 50*time.Millisecond {
			return "0s"
		}
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// showPath writes a path the short way: relative to the working directory
// when it lies inside it, and with "~" for the home directory otherwise.
// The result is safe to draw.
func showPath(cwd, home, p string) string {
	if p == "" {
		return ""
	}
	if cwd != "" && filepath.IsAbs(p) {
		if rel, err := filepath.Rel(cwd, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return editor.Visible(rel)
		}
	}
	return editor.Visible(tilde(home, p))
}

// tilde writes "~" for the home directory at the start of a path.
func tilde(home, p string) string {
	if home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
