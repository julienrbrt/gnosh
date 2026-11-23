package app

import (
	"encoding/base64"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A picture mentioned in a prompt goes to the model as the picture, with
// the words a mention brings, as a call of Read would bring it.
func TestMentionedImage(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "A dot."})
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	w.Write("shot.png", string(png))
	run(t, w, "-p", "what is in @shot.png?").wantCode(t, 0)
	body := string(w.Model.Requests()[0].Body)
	has(t, "the first request", body, "The user mentioned the file shot.png", "image/png", "iVBORw0KGgo")
}
