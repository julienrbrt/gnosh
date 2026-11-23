// Package clipboard puts text on the clipboard of the machine gnosh runs
// on, with the machine's own tool: pbcopy on macOS; wl-copy, xclip or
// xsel on a Linux desktop. It is for when the person's terminal is on
// this machine (gnosh was not reached over ssh): /copy uses it, and so
// does gnosh ssh for what the far side copies. Apple's Terminal drops the
// terminal's own way of copying (OSC 52), so without it nothing reached
// the clipboard there.
package clipboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// wait is how long the tool is given.
const wait = 5 * time.Second

// Tool is the program, with its arguments, that puts text on this
// machine's clipboard: nil when the person's terminal is on another
// machine, or this one has none. goos is runtime.GOOS; getenv looks up
// the environment gnosh was given, and look finds a program on its PATH.
func Tool(goos string, getenv func(string) string, look func(string) (string, error)) []string {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != "" {
		return nil
	}
	try := func(name string, args ...string) []string {
		if path, err := look(name); err == nil {
			return append([]string{path}, args...)
		}
		return nil
	}
	switch goos {
	case "darwin":
		return try("pbcopy")
	case "windows", "plan9", "js", "wasip1":
		return nil
	}
	if getenv("WAYLAND_DISPLAY") != "" {
		if tool := try("wl-copy"); tool != nil {
			return tool
		}
	}
	if getenv("DISPLAY") != "" {
		if tool := try("xclip", "-selection", "clipboard"); tool != nil {
			return tool
		}
		return try("xsel", "--clipboard", "--input")
	}
	return nil
}

// Plain is text as it may go on a clipboard. What it is pasted into next
// may be a terminal, so its control characters go, but for tab and the
// line breaks: a carriage return becomes a newline, and goes before one.
// An escape sequence (ESC [201~ ends a bracketed paste) could otherwise
// have the rest of it taken as typed. /copy copies so (the TUI review of
// 2026-11-19), and so does gnosh ssh what the far machine copies, which
// went on this machine's clipboard as it came (the relay review of the
// same day).
func Plain(s string) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n':
			return r
		case r == '\r':
			return '\n'
		case r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0:
			return -1
		}
		return r
	}, s)
}

// rich names what pbcopy keeps text as, other than text, by how it
// begins: rich text or PostScript; "" for text.
func rich(text string) string {
	switch {
	case strings.HasPrefix(text, `{\rtf`):
		return "rich text (RTF)"
	case strings.HasPrefix(text, "%!PS-Adobe"):
		return "PostScript (EPS)"
	}
	return ""
}

// Copy runs tool with text on its standard input, in the environment env
// (as os.Environ gives it; nil is this process's own). pbcopy reads its
// input in the encoding of the locale, and where none is set in an
// encoding that is not UTF-8, so it is given a UTF-8 locale: the text is
// UTF-8. Text that begins as rich text or PostScript is not given to
// pbcopy, which would keep it as that, with its links and hidden text,
// and not as the text it is.
func Copy(ctx context.Context, tool []string, env []string, text string) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool[0], tool[1:]...)
	if filepath.Base(tool[0]) == "pbcopy" {
		if kind := rich(text); kind != "" {
			return fmt.Errorf("the text begins as %s does, which pbcopy would keep as that and not as text", kind)
		}
		if env == nil {
			env = os.Environ()
		}
		// (Of two values of one variable, the last counts.)
		env = append(env[:len(env):len(env)], "LC_ALL=en_US.UTF-8")
	}
	cmd.Env = env
	cmd.Stdin = strings.NewReader(text)
	// Its output goes nowhere, not into a pipe: wl-copy and xclip leave a
	// process behind to hand the text out, which would hold a pipe open.
	return cmd.Run()
}
