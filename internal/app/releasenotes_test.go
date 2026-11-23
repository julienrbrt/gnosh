package app

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/buildinfo"
)

// /release-notes lists the changes a build was made from; a build of the
// tree as it stands, as the tests are, carries none and says so.
func TestReleaseNotes(t *testing.T) {
	w := apptest.NewWorld(t)
	r := run(t, w, "-p", "/release-notes").wantCode(t, 0)
	has(t, "/release-notes", r.out, "carries no list of its changes")
}

// After /restart into a newer build, the changes since the old one: their
// count (a floor when the old build is older than the list reaches) and the
// newest few.
func TestWhatsNewText(t *testing.T) {
	var changes []buildinfo.Change
	for i := range 10 {
		changes = append(changes, buildinfo.Change{ID: strings.Repeat(string(rune('a'+i)), 7), Subject: "change " + string(rune('A'+i))})
	}
	got := whatsNewText("0123abc", changes, true)
	for _, want := range []string{"took over from 0123abc: 10 changes since.", "aaaaaaa change A", "hhhhhhh change H", "… and 2 more: /release-notes lists them."} {
		if !strings.Contains(got, want) {
			t.Errorf("whatsNewText lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "iiiiiii") {
		t.Errorf("whatsNewText lists more than it shows:\n%s", got)
	}
	if got := whatsNewText("0123abc", changes[:1], true); !strings.Contains(got, "1 change since.") {
		t.Errorf("one change: %s", got)
	}
	if got := whatsNewText("0123abc", changes[:3], false); !strings.Contains(got, "more than 3 changes since.") {
		t.Errorf("an old build beyond the list: %s", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Errorf("an empty line in the note: %q", got)
	}
}
