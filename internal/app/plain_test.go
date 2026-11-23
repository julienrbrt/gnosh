package app

import (
	"context"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// seat is a person at a terminal, for a test: what they type goes down a
// pipe that the program is told is a terminal, and what the program
// prints is read as it comes.
type seat struct {
	t       *testing.T
	in      *io.PipeWriter
	out     *safeBuffer
	errs    *safeBuffer
	signals chan os.Signal
	done    chan int
	pos     int
}

// sit starts an interactive session with a person at its terminal, who
// answers a question as soon as it shows (the wait for a line typed before
// it could be read is off: Options.AnswerWait).
func sit(t *testing.T, w *apptest.World, args ...string) *seat {
	t.Helper()
	none := time.Duration(0)
	return sitWaiting(t, w, &none, args...)
}

// sitWaiting is sit with the plain front end's wait for an answer typed
// before its question (nil: gnosh's own).
func sitWaiting(t *testing.T, w *apptest.World, wait *time.Duration, args ...string) *seat {
	t.Helper()
	pr, pw := io.Pipe()
	s := &seat{t: t, in: pw, out: &safeBuffer{}, errs: &safeBuffer{}, signals: make(chan os.Signal, 4), done: make(chan int, 1)}
	o := testOptions(w, args...)
	o.AnswerWait = wait
	o.Stdin, o.Stdout, o.Stderr = pr, s.out, s.errs
	o.StdinIsTerminal, o.StdoutIsTerminal = true, true
	o.Signals = s.signals
	go func() { s.done <- Main(context.Background(), o) }()
	t.Cleanup(func() {
		pw.Close()
		select {
		case <-s.done:
		case <-time.After(patience):
			t.Errorf("the session did not end when its input was closed.\nscreen:\n%s", s.out.String())
		}
	})
	return s
}

// patience is how long a test waits for something to show before it calls
// it missing. It is a bound for a broken program, not a measure of a
// working one: the tests run beside others on a loaded machine.
const patience = 60 * time.Second

// seeAnywhere waits until the screen shows piece, wherever it stands. It
// is for a line whose place among the others is not fixed: a notice that
// the session raises as it starts is shown by a goroutine of its own, and
// may come before or after the lines the front end writes itself.
func (s *seat) seeAnywhere(piece string) {
	s.t.Helper()
	deadline := time.Now().Add(patience)
	for !strings.Contains(s.out.String(), piece) {
		if time.Now().After(deadline) {
			s.t.Fatalf("the screen never showed %q.\nthe screen:\n%s\nstderr:\n%s", piece, clip(s.out.String()), s.errs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// see waits until the screen shows piece after everything seen so far.
func (s *seat) see(piece string) {
	s.t.Helper()
	deadline := time.Now().Add(patience)
	for {
		screen := s.out.String()
		if i := strings.Index(screen[s.pos:], piece); i >= 0 {
			s.pos += i + len(piece)
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("the screen never showed %q after what was seen before.\nseen before:\n%s\nsince then:\n%s\nstderr:\n%s", piece, clip(screen[:s.pos]), clip(screen[s.pos:]), s.errs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// mcpConnected waits until /mcp says the server is connected: a session
// connects its servers in the background, and a prompt typed before then
// finds none of their tools.
func (s *seat) mcpConnected(name string) {
	s.t.Helper()
	deadline := time.Now().Add(patience)
	for {
		s.typeLine("/mcp")
		for wait := time.Now().Add(time.Second); time.Now().Before(wait); time.Sleep(10 * time.Millisecond) {
			screen := s.out.String()
			if i := strings.LastIndex(screen, name+": connected"); i >= s.pos {
				s.pos = i + len(name+": connected")
				return
			}
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("/mcp never said %s is connected.\nthe screen:\n%s\nstderr:\n%s", name, clip(s.out.String()), s.errs.String())
		}
	}
}

// typeLine types a line and Enter.
func (s *seat) typeLine(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatalf("typing %q: %v", line, err)
	}
}

// exit waits for the program to end and returns its exit code.
func (s *seat) exit() int {
	s.t.Helper()
	select {
	case code := <-s.done:
		s.done <- code
		return code
	case <-time.After(patience):
		s.t.Fatalf("the session did not end.\nscreen:\n%s", clip(s.out.String()))
	}
	return -1
}

// A person types a prompt, answers a permission question after seeing the
// change it is about, refuses another with words for the model, runs
// slash commands, and leaves with /exit.
func TestPlainSession(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Thinking: "It is a sum.", Text: "The answer is 4."},
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Read", Args: `{"file_path":"a.txt"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Edit", Args: `{"file_path":"a.txt","old_string":"two plus","new_string":"three plus"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "The file is changed; the command was not run."},
		apptest.Reply{Text: "Without thinking: 5."})
	w.Write("a.txt", "one\ntwo plus two\nthree\n")
	s := sit(t, w)
	s.see("session ")
	s.see("model local/test-model | thinking on")
	s.see("Type a prompt and Enter.")

	// A prompt, and its answer with the model's thinking set apart.
	s.typeLine("what is 2+2?")
	s.see("  ~ It is a sum.")
	s.see("The answer is 4.")
	s.see("-- done in ")

	// A question, with the change it is about, answered yes.
	s.typeLine("change the file")
	s.see("* Read")
	s.see("? Allow Edit?")
	s.see("a.txt")
	s.see("three plus two")
	s.see("  1. Yes, this once")
	s.see("Type a number and Enter.")
	if got := w.Read("a.txt"); !strings.Contains(got, "two plus two") {
		t.Fatalf("the file was changed before the question was answered: %q", got)
	}
	s.typeLine("1")
	s.see("= +1 -1")
	// The next question is refused, with words for the model.
	s.see("? Allow Bash?")
	s.see("$ touch made.txt")
	s.typeLine("no, leave the folder as it is")
	s.see("The file is changed; the command was not run.")
	s.see("-- done in ")
	if got := w.Read("a.txt"); got != "one\nthree plus two\nthree\n" {
		t.Errorf("a.txt = %q", got)
	}
	if w.Exists("made.txt") {
		t.Errorf("the command ran although the person said no")
	}
	has(t, "what the model read after the refusal", requestText(t, w, 4), "leave the folder as it is")

	// Slash commands.
	s.typeLine("/status")
	s.see("session      ")
	s.see("model        local/test-model at http://127.0.0.1:")
	s.see("state        idle")
	s.typeLine("/nosuch")
	s.see("There is no command /nosuch.")
	s.typeLine("/help")
	s.see("/compact [instructions]")
	s.typeLine("/no_think")
	s.see("Thinking is off for local/test-model")
	s.typeLine("and 2+3?")
	s.see("Without thinking: 5.")
	s.see("thinking off")
	has(t, "the request after /no_think", requestText(t, w, 5), `"enable_thinking":false`)
	has(t, "the request before it", requestText(t, w, 4), `"enable_thinking":true`)
	// The choice is kept for later sessions.
	has(t, "the state file", w.Read(w.DataDir()+"/state.json"), "local/test-model", "off")

	s.typeLine("/exit")
	if code := s.exit(); code != 0 {
		t.Errorf("exit code %d after /exit", code)
	}
	s.see("To resume this session: gnosh -r ")
	w.Model.Done()
	checkSession(t, w, w.Sessions()[0])
}

// Ctrl+C stops the turn that is running and leaves the session open; with
// nothing running it says so; a second one right after quits with 130.
func TestPlainCtrlC(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "This answer never ends, because the model is held.", Hold: hold},
		apptest.Reply{Text: "A short answer."})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("tell me a long story")
	s.see("This answer never")
	s.signals <- os.Interrupt
	s.see("Stopping the turn.")
	s.see("-- stopped in ")

	// The session is still there.
	s.typeLine("a short one then")
	s.see("A short answer.")
	s.see("-- done in ")

	time.Sleep(quitWindow + 200*time.Millisecond)
	s.signals <- os.Interrupt
	s.see("Nothing is running. Ctrl+C again")
	s.signals <- os.Interrupt
	if code := s.exit(); code != 130 {
		t.Errorf("exit code %d after two Ctrl+C, want 130", code)
	}
	s.see("To resume this session: gnosh -r ")
	// The stopped reply is kept as stopped, and the file reads back whole.
	checkSession(t, w, w.Sessions()[0])
}

// SIGTERM ends an interactive session in order, with 143.
func TestPlainSIGTERM(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	w := apptest.NewWorld(t, apptest.Reply{Text: "This answer never ends either.", Hold: hold})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("go")
	s.see("This answer")
	s.signals <- syscall.SIGTERM
	if code := s.exit(); code != 143 {
		t.Errorf("exit code %d after SIGTERM, want 143", code)
	}
	s.see("To resume this session: gnosh -r ")
	checkSession(t, w, w.Sessions()[0])
}

// The end of the input (Ctrl+D) ends the session once what is under way
// has finished.
func TestPlainEndOfInput(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Four."})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("what is 2+2?")
	s.in.Close()
	if code := s.exit(); code != 0 {
		t.Errorf("exit code %d at the end of the input", code)
	}
	s.see("Four.")
	s.see("To resume this session: gnosh -r ")
}

// A folder whose own settings wait for trust: the person is asked before
// the session starts, with what the settings would do, and a yes is
// recorded.
func TestPlainTrustQuestion(t *testing.T) {
	w := apptest.NewWorld(t, editScript()...)
	w.Write("a.txt", "two plus two\n")
	w.Write("gnosh.json", `{"permissions": {"allow": ["Edit"]}}`+"\n")
	s := sit(t, w)
	s.see("? Trust this folder?")
	s.see("Edit")
	s.see("  2. Yes: use this folder's settings")
	s.typeLine("2")
	s.see("Type a prompt and Enter.")
	s.typeLine("change it")
	s.see("Done.")
	if got := w.Read("a.txt"); got != "three plus two\n" {
		t.Errorf("a.txt = %q: the folder's allow rule was not in force after the yes", got)
	}
	s.typeLine("/exit")
	s.exit()
	if out := run(t, w, "trust", "--list").out; !strings.Contains(out, w.Dir) {
		t.Errorf("the yes was not recorded: trust --list says %q", out)
	}
}

// A prompt piped into an interactive session, with no terminal to type
// at: the session does that one prompt, refuses what would need a yes,
// and ends.
func TestPlainPipedPromptWithNobodyToType(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "It was refused."})
	o := testOptions(w)
	o.Stdin = strings.NewReader("make the file\nplease\n")
	o.StdoutIsTerminal = true
	r := runWith(t, t.Context(), o).wantCode(t, 0)
	has(t, "the screen", r.out, "Nobody can type here", "It was refused.", "To resume this session")
	if w.Exists("made.txt") {
		t.Errorf("a command that needs a yes ran with nobody to give it")
	}
	if users := userTexts(w.Model.Requests()[0]); len(users) != 1 || !strings.HasSuffix(users[0], "make the file\nplease\n") {
		t.Errorf("the piped text is not the one prompt: %q", users)
	}
	w.Model.Done()
}

// A line typed before a question could be read is not its answer: a "2"
// meant for a list in the model's reply picked "Yes, and don't ask again"
// (the cd review of 2026-11-19). It is said so, and the question waits for
// an answer typed after it.
func TestPlainAnswerTypedBeforeTheQuestion(t *testing.T) {
	w := apptest.NewWorld(t, editScript()...)
	w.Write("a.txt", "two plus two\n")
	s := sitWaiting(t, w, nil)
	s.see("Type a prompt and Enter.")
	s.typeLine("change it")
	s.see("  1. Yes")
	s.typeLine("1") // at once: before the question could be read
	s.see("typed before the question could be read")
	if got := w.Read("a.txt"); got != "two plus two\n" {
		t.Fatalf("a.txt = %q: the line typed at once answered the question", got)
	}
	time.Sleep(answerWait + 100*time.Millisecond)
	s.typeLine("1")
	s.see("Done.")
	if got := w.Read("a.txt"); got != "three plus two\n" {
		t.Errorf("a.txt = %q after the answer typed in time", got)
	}
	s.typeLine("/exit")
	s.exit()
}

// What plain mode writes holds nothing a terminal acts on: C0 and C1
// controls are written as escapes, a byte that is no UTF-8 as U+FFFD (alone,
// 0x9b is the 8-bit CSI), and a mark that turns text around as its \u
// form (the cd review of 2026-11-19).
func TestVisible(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":        "plain text",
		"a\x1b[2Jb":         `a\x1b[2Jb`,
		"c1 \u009b31m":      `c1 \x9b31m`,
		"raw \x9b31m":       "raw \uFFFD31m",
		"evil\u202etxt.exe": `evil\u202etxt.exe`,
		"tab\tand\nnewline": "tab\tand\nnewline",
	} {
		if got := visible(in); got != want {
			t.Errorf("visible(%q) = %q, want %q", in, got, want)
		}
	}
}
