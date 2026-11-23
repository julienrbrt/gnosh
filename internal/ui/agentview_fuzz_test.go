package ui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor/keyscript"
)

// agentPool is every kind of event the agent view takes in a fuzzed
// session. A byte of fuzz input picks one.
var agentPool = keyscript.MustParse(`
	"a" "b" " " "1" "2" "3" "9" "?" "/" "@" ":" "n" "s" "o" "q" "世" "👍"
	<Enter> <Shift+Enter> <Right> <Left> <Up> <Down> <PgUp> <PgDn> <Home> <End>
	<Esc> <Esc> <Ctrl+C> <Ctrl+X> <Ctrl+X> <Ctrl+S> <Ctrl+R> <Ctrl+L> <Ctrl+W> <Ctrl+U>
	<Alt+Up> <Alt+Down> <Ctrl+F> <Paste "/stop">
	<Backspace> <Tab> <Space> <Space>
	<Paste "s:working"> <Paste "n:docs"> <Paste "o:42"> <Paste "/model qwen/other"> <Paste "/effort high">
	<Paste "/rename night"> <Paste "@reviewer look"> <Paste "tidy the README"> <Paste "a\nb">
	<Paste "\x1b[31mred\x07"> <Paste "/exit">
`)

// agentSession runs the events that data picks through an agent view of
// the rig's runs, which change under it as the bytes say (a run starts,
// waits on a question, ends, goes), and draws it after each at a size the
// bytes also change. After every event the frame must be whole: exactly
// as many rows as the screen has (a run watched aside), none wider than
// the screen, the cursor inside the box. Nothing may panic.
func agentSession(t *testing.T, data []byte, cols, rows int) {
	g := newAgentRig(t)
	cols, rows = cols%140+1, rows%50+1
	for i := 0; i < len(data) && i < 300; i++ {
		b := data[i]
		switch {
		case b >= 250:
			// The screen's size, and time.
			cols = int(b-250)*29 + 1 + i%5
			rows = int(b-250)*7 + 1 + i%3
			g.clk.add(time.Duration(b-249) * time.Second)
			g.v.tick()
			continue
		case b >= 240:
			// The runs change under the view.
			id := []string{runTests, runDocs, runFlaky, runShip}[int(b)%4]
			switch b % 6 {
			case 0:
				g.src.set(id, func(r *app.AgentRun) { r.State, r.Ended = "done", g.clk.Now() })
			case 5:
				g.src.set(id, func(r *app.AgentRun) {
					r.State, r.Ended, r.Question = "failed", g.clk.Now(), nil
					r.Ending = "it ended without a word: it was killed, or its machine stopped, " + strings.Repeat("and so on ", int(b)%4)
				})
			case 1:
				g.src.set(id, func(r *app.AgentRun) {
					r.State, r.Question = "blocked", &app.AgentQuestion{ID: "q", Title: "Allow Bash?",
						Text:    "  $ rm -rf x\n  Why gnosh asks: " + strings.Repeat("rm changes files, which asks unless a rule allows it; ", int(b)%5),
						Choices: []app.QuestionChoice{{Key: "yes", Label: "Yes"}, {Key: "no", Label: "No"}}}
				})
			case 2:
				g.src.set(id, func(r *app.AgentRun) { r.State, r.Question = "working", nil })
			case 3:
				_ = g.src.Forget(id)
			case 4:
				g.src.set(id, func(r *app.AgentRun) { r.Name, r.Prompt = strings.Repeat("long name ", int(b)%7), "" })
			}
			g.v.refresh()
			continue
		}
		ev := agentPool[int(b)%len(agentPool)]
		g.v.input(ev)
		if g.v.quit || g.v.open != "" {
			// The loop would leave, or give the terminal to gnosh -r; the
			// session goes on as if it came back.
			g.v.quit, g.v.open = false, ""
		}
		checkAgentFrame(t, g.v, cols, rows, fmt.Sprintf("event %d (%v) at %dx%d", i, ev, cols, rows))
	}
}

// checkAgentFrame draws the view at cols by rows and checks the frame.
func checkAgentFrame(t *testing.T, v *agentView, cols, rows int, when string) {
	t.Helper()
	body, live, crow, ccol := v.render(cols, rows)
	if v.watch == nil && len(body)+len(live) != max(rows, len(live)+1) {
		t.Fatalf("%s: the frame is %d+%d rows, the screen %d", when, len(body), len(live), rows)
	}
	width := max(cols, 10)
	for i, l := range append(append([]string(nil), body...), live...) {
		if w := v.m.VisibleWidth(l); w > width {
			t.Fatalf("%s: row %d is %d cells wide, the screen %d: %q", when, i, w, width, text.Strip(l))
		}
	}
	if crow >= len(live) || ccol > width {
		t.Fatalf("%s: the cursor is at %d,%d, outside the %d rows of the box", when, crow, ccol, len(live))
	}
}

func FuzzAgentView(f *testing.F) {
	f.Add([]byte("hello"), uint8(80), uint8(24))
	f.Add([]byte{0, 1, 2, 240, 241, 3, 250, 4, 5, 246, 6}, uint8(20), uint8(5))
	for seed := range 20 {
		rng := rand.New(rand.NewSource(int64(seed) + 5))
		data := make([]byte, 160)
		rng.Read(data)
		f.Add(data, uint8(seed*7), uint8(seed*3))
	}
	f.Fuzz(func(t *testing.T, data []byte, cols, rows uint8) {
		agentSession(t, data, int(cols), int(rows))
	})
}

// The same property over a fixed set of random sessions, so that it is
// checked on every run and not only while fuzzing.
func TestRandomAgentSessions(t *testing.T) {
	for seed := range 150 {
		rng := rand.New(rand.NewSource(int64(seed)))
		data := make([]byte, 200)
		rng.Read(data)
		agentSession(t, data, rng.Intn(256), rng.Intn(256))
	}
}

// (term is used by the pool's events.)
var _ = term.Key{}
