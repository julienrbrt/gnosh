package app

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// Ctrl+V's picture: a clipboard tool on the PATH (a stand-in for xclip
// here) hands out a PNG, the session keeps it as image-1 in its folder,
// and a prompt that mentions @image-1 sends it as the picture. The next
// one is image-2; with no display there is no clipboard to read.
func TestPastePicture(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "A dot."})
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	pic := w.Write("../clip/pic.png", string(png))
	w.Script("../fakebin/xclip", "cat "+pic)
	w.PathFirst = append(w.PathFirst, filepath.Dir(w.Path("../fakebin/xclip")))
	type paster interface {
		PastePicture(ctx context.Context) (string, error)
	}
	var kept string
	attach(t, w, func(o *Options) { o.Environ = append(o.Environ, "DISPLAY=:0") }, func(ctx context.Context, c SessionControls, f *scripted) int {
		pp, ok := c.(paster)
		if !ok {
			t.Error("the session's controls take no picture")
			return 1
		}
		name, err := pp.PastePicture(ctx)
		if err != nil || name != "image-1" {
			t.Errorf("PastePicture = %q, %v", name, err)
			return 1
		}
		if name, err = pp.PastePicture(ctx); err != nil || name != "image-2" {
			t.Errorf("the second PastePicture = %q, %v", name, err)
		}
		kept = c.(*controls).cur().pastedPicture("image-1")
		if _, err := c.Submit(agent.Prompt{Text: "what is in @image-1?"}); err != nil {
			t.Errorf("Submit: %v", err)
			return 1
		}
		find(f, "the turn's end", func(e *agent.TurnEndEvent) bool { return true })
		return 0
	})
	if data, err := os.ReadFile(kept); err != nil || string(data) != string(png) {
		t.Errorf("the kept picture %s: %v", kept, err)
	}
	body := string(w.Model.Requests()[0].Body)
	has(t, "the request", body, "image/png", "iVBORw0KGgo")

	// No display, no clipboard.
	v := apptest.NewWorld(t)
	attach(t, v, nil, func(ctx context.Context, c SessionControls, f *scripted) int {
		if _, err := c.(paster).PastePicture(ctx); !errors.Is(err, ErrNoClipboard) {
			t.Errorf("with no display: %v, want ErrNoClipboard", err)
		}
		return 0
	})
}
