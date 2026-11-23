package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// /cd moves the session to another folder, as Claude Code's does. It
// says where the session is with nothing after it; refuses a folder that
// is not there, the one it is in, and one the Cd rules keep it out of;
// asks about a folder not trusted (a no stays); and otherwise puts the
// session with the new folder's sessions and starts gnosh again there,
// with --cwd, the session, and the folder it left. The gnosh it starts
// tells the model of the move, with the new folder's settings in force.
func TestCd(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "Hi."},
		apptest.Reply{Text: "In the lib."},
	)
	lib := filepath.Join(w.Root, "lib")
	secret := filepath.Join(w.Root, "secret")
	for _, d := range []string{lib, secret, filepath.Join(w.Root, "extra")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The lib's own settings wait for trust; its instructions go to the
	// model once the session is there.
	if err := os.WriteFile(filepath.Join(lib, "gnosh.json"), []byte(`{"permissions": {"allow": ["Bash(make *)"]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "GNOSH.md"), []byte("In this project the codeword is PERSIMMON.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.Deny("Cd(/" + secret + ")")
	var started, envs [][]string
	s := sitWith(t, w, func(o *Options) {
		o.Relaunch = func(args, env []string) error {
			started, envs = append(started, args), append(envs, env)
			return os.ErrPermission
		}
	}, "-y", "say hi")
	s.see("Hi.")
	s.see("-- done in ")
	s.typeLine("/cd")
	s.see("The session works in " + w.Dir + ". /cd <folder> moves it to another")
	s.typeLine("/cd nowhere")
	s.see(filepath.Join(w.Dir, "nowhere") + " does not exist; /cd takes a folder that is there")
	s.typeLine("/cd ../secret")
	s.see("the session stays in " + w.Dir + ". The deny rule Cd(/" + secret + ") from " + w.SettingsFile() + " covers " + secret + ".")
	s.typeLine("/cd .")
	s.see("The session works in " + w.Dir + " already.")
	s.typeLine("/add-dir ../extra")
	s.see("is a working directory for the rest of this session")

	// A no leaves the session where it is.
	s.typeLine("/cd ../lib")
	s.see("Trust " + lib + "? The session moves there only if it is.")
	s.see("Bash(make *)")
	s.see("  2. Yes: use this folder's settings, and move there")
	s.typeLine("1")
	s.see("the session stays in " + w.Dir + ": " + lib + " was not trusted")
	st := &transcript.Store{Dir: w.DataDir()}
	if list, _ := st.List(context.Background(), w.Dir); len(list) != 1 {
		t.Fatalf("after a no, the project lists %d sessions", len(list))
	}

	// A yes moves it: through a link, to the folder the link leads to,
	// as every start of gnosh names it (the cd review of 2026-11-19: it
	// was filed under the link's path, where no start looks).
	if err := os.Symlink(lib, filepath.Join(w.Root, "liblink")); err != nil {
		t.Fatal(err)
	}
	s.typeLine("/cd ../liblink")
	s.see("Trust " + lib + "?")
	s.typeLine("2")
	s.see("The session moves to " + lib + ": gnosh starts again there, with that folder's own settings, instructions and skills.")
	if code := s.exit(); code != 1 {
		t.Errorf("exit code %d, want 1 for a start that failed", code)
	}
	if n := len(w.Sessions()); n != 1 {
		t.Fatalf("%d session files", n)
	}
	file := w.Sessions()[0]
	id := strings.TrimSuffix(filepath.Base(file), ".jsonl")
	if filepath.Dir(file) != st.ProjectDir(lib) {
		t.Errorf("the session's file is at %s, want it with the lib's sessions", file)
	}
	for dir, want := range map[string]int{w.Dir: 0, lib: 1} {
		if list, _ := st.List(context.Background(), dir); len(list) != want {
			t.Errorf("%s lists %d sessions, want %d", dir, len(list), want)
		}
	}
	want := []string{"--add-dir=" + filepath.Join(w.Root, "extra"), "--cwd=" + lib, "--dangerously-skip-permissions", "--resume=" + id}
	if len(started) != 1 || !slices.Equal(started[0], want) {
		t.Fatalf("gnosh started again with %q, want %q", started, want)
	}
	if !slices.Contains(envs[0], MovedEnv+"="+w.Dir) || !slices.ContainsFunc(envs[0], func(kv string) bool { return strings.HasPrefix(kv, RestartedEnv+"=") }) {
		t.Errorf("the gnosh /cd starts is not told it moved, and from where: %q", envs[0])
	}

	// The gnosh it starts goes on in the lib, trusted now, and tells the
	// model where it is.
	s = sitWith(t, w, func(o *Options) {
		o.Environ = append(slices.Clone(o.Environ), envs[0][len(envs[0])-2:]...)
	}, started[0]...)
	s.see("Type a prompt and Enter.")
	s.typeLine("where are we")
	s.see("In the lib.")
	s.typeLine("/exit")
	s.exit()
	reqs := w.Model.Requests()
	has(t, "the request after the move", string(reqs[len(reqs)-1].Body),
		"The user moved this session with /cd from "+w.Dir+" to "+lib+".", "working directory is "+lib,
		"the codeword is PERSIMMON")
	if strings.Contains(string(reqs[0].Body), "moved this session") {
		t.Error("the model was told of a move before there was one")
	}
	if list, _ := st.List(context.Background(), lib); len(list) != 1 || list[0].ID != id {
		t.Errorf("after the new start, the lib lists %+v", list)
	}
}

// /cd is the person's alone: not in a run, not with nobody to start gnosh
// again, and not out of a session's own work tree.
func TestCdRefused(t *testing.T) {
	w := apptest.NewWorld(t)
	other := filepath.Join(w.Root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	res := run(t, w, "-p", "/cd "+other)
	has(t, "a run's /cd", res.out+res.err, "/cd works in an interactive session and does nothing as the prompt of a run")

	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("/cd " + other)
	s.see("this gnosh cannot start itself again, which moving the session needs.")
	s.typeLine("/exit")
	s.exit()

	// A Cd allow rule makes a list of where /cd may go; a bare deny
	// switches it off.
	w.Settings(`{"providers": {"local": {"type": "llamacpp", "baseUrl": "` + w.Model.URL + `"}}, "model": "local/test-model", "permissions": {"allow": ["Cd(~/code/**)"]}}`)
	s = sitWith(t, w, func(o *Options) { o.Relaunch = func(args, env []string) error { return nil } })
	s.see("Type a prompt and Enter.")
	s.typeLine("/cd " + other)
	s.see("The Cd allow rules name the folders /cd may go to (Cd(~/code/**)), and " + other + " is none of them.")
	s.typeLine("/exit")
	s.exit()
	w.Settings(`{"providers": {"local": {"type": "llamacpp", "baseUrl": "` + w.Model.URL + `"}}, "model": "local/test-model", "permissions": {"deny": ["Cd"]}}`)
	s = sitWith(t, w, func(o *Options) { o.Relaunch = func(args, env []string) error { return nil } })
	s.see("Type a prompt and Enter.")
	s.typeLine("/cd " + other)
	s.see("The deny rule Cd from " + w.SettingsFile() + " switches /cd off.")
	s.typeLine("/exit")
	s.exit()
}
