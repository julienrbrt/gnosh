package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"github.com/gnosh-ai/gnosh/internal/clipboard"
)

// This file is the picture the person pastes into the prompt box with
// Ctrl+V, as Claude Code's Ctrl+V pastes one (spec/cc/interactive.md 2.7):
// the picture on this machine's clipboard is kept in the session's folder
// as pictures/image-N.png, and the box mentions it as @image-N, which the
// @ mentions read as that file, and send as a picture. The name stays good
// for the session, a resumed one too.

// ErrNoPicture says the clipboard holds no picture; ErrNoClipboard that
// this machine has no clipboard gnosh can read (no tool for it is
// installed); ErrClipboardFar that the person came over ssh, and their
// clipboard is on their own machine.
var (
	ErrNoPicture    = clipboard.ErrNoPicture
	ErrNoClipboard  = errors.New("no clipboard of this machine can be read")
	ErrClipboardFar = errors.New("the clipboard is on the machine ssh came from")
)

// pictureName is the name a pasted picture is mentioned by.
var pictureName = regexp.MustCompile(`^image-[0-9]{1,6}$`)

// pictureEndings are the file endings a kept picture may have.
var pictureEndings = []string{".png", ".jpg", ".gif", ".webp"}

// picturesDir is where the session keeps the pictures pasted into it.
func (l *live) picturesDir() string {
	return filepath.Join(l.st.dirs(l.ts.ID()).Session, "pictures")
}

// pastedPicture is the file of the pasted picture a mention names, or "".
func (l *live) pastedPicture(name string) string {
	if !pictureName.MatchString(name) {
		return ""
	}
	for _, ending := range pictureEndings {
		p := filepath.Join(l.picturesDir(), name+ending)
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// keepPicture keeps a picture in the session's folder under the next free
// name, and returns the name.
func (l *live) keepPicture(data []byte, ending string) (string, error) {
	dir := l.picturesDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for n := 1; n < 1_000_000; n++ {
		name := fmt.Sprintf("image-%d", n)
		if l.pastedPicture(name) != "" {
			continue
		}
		f, err := os.OpenFile(filepath.Join(dir, name+ending), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(f.Name())
			return "", err
		}
		return name, nil
	}
	return "", errors.New("the session holds too many pictures")
}

// PastePicture takes the picture off the clipboard of the machine gnosh
// runs on, keeps it in the session's folder, and returns the name a
// prompt mentions it by ("image-1"). ErrNoClipboard and ErrNoPicture say
// why there is none.
func (c *controls) PastePicture(ctx context.Context) (string, error) {
	l := c.cur()
	if clipboard.OverSSH(l.p.getenv) {
		return "", ErrClipboardFar
	}
	tool := clipboard.PictureTool(runtime.GOOS, l.p.getenv, l.p.lookPath)
	if tool == nil {
		return "", ErrNoClipboard
	}
	data, err := clipboard.ReadPicture(ctx, tool, l.p.o.Environ)
	if err != nil {
		return "", err
	}
	return l.keepPicture(data, ".png")
}
