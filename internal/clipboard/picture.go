package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// This file takes a picture off the clipboard of the machine gnosh runs on,
// for Ctrl+V in the prompt box, as Claude Code's Ctrl+V pastes one: as PNG,
// with the machine's own tool, osascript on macOS (which writes the picture
// to a file), wl-paste or xclip on a Linux desktop.

// ErrNoPicture says the clipboard holds no picture.
var ErrNoPicture = errors.New("the clipboard holds no picture")

// pictureMost is the largest picture taken off the clipboard.
const pictureMost = 64 << 20

// pngMark is how a PNG file begins.
var pngMark = []byte("\x89PNG\r\n\x1a\n")

// OverSSH reports whether the person came in over ssh, so that their
// clipboard is on another machine.
func OverSSH(getenv func(string) string) bool {
	return getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != ""
}

// PictureTool is the program, with its arguments, that hands out the
// picture on this machine's clipboard: nil when the person's terminal is on
// another machine, or this one has none. Its arguments are as Tool's.
func PictureTool(goos string, getenv func(string) string, look func(string) (string, error)) []string {
	if OverSSH(getenv) {
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
		return try("osascript")
	case "windows", "plan9", "js", "wasip1":
		return nil
	}
	if getenv("WAYLAND_DISPLAY") != "" {
		if tool := try("wl-paste", "--no-newline", "--type", "image/png"); tool != nil {
			return tool
		}
	}
	if getenv("DISPLAY") != "" {
		return try("xclip", "-selection", "clipboard", "-target", "image/png", "-out")
	}
	return nil
}

// ReadPicture takes the picture off the clipboard with tool, in the
// environment env (nil is this process's own), as PNG.
func ReadPicture(ctx context.Context, tool []string, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if filepath.Base(tool[0]) == "osascript" {
		return macPicture(ctx, tool[0], env)
	}
	cmd := exec.CommandContext(ctx, tool[0], tool[1:]...)
	cmd.Env = env
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, rerr := io.ReadAll(io.LimitReader(out, pictureMost+1))
	werr := cmd.Wait()
	switch {
	case len(data) > pictureMost:
		return nil, fmt.Errorf("the picture is larger than %d MiB", pictureMost>>20)
	case werr != nil && ctx.Err() != nil:
		// (Before the PNG's first bytes are looked at: a tool stopped
		// in the middle has handed out the start of a picture.)
		return nil, fmt.Errorf("%s took too long", filepath.Base(tool[0]))
	case rerr != nil:
		return nil, rerr
	case werr == nil && bytes.HasPrefix(data, pngMark):
		return data, nil
	}
	// (wl-paste and xclip fail when the clipboard has no PNG to give.)
	return nil, ErrNoPicture
}

// macPicture has osascript write the clipboard's picture, as PNG, to a
// file of its own, and reads it.
func macPicture(ctx context.Context, osascript string, env []string) ([]byte, error) {
	f, err := os.CreateTemp("", "gnosh-picture-*.png")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	lines := []string{
		"try",
		"set pic to (the clipboard as «class PNGf»)",
		"on error",
		`return "none"`,
		"end try",
		"set f to open for access (POSIX file " + appleString(path) + ") with write permission",
		"set eof f to 0",
		"write pic to f",
		"close access f",
		`return "ok"`,
	}
	var args []string
	for _, l := range lines {
		args = append(args, "-e", l)
	}
	cmd := exec.CommandContext(ctx, osascript, args...)
	cmd.Env = env
	said, err := cmd.Output()
	if strings.TrimSpace(string(said)) == "none" {
		return nil, ErrNoPicture
	}
	if err != nil {
		return nil, fmt.Errorf("osascript: %v", err)
	}
	if info, err := os.Stat(path); err == nil && info.Size() > pictureMost {
		return nil, fmt.Errorf("the picture is larger than %d MiB", pictureMost>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, pngMark) {
		return nil, ErrNoPicture
	}
	return data, nil
}

// appleString is s as an AppleScript string.
func appleString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
