package deliver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestRegister makes a register in a new project folder, sealed with a
// key of the test's.
func newTestRegister(t *testing.T) *register {
	t.Helper()
	project := filepath.Join(t.TempDir(), "proj", ".gnosh")
	return newRegister(filepath.Join(project, folderName), bytes.Repeat([]byte{7}, 32), nil, nil)
}

// file writes a file into a new directory of the test's and returns its
// path.
func file(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A delivered file gets a token of 32 random bytes; the same file again
// keeps it, another file gets another; and the register is a file of the
// project's data folder that only its owner reads, in a folder git leaves
// alone.
func TestRegisterTokens(t *testing.T) {
	r := newTestRegister(t)
	video := file(t, "ad.mp4", "a video")
	first, err := r.add(t.Context(), "", video, video, "hebrew ad video")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.Token)
	if err != nil || len(raw) != 32 || len(first.Token) != 43 {
		t.Fatalf("the token %q is not 32 bytes in unpadded URL-safe base64 (%v)", first.Token, err)
	}
	if first.Name != "ad.mp4" || first.File != video || first.Label != "hebrew ad video" {
		t.Errorf("the entry: %+v", first)
	}

	again, err := r.add(t.Context(), "", video, video, "the ad, again")
	if err != nil || again.Token != first.Token || again.Label != "the ad, again" || !again.Added.Equal(first.Added) {
		t.Errorf("the same file delivered again: %+v (%v), want the token and the day it was first delivered kept", again, err)
	}
	other, err := r.add(t.Context(), "render", "renders/ad.mp4", video, "from the render box")
	if err != nil || other.Token == first.Token || other.Token == "" {
		t.Errorf("another file got the token %q (%v)", other.Token, err)
	}
	if served, foreign, err := r.count(); served != 2 || foreign != 0 || err != nil {
		t.Errorf("the register counts %d entries and %d of someone else's (%v), want 2 and 0", served, foreign, err)
	}

	// Found by its token, and by what was delivered.
	if rec, ok := r.byToken(first.Token); !ok || rec.File != video {
		t.Errorf("by its token: %+v, %v", rec, ok)
	}
	if rec, ok := r.byTarget("render", "renders/ad.mp4"); !ok || rec.Token != other.Token {
		t.Errorf("by its machine and path: %+v, %v", rec, ok)
	}
	for _, token := range []string{"", "nope", first.Token + "x", strings.ToLower(first.Token)} {
		if token == first.Token {
			continue
		}
		if _, ok := r.byToken(token); ok {
			t.Errorf("the token %q found an entry", token)
		}
	}

	// On disk.
	st, err := os.Stat(r.path())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the register's file: %v, mode %v, want one only its owner reads", err, st.Mode())
	}
	for _, ignore := range []string{filepath.Join(filepath.Dir(r.dir), ".gitignore"), filepath.Join(r.dir, ".gitignore")} {
		if data, err := os.ReadFile(ignore); err != nil || string(data) != "*\n" {
			t.Errorf("%s holds %q (%v), want the line that keeps the folder out of git", ignore, data, err)
		}
	}
	if st, err := os.Stat(r.dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("the folder's mode is %v (%v)", st.Mode(), err)
	}
}

// An entry that is dropped is not served any more; the others stay, and
// so does what someone else wrote.
func TestRegisterDrop(t *testing.T) {
	r := newTestRegister(t)
	if dropped, err := r.drop(t.Context(), "render", "a.mp4"); dropped || err != nil {
		t.Errorf("dropping from a register that is not there: %v, %v", dropped, err)
	}
	copy1, copy2 := file(t, "a.mp4", "a"), file(t, "b.mp4", "b")
	a, err := r.add(t.Context(), "render", "a.mp4", copy1, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.add(t.Context(), "render", "b.mp4", copy2, "b")
	if err != nil {
		t.Fatal(err)
	}
	if dropped, err := r.drop(t.Context(), "web", "a.mp4"); dropped || err != nil {
		t.Errorf("dropping a file that was never delivered: %v, %v", dropped, err)
	}
	if dropped, err := r.drop(t.Context(), "render", "a.mp4"); !dropped || err != nil {
		t.Fatalf("dropping a delivered file: %v, %v", dropped, err)
	}
	if _, ok := r.byToken(a.Token); ok {
		t.Errorf("a dropped entry is still served")
	}
	if _, ok := r.byToken(b.Token); !ok {
		t.Errorf("another entry went with the one that was dropped")
	}
	if dropped, _ := r.drop(t.Context(), "render", "b.mp4"); !dropped {
		t.Errorf("the last entry was not dropped")
	}
	if served, foreign, err := newRegister(r.dir, r.key, nil, nil).count(); served != 0 || foreign != 0 || err != nil {
		t.Errorf("an emptied register counts %d and %d (%v)", served, foreign, err)
	}
	// The same file delivered anew gets a new token.
	again, err := r.add(t.Context(), "render", "a.mp4", copy1, "a")
	if err != nil || again.Token == a.Token {
		t.Errorf("a file delivered anew after it was dropped: %+v, %v", again, err)
	}
}

// A second register on the same folder, which is what a later session and
// another session of the project are, reads what the first wrote, and sees
// what is added while it runs.
func TestRegisterIsReadBack(t *testing.T) {
	r := newTestRegister(t)
	video := file(t, "ad.mp4", "a video")
	first, err := r.add(t.Context(), "", video, video, "ad")
	if err != nil {
		t.Fatal(err)
	}
	second := newRegister(r.dir, r.key, nil, nil)
	if rec, ok := second.byToken(first.Token); !ok || rec.File != video {
		t.Fatalf("a second register does not find the first one's entry: %+v, %v", rec, ok)
	}
	page := file(t, "index.html", "<p>hello</p>")
	// The file's time is what tells a change, so the second write is made
	// to differ in it whatever the file system's clock does.
	added, err := second.add(t.Context(), "", page, page, "page")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(r.path(), later, later); err != nil {
		t.Fatal(err)
	}
	if rec, ok := r.byToken(added.Token); !ok || rec.File != page {
		t.Errorf("the first register does not see what the second added: %+v, %v", rec, ok)
	}
	if _, ok := r.byToken(first.Token); !ok {
		t.Errorf("the first entry was lost when the second register wrote")
	}
}

// An entry that this account's gnosh did not write is not served: one
// sealed with another key, one whose file was changed after it was
// sealed, one with no seal. A repository that brings a register of its own
// brings nothing that is served. Such entries are left in the file.
func TestRegisterServesOnlySealedEntries(t *testing.T) {
	r := newTestRegister(t)
	video := file(t, "ad.mp4", "a video")
	mine, err := r.add(t.Context(), "", video, video, "mine")
	if err != nil {
		t.Fatal(err)
	}

	// What somebody else would write into the file.
	stranger := newRegister(r.dir, bytes.Repeat([]byte{9}, 32), nil, nil)
	theirs := record{Token: "theirtokentheirtokentheirtokentheirtokenthe", Name: "id_ed25519", Path: "/home/jae/.ssh/id_ed25519", File: "/home/jae/.ssh/id_ed25519"}
	theirs.Seal = stranger.seal(theirs)
	moved := mine
	moved.Token, moved.File = "movedtokenmovedtokenmovedtokenmovedtokenmov", "/etc/passwd"
	bare := record{Token: "baretokenbaretokenbaretokenbaretokenbaretok", Name: "x", Path: "/x", File: "/etc/hosts"}
	var kept registerFile
	data, err := os.ReadFile(r.path())
	if err != nil || json.Unmarshal(data, &kept) != nil {
		t.Fatal(err)
	}
	kept.Files = append(kept.Files, theirs, moved, bare)
	data, _ = json.Marshal(kept)
	if err := os.WriteFile(r.path(), data, 0o600); err != nil {
		t.Fatal(err)
	}

	fresh := newRegister(r.dir, r.key, nil, nil)
	if served, foreign, err := fresh.count(); served != 1 || foreign != 3 || err != nil {
		t.Fatalf("the register counts %d entries to serve and %d of someone else's (%v), want 1 and 3", served, foreign, err)
	}
	for _, token := range []string{theirs.Token, moved.Token, bare.Token} {
		if rec, ok := fresh.byToken(token); ok {
			t.Errorf("the entry %+v, which this account's gnosh did not write, is served", rec)
		}
	}
	if _, ok := fresh.byToken(mine.Token); !ok {
		t.Errorf("the account's own entry is not served")
	}
	if _, ok := fresh.byTarget("", "/home/jae/.ssh/id_ed25519"); ok {
		t.Errorf("a stranger's entry is found by what it names")
	}

	// Writing the register leaves what it does not understand where it
	// is, so that two accounts that share a project do not undo each
	// other.
	page := file(t, "index.html", "<p>hello</p>")
	if _, err := fresh.add(t.Context(), "", page, page, "page"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(r.path())
	for _, token := range []string{theirs.Token, moved.Token, bare.Token, mine.Token} {
		if !strings.Contains(string(data), token) {
			t.Errorf("the entry with the token %s is gone from the file", token)
		}
	}

	// A file that is not the register's is not served from, and says so.
	if err := os.WriteFile(r.path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := newRegister(r.dir, r.key, nil, nil)
	if _, ok := broken.byToken(mine.Token); ok {
		t.Errorf("an entry was served out of a file that cannot be read")
	}
	if _, _, err := broken.count(); err == nil || !strings.Contains(err.Error(), "is not the JSON gnosh wrote") {
		t.Errorf("a register that cannot be read: %v", err)
	}
}

// The account's key is made once, in gnosh's own data directory, in a file
// only the account reads; every later reading gives the same key.
func TestAccountKey(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	key, err := loadKey(state, nil)
	if err != nil || len(key) != 32 {
		t.Fatalf("the key: %d bytes, %v", len(key), err)
	}
	again, err := loadKey(state, nil)
	if err != nil || !bytes.Equal(key, again) {
		t.Errorf("the key read again differs (%v)", err)
	}
	path := filepath.Join(state, stateName, keyName)
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the key's file: %v, mode %v", err, st.Mode())
	}
	if st, err := os.Stat(filepath.Dir(path)); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("the key's folder: %v, mode %v", err, st.Mode())
	}
	other, err := loadKey(filepath.Join(t.TempDir(), "state"), nil)
	if err != nil || bytes.Equal(key, other) {
		t.Errorf("two accounts drew the same key (%v)", err)
	}

	// A file that does not hold a key is said to be so, and is not
	// written over.
	if err := os.WriteFile(path, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadKey(state, nil); err == nil || !strings.Contains(err.Error(), "does not hold the key gnosh wrote there") {
		t.Errorf("a file that holds no key: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "not a key\n" {
		t.Errorf("the file was written over: %q", data)
	}
}

// The name an address ends with is the last part of the path, whatever
// machine the path is of.
func TestFileName(t *testing.T) {
	for path, want := range map[string]string{
		"/root/renders/ad.mp4": "ad.mp4",
		"renders/ad.mp4":       "ad.mp4",
		"ad.mp4":               "ad.mp4",
		"/root/out/":           "out",
		"/":                    "file",
		"..":                   "file",
		"a b/c d.pdf":          "c d.pdf",
	} {
		if got := fileName(path); got != want {
			t.Errorf("fileName(%q) = %q, want %q", path, got, want)
		}
	}
}

// The copy of a file from a host has a folder of its own, so that the
// same file delivered again takes the earlier copy's place and two files
// of one name do not meet.
func TestCopyPath(t *testing.T) {
	r := newTestRegister(t)
	a, b := r.copyPath("render", "renders/ad.mp4"), r.copyPath("web", "renders/ad.mp4")
	if a == b || a != r.copyPath("render", "renders/ad.mp4") || filepath.Base(a) != "ad.mp4" {
		t.Errorf("the copies are kept at %s and %s", a, b)
	}
	if !strings.HasPrefix(a, filepath.Join(r.dir, copiesName)+string(filepath.Separator)) {
		t.Errorf("the copy %s is not in the folder for copies", a)
	}
}

// A register that is a link (here to /dev/zero, which never ends), or a
// file far bigger than one gnosh writes, is refused at once, unread: a
// cloned repository may hold one, and every session in the folder reads
// the register as it starts (the deliverables review of 2026-11-19).
func TestRegisterThatIsNoFileIsRefused(t *testing.T) {
	r := newTestRegister(t)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", r.path()); err != nil {
		t.Skip("no links here:", err)
	}
	done := make(chan error, 1)
	go func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		done <- r.load()
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "is not a file gnosh wrote (it is a link)") {
			t.Errorf("a register linked to /dev/zero: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a register linked to /dev/zero did not end")
	}
	os.Remove(r.path())
	big, err := os.Create(r.path())
	if err != nil {
		t.Fatal(err)
	}
	big.Truncate(maxRegister + 1)
	big.Close()
	r.mu.Lock()
	err = r.load()
	r.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "more than one gnosh writes") {
		t.Errorf("a register too big: %v", err)
	}
}

// The folder for delivered files is not made or written through a link: a
// repository may carry one in its data folder, and the register would be
// written wherever it leads (the deliverables review of 2026-11-19).
func TestRegisterWritesNothingThroughALink(t *testing.T) {
	r := newTestRegister(t)
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(r.dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, r.dir); err != nil {
		t.Skip("no links here:", err)
	}
	video := file(t, "ad.mp4", "a video")
	if _, err := r.add(t.Context(), "", video, video, "the ad"); err == nil || !strings.Contains(err.Error(), "is a link") {
		t.Errorf("adding through a linked folder: %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the link's target was written: %v", entries)
	}
	if err := noLinks(filepath.Dir(r.dir), filepath.Join(r.dir, "files", "x")); err == nil {
		t.Error("noLinks let a path through a link")
	}
}
