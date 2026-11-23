package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// gnokeyCall is one call of the gnokey tool, scripted.
func gnokeyCallOf(id, command string) apptest.Call {
	args, _ := json.Marshal(map[string]string{"command": command, "description": "test"})
	return apptest.Call{ID: id, Name: "gnokey", Args: string(args)}
}

// gnokeyWorld is a world with a stand-in gnokey first on the PATH, which
// says what it was run with and what it read on its standard input.
func gnokeyWorld(t *testing.T, replies ...apptest.Reply) *apptest.World {
	w := apptest.NewWorld(t, replies...)
	w.Write(w.Dir+"/gnomod.toml", "module = \"gno.land/r/demo/counter\"\n") // a Gno project
	w.Script(w.Root+"/gnobin/gnokey", `echo "args: $*"
case " $* " in *" -insecure-password-stdin "*) read pw; echo "read: $pw";; esac`)
	w.PathFirst = append(w.PathFirst, w.Root+"/gnobin")
	return w
}

// gnokeySession runs a session through a front end of the test's own, the
// way the full-screen interface would: prompt, then the turn's end. The
// masked question is answered with password, a permission question with
// yes. It returns the questions the front end was put.
func gnokeySession(t *testing.T, w *apptest.World, prompt, password string, args ...string) []agent.Question {
	t.Helper()
	front := &testFront{
		answer: func(q agent.Question) agent.Answer {
			if q.Kind == secretKind {
				return agent.Answer{Text: password}
			}
			return agent.Answer{Choice: "yes"}
		},
		run: func(ctx context.Context, c SessionControls, f *testFront) int {
			if _, err := c.Submit(agent.Prompt{Text: prompt}); err != nil {
				t.Error(err)
			}
			if err := c.Wait(ctx); err != nil {
				t.Error(err)
			}
			c.Quit("prompt_input_exit")
			return 0
		}}
	o := testOptions(w, args...)
	o.StdinIsTerminal, o.StdoutIsTerminal = true, true
	o.Signals = make(chan os.Signal, 4)
	o.Interactive = func(FrontendInfo) Frontend { return front }
	runWith(t, t.Context(), o).wantCode(t, 0)
	questions, _, _ := front.told()
	return questions
}

// The gnokey tool runs a query as it is, and asks the person for the key's
// password before a command that signs: at a masked prompt that names the
// key and shows the command, the password then going to gnokey on its
// standard input by the flag gnosh puts before the key. What gnokey
// printed reaches the model without the password, and the session's file
// holds it nowhere.
func TestGnokeyToolSigns(t *testing.T) {
	const sign = "gnokey maketx call -pkgpath gno.land/r/demo/counter -func Increment -gas-fee 1000000ugnot -gas-wanted 2000000 -broadcast -chainid dev -remote 127.0.0.1:26657 testkey"
	w := gnokeyWorld(t,
		apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c1", "gnokey query vm/qrender -data 'gno.land/r/demo/counter:'")}},
		apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c2", sign)}},
		apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c3", "gnokey maketx run -gas-fee 1ugnot -gas-wanted 1 -chainid gnoland1 mainkey script.gno")}},
		apptest.Reply{Text: "Incremented."})
	w.Write(w.Dir+"/script.gno", "package main\n")
	questions := gnokeySession(t, w, "increment the counter", "hunter22", "-y")
	var secrets []agent.Question
	for _, q := range questions {
		if q.Kind == secretKind {
			secrets = append(secrets, q)
		}
	}
	if len(secrets) != 2 {
		t.Fatalf("%d masked questions, want 2: %+v", len(secrets), questions)
	}
	if secrets[0].Title != "Password for the key testkey" || !strings.Contains(secrets[0].Text, "gnokey is to sign with the key testkey:") || !strings.Contains(secrets[0].Text, "gnokey maketx call -pkgpath gno.land/r/demo/counter") || strings.Contains(secrets[0].Text, "main network") {
		t.Errorf("the first masked question: %+v", secrets[0])
	}
	// maketx run's key is the word before the file, and gno.land's main
	// network is said first.
	if secrets[1].Title != "Password for the key mainkey" || !strings.HasPrefix(secrets[1].Text, "This is gno.land's main network") {
		t.Errorf("the second masked question: %+v", secrets[1])
	}

	has(t, "the query", toolResult(t, w, 1, "c1"), "args: query vm/qrender -data gno.land/r/demo/counter:")
	got := toolResult(t, w, 2, "c2")
	has(t, "what the model read", got, "args: maketx call -insecure-password-stdin -pkgpath gno.land/r/demo/counter", "-chainid dev -remote 127.0.0.1:26657 testkey", "read: [the password]")
	// gnokey reads a private copy of the script the question showed.
	has(t, "maketx run", toolResult(t, w, 3, "c3"), "args: maketx run -insecure-password-stdin -gas-fee 1ugnot -gas-wanted 1 -chainid gnoland1 mainkey /")
	if !regexp.MustCompile(`mainkey /\S*gnosh-gnokey-\S*/script\.gno`).MatchString(toolResult(t, w, 3, "c3")) {
		t.Errorf("maketx run was not handed a private copy: %s", toolResult(t, w, 3, "c3"))
	}
	if !strings.Contains(secrets[1].Text, "It signs what script.gno holds") || !strings.Contains(secrets[1].Text, "  package main") {
		t.Errorf("the run's question does not show the script: %s", secrets[1].Text)
	}
	for i := range w.Model.Requests() {
		if strings.Contains(string(w.Model.Requests()[i].Body), "hunter22") {
			t.Errorf("request %d holds the password", i)
		}
	}
	for _, path := range w.Sessions() {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "hunter22") {
			t.Errorf("the session's file %s holds the password", filepath.Base(path))
		}
	}
}

// The question that asks for the password shows all of what is signed:
// what the command sends, pays and calls first, then the whole command,
// never cut. It cut the command at 600 bytes, so a long -args hid the
// -send after it (the gno review of 2026-11-19). A character drawn as
// nothing, a command too long to read and a script too long to show are
// refused, and nothing is signed.
func TestGnokeyToolShowsWhatItSigns(t *testing.T) {
	long := strings.Repeat("x", 580)
	hidden := "gnokey maketx call -pkgpath gno.land/r/demo/counter -func Set -args 'a\u202eb' -chainid dev k"
	w := gnokeyWorld(t,
		apptest.Reply{Calls: []apptest.Call{
			gnokeyCallOf("c1", "gnokey maketx call -pkgpath gno.land/r/demo/counter -func Set -args "+long+" -send 5000000000ugnot -gas-fee 1ugnot -gas-wanted 1 -chainid dev k"),
			gnokeyCallOf("c2", hidden),
			gnokeyCallOf("c3", "gnokey maketx call -pkgpath gno.land/r/demo/counter -func Set -args "+strings.Repeat("y", 2100)+" -chainid dev k"),
			gnokeyCallOf("c4", "gnokey maketx run -gas-fee 1ugnot -gas-wanted 1 -chainid dev k long.gno"),
		}},
		apptest.Reply{Text: "Done."})
	w.Write(w.Dir+"/long.gno", "package main\n"+strings.Repeat("// a line\n", 100))
	questions := gnokeySession(t, w, "set it", "hunter22", "-y")
	var secrets []agent.Question
	for _, q := range questions {
		if q.Kind == secretKind {
			secrets = append(secrets, q)
		}
	}
	if len(secrets) != 1 {
		t.Fatalf("%d masked questions, want 1: %+v", len(secrets), secrets)
	}
	q := secrets[0].Text
	for _, want := range []string{"What it sends, pays and calls:", "  -send 5000000000ugnot", "  -gas-fee 1ugnot", "  -func Set", "The whole command:", long + " -send 5000000000ugnot -gas-fee 1ugnot"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question does not show %q:\n%s", clip(want), q)
		}
	}
	for id, want := range map[string]string{
		"c2": "holds U+202E, a character a terminal draws as nothing",
		"c3": "characters long, more than the user can read",
		"c4": "long.gno has 101 lines, more than the question that asks for the password can show",
	} {
		got := toolResult(t, w, 1, id)
		has(t, id, got, want)
		hasNot(t, id, got, "args:")
	}
}

// The gnokey a password was last handed to is remembered across sessions:
// one rebuilt or replaced since is said in the question, for the person to
// stop if they did not do it (the gno review of 2026-11-19).
func TestGnokeyToolSaysWhenGnokeyChanged(t *testing.T) {
	const sign = "gnokey maketx call -pkgpath gno.land/r/demo/counter -func Increment -gas-fee 1ugnot -gas-wanted 1 -chainid dev k"
	const note = "is not the one gnosh last handed a password to"
	secret := func(qs []agent.Question) string {
		for _, q := range qs {
			if q.Kind == secretKind {
				return q.Text
			}
		}
		t.Fatal("no masked question")
		return ""
	}
	w := gnokeyWorld(t, apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c1", sign)}}, apptest.Reply{Text: "Done."})
	if q := secret(gnokeySession(t, w, "sign it", "hunter22", "-y")); strings.Contains(q, note) {
		t.Errorf("the first signing said gnokey changed:\n%s", q)
	}
	w.Model.Done()
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c2", sign)}}, apptest.Reply{Text: "Done."})
	if q := secret(gnokeySession(t, w, "sign it again", "hunter22", "-y")); strings.Contains(q, note) {
		t.Errorf("the same gnokey was said to have changed:\n%s", q)
	}
	// The stand-in is rebuilt between sessions.
	w.Model.Done()
	w.Script(w.Root+"/gnobin/gnokey", `echo "args: $*" # rebuilt
case " $* " in *" -insecure-password-stdin "*) read pw; echo "read: $pw";; esac`)
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c3", sign)}}, apptest.Reply{Text: "Done."})
	if q := secret(gnokeySession(t, w, "and again", "hunter22", "-y")); !strings.Contains(q, note) {
		t.Errorf("a rebuilt gnokey was not said:\n%s", q)
	}
}

// What the gnokey tool refuses: key management, which stays in the
// person's terminal; the password flag passed by the model; a shell's
// words; anything that is not gnokey. A run, with nobody to type a
// password, has no gnokey tool at all.
func TestGnokeyToolRefuses(t *testing.T) {
	w := gnokeyWorld(t,
		apptest.Reply{Calls: []apptest.Call{
			gnokeyCallOf("c1", "gnokey add mykey"),
			gnokeyCallOf("c2", "gnokey maketx send -broadcast -insecure-password-stdin k"),
			gnokeyCallOf("c3", "gnokey list | cat"),
			gnokeyCallOf("c4", "rm -rf /"),
			gnokeyCallOf("c5", "gnokey maketx send -to g1x -send 1ugnot -broadcast"),
			gnokeyCallOf("c6", "gnokey maketx send -to g1x -send 1ugnot -broadcast=false k"),
			// gnokey reads a command's name in any letter case, after its
			// root flags: neither slips by.
			gnokeyCallOf("c7", "gnokey Delete -force mykey"),
			gnokeyCallOf("c8", "gnokey -remote list delete -force mykey"),
			gnokeyCallOf("c9", "gnokey maketx send -config gnokey.toml -gas-fee 1ugnot k"),
			gnokeyCallOf("c10", "gnokey sign -tx-path /dev/stdin -chainid dev k"),
			gnokeyCallOf("c11", "gnokey maketx call -memo 'a\nb' k"),
			gnokeyCallOf("c12", "gnokey frobnicate k"),
		}},
		apptest.Reply{Text: "Done."})
	questions := gnokeySession(t, w, "try it all", "pw", "-y")
	for id, want := range map[string]string{
		"c1":  "gnokey add makes, changes or hands out a key, which stays in the user's own terminal",
		"c2":  "passes gnokey's -insecure-password-stdin itself, which only gnosh may do",
		"c3":  "is a shell's to read, and this command is run with none",
		"c4":  "the command must begin with gnokey",
		"c5":  "the key's name is missing",
		"c7":  "gnokey delete makes, changes or hands out a key",
		"c8":  "put gnokey's command right after gnokey",
		"c9":  "gnokey -config reads flags from a file",
		"c10": "is no file but a device, a pipe or a socket",
		"c11": "holds a line break",
		"c12": "gnokey frobnicate is not a command this tool runs",
	} {
		got := toolResult(t, w, 1, id)
		has(t, id, got, want)
		hasNot(t, id, got, "args:")
	}
	// An unsigned transaction is made with no password.
	has(t, "c6", toolResult(t, w, 1, "c6"), "args: maketx send -to g1x -send 1ugnot -broadcast=false k")
	for _, q := range questions {
		if q.Kind == secretKind {
			t.Errorf("a refused call asked for a password: %+v", q)
		}
	}

	// A run has no gnokey tool,
	w2 := gnokeyWorld(t, apptest.Reply{Text: "ok"})
	run(t, w2, "-p", "hi").wantCode(t, 0)
	if reqs := w2.Model.Requests(); len(reqs) == 0 || slices.Contains(toolNamesOf(reqs[0]), "gnokey") {
		t.Errorf("a run was given the gnokey tool")
	}
	// nor has the plain front end, which reads lines typed ahead as
	// prompts,
	w5 := gnokeyWorld(t, apptest.Reply{Text: "ok"})
	s5 := sit(t, w5)
	s5.see("Type a prompt and Enter.")
	s5.typeLine("hi")
	s5.see("ok")
	s5.typeLine("/exit")
	s5.exit()
	if reqs := w5.Model.Requests(); len(reqs) == 0 || slices.Contains(toolNamesOf(reqs[0]), "gnokey") {
		t.Errorf("the plain front end was given the gnokey tool")
	}
	// nor a session in a project with no Gno code.
	w4 := gnokeyWorld(t, apptest.Reply{Text: "ok"})
	os.Remove(w4.Dir + "/gnomod.toml")
	gnokeySession(t, w4, "hi", "")
	if reqs := w4.Model.Requests(); len(reqs) == 0 || slices.Contains(toolNamesOf(reqs[0]), "gnokey") {
		t.Errorf("a session in a project with no Gno code was given the gnokey tool")
	}
}

// A gnokey that is not the one the session started with is handed no
// password: a model that rebuilt or replaced it would read the password.
func TestGnokeyToolRefusesAChangedGnokey(t *testing.T) {
	w := gnokeyWorld(t,
		apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c1", "gnokey list")}},
		apptest.Reply{Text: "Done."})
	bin := w.Root + "/gnobin/gnokey"
	front := &testFront{
		answer: func(q agent.Question) agent.Answer { return agent.Answer{Choice: "yes"} },
		run: func(ctx context.Context, c SessionControls, f *testFront) int {
			// Replaced once the session has started.
			w.Script(bin, `echo "a gnokey of the model's"`)
			if _, err := c.Submit(agent.Prompt{Text: "list the keys"}); err != nil {
				t.Error(err)
			}
			if err := c.Wait(ctx); err != nil {
				t.Error(err)
			}
			c.Quit("prompt_input_exit")
			return 0
		}}
	o := testOptions(w, "-y")
	o.StdinIsTerminal, o.StdoutIsTerminal = true, true
	o.Signals = make(chan os.Signal, 4)
	o.Interactive = func(FrontendInfo) Frontend { return front }
	runWith(t, t.Context(), o).wantCode(t, 0)
	got := toolResult(t, w, 1, "c1")
	has(t, "a replaced gnokey", got, "is not the gnokey this session started with")
	hasNot(t, "a replaced gnokey", got, "a gnokey of the model's")
}

// With the real gnokey, where this machine has it: a transaction made
// offline is signed with the password typed at the prompt, and a wrong
// password is reported as gnokey reports it, nothing signed.
func TestGnokeyToolWithTheRealGnokey(t *testing.T) {
	if _, err := exec.LookPath("gnokey"); err != nil {
		t.Skip("gnokey is not installed here")
	}
	home := t.TempDir()
	add := exec.Command("gnokey", "add", "e2ekey", "-home", home, "-insecure-password-stdin")
	add.Stdin = strings.NewReader("pw12345678\npw12345678\n")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("gnokey add: %v\n%s", err, out)
	}
	list, _ := exec.Command("gnokey", "list", "-home", home).Output()
	addr := regexp.MustCompile(`g1[a-z0-9]+`).FindString(string(list))
	unsigned, err := exec.Command("gnokey", "maketx", "send", "-send", "1ugnot", "-to", addr, "-gas-fee", "1000ugnot", "-gas-wanted", "100000", "-broadcast=false", "-home", home, "e2ekey").Output()
	if err != nil || addr == "" {
		t.Fatalf("gnokey maketx: %v (%s)", err, addr)
	}
	tx := filepath.Join(home, "tx.json")
	if err := os.WriteFile(tx, unsigned, 0o600); err != nil {
		t.Fatal(err)
	}
	sign := "gnokey sign -tx-path " + tx + " -chainid dev -account-number 0 -account-sequence 0 -home " + home + " e2ekey"
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c1", sign)}},
		apptest.Reply{Calls: []apptest.Call{gnokeyCallOf("c2", sign)}},
		apptest.Reply{Text: "Signed."})
	w.Write(w.Dir+"/gnomod.toml", "module = \"gno.land/r/demo/counter\"\n")
	tries := 0
	front := &testFront{
		answer: func(q agent.Question) agent.Answer {
			if q.Kind != secretKind {
				return agent.Answer{Choice: "yes"}
			}
			if tries++; tries == 1 {
				return agent.Answer{Text: "wrong-password"}
			}
			return agent.Answer{Text: "pw12345678"}
		},
		run: func(ctx context.Context, c SessionControls, f *testFront) int {
			if _, err := c.Submit(agent.Prompt{Text: "sign it"}); err != nil {
				t.Error(err)
			}
			if err := c.Wait(ctx); err != nil {
				t.Error(err)
			}
			c.Quit("prompt_input_exit")
			return 0
		}}
	o := testOptions(w, "-y")
	o.StdinIsTerminal, o.StdoutIsTerminal = true, true
	o.Signals = make(chan os.Signal, 4)
	o.Interactive = func(FrontendInfo) Frontend { return front }
	runWith(t, t.Context(), o).wantCode(t, 0)
	has(t, "a wrong password", toolResult(t, w, 1, "c1"), "invalid account password")
	has(t, "the password", toolResult(t, w, 2, "c2"), "Tx successfully signed")
	if data, _ := os.ReadFile(tx); !strings.Contains(string(data), `"signature":`) {
		t.Errorf("the transaction was not signed:\n%s", data)
	}
}
