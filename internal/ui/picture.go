package ui

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/shellquote"
	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// This file is pictures in the prompt box, as Claude Code takes them
// (spec/cc/interactive.md 2.7). Ctrl+V pastes the picture on the clipboard
// of the machine gnosh runs on: the session keeps it, and the box mentions
// it as @image-N. A paste that is nothing but the paths of picture files,
// as a terminal pastes files dragged into it, becomes @ mentions of them.
// Either way the picture goes with the prompt as a picture.

// picturePaster is a session that takes a picture off the clipboard and
// keeps it (app's controls.PastePicture).
type picturePaster interface {
	PastePicture(ctx context.Context) (string, error)
}

// pastePicture is Ctrl+V in the prompt box. The clipboard is read away
// from the screen's loop, as a slash command's job is: a clipboard tool
// that takes its time (up to ten seconds) leaves the screen working.
func (s *session) pastePicture() {
	pp, ok := s.c.(picturePaster)
	if !ok {
		s.flash("Pasting a picture (Ctrl+V) is not joined to this build.")
		return
	}
	if s.pasting {
		s.flash("The clipboard is still being read.")
		return
	}
	s.pasting = true
	s.host.spawn(func() func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		name, err := pp.PastePicture(ctx)
		return func() {
			s.pasting = false
			s.pasted(name, err)
		}
	})
}

// pasted says how a paste of a picture went, and puts its mention in the
// box: unless the box has turned into a shell line or a search meanwhile,
// where the mention would be no mention, and is said instead.
func (s *session) pasted(name string, err error) {
	switch {
	case errors.Is(err, app.ErrClipboardFar):
		s.flash("Over ssh, the clipboard Ctrl+V would read is on your own machine, out of gnosh's reach. Put the picture in a file here and mention it with @.")
	case errors.Is(err, app.ErrNoClipboard):
		s.flash("gnosh finds no way to read this machine's clipboard (it uses wl-paste or xclip). Drag the picture's file in, or mention it with @.")
	case errors.Is(err, app.ErrNoPicture):
		s.flash("The clipboard holds no picture. Text is pasted with the terminal's own paste.")
	case err != nil:
		s.flash("The picture could not be pasted: " + err.Error() + ".")
	case s.ed.Mode() != editor.ModePrompt || s.ed.Searching():
		s.flash("The picture is kept as @" + name + ": mention it in a prompt to send it.")
	default:
		s.ed.Handle(term.Paste{Text: s.spaced("@" + name + " ")})
		s.flash("The picture goes with the prompt as @" + name + ".")
	}
}

// spaced puts a space before mentions that would otherwise follow the
// character before the cursor with none between: an @ glued to a word
// mentions nothing (app's mentionWord takes one at the start or after a
// space), though gnosh would have said the picture goes.
func (s *session) spaced(mentions string) string {
	text, at := s.ed.Text(), s.ed.Cursor()
	if at <= 0 || at > len(text) {
		return mentions
	}
	if r, _ := utf8.DecodeLastRuneInString(text[:at]); unicode.IsSpace(r) {
		return mentions
	}
	return " " + mentions
}

// pictureEndings are the endings of the picture files a paste of paths
// may name.
var pictureEndings = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// picturesAtOnce is the most picture files one paste of paths becomes
// mentions of: as many as one prompt brings (app's maxMentions).
const picturesAtOnce = 10

// picturePaths turns a paste that is nothing but the paths of picture
// files (as a terminal pastes files dragged into it: the spaces in them
// escaped, or quoted, or as file:// addresses, on one line or a line
// each) into @ mentions of them, and reports whether it was one. No more
// are taken than a prompt brings (app's maxMentions, 10): an eleventh
// would be said to go, and would not.
func (s *session) picturePaths(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 8192 {
		return "", false
	}
	var words []string
	for _, line := range strings.Split(text, "\n") {
		w, err := shellquote.Split(strings.TrimSuffix(line, "\r"))
		if err != nil {
			return "", false
		}
		words = append(words, w...)
	}
	if len(words) == 0 || len(words) > picturesAtOnce {
		return "", false
	}
	out := make([]string, 0, len(words))
	var paths []string
	for _, w := range words {
		p := w
		if strings.HasPrefix(p, "file://") {
			u, err := url.Parse(p)
			if err != nil {
				return "", false
			}
			p = u.Path
		}
		if strings.HasPrefix(p, "~/") && s.home != "" {
			p = filepath.Join(s.home, p[2:])
		}
		if !filepath.IsAbs(p) || !pictureEndings[strings.ToLower(filepath.Ext(p))] {
			return "", false
		}
		paths = append(paths, p)
		shown := p
		if s.home != "" && strings.HasPrefix(p, s.home+string(filepath.Separator)) {
			shown = "~/" + p[len(s.home)+1:]
		}
		if strings.ContainsAny(shown, "\"\n") {
			return "", false
		}
		if strings.ContainsAny(shown, " \t") {
			shown = `"` + shown + `"`
		}
		out = append(out, "@"+shown)
	}
	if !regularFiles(paths, time.Second) {
		return "", false
	}
	return strings.Join(out, " ") + " ", true
}

// regularFiles reports whether every path is a regular file that is
// there, giving up after wait: a path on a network mount that does not
// answer would otherwise hold the screen (the paste is then text).
func regularFiles(paths []string, wait time.Duration) bool {
	done := make(chan bool, 1)
	go func() {
		for _, p := range paths {
			if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
				done <- false
				return
			}
		}
		done <- true
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(wait):
		return false
	}
}
