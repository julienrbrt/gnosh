package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// boomTool is a tool that panics.
type boomTool struct{}

func (boomTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "Boom", Description: "Fails badly.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
}

func (boomTool) Run(context.Context, core.ToolCall, *core.ToolEnv) (core.ToolResult, error) {
	panic("the tool fell over")
}

// A tool that crashes does not take the session with it: the model is
// told, the turn goes on, and the person is told where the details are.
// The next session in the folder says once that something crashed.
func TestToolCrashIsSurvivedAndWrittenDown(t *testing.T) {
	feature := Feature{
		Name:  "boom",
		Tools: []ToolName{{Name: "Boom", Quiet: true}},
		Session: func(ctx context.Context, s *Session) (*Contribution, error) {
			return &Contribution{Tools: []core.Tool{boomTool{}}}, nil
		},
	}
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Boom", Args: `{}`}}},
		apptest.Reply{Text: "The tool crashed, so I stopped there."},
		apptest.Reply{Text: "ok"})
	o := testOptions(w, "-p", "use the tool")
	o.Features = []Feature{feature}
	r := runWith(t, t.Context(), o).wantCode(t, 0)
	if r.out != "The tool crashed, so I stopped there.\n" {
		t.Errorf("stdout = %q", r.out)
	}
	has(t, "what the model read", toolResult(t, w, 1, "c1"), "crashed")
	has(t, "stderr", r.err, "the tool Boom crashed", "the tool fell over", "the details are in ")
	path := regexp.MustCompile(`the details are in (\S+\.log)`).FindStringSubmatch(r.err)
	if path == nil {
		t.Fatalf("stderr names no crash file:\n%s", r.err)
	}
	data, err := os.ReadFile(path[1])
	if err != nil {
		t.Fatalf("the crash file: %v", err)
	}
	has(t, "the crash file", string(data), "the tool fell over", "goroutine ", "boomTool")
	checkSession(t, w, w.Sessions()[0])

	// The next session says so, once.
	r = run(t, w, "-p", "hello").wantCode(t, 0)
	has(t, "stderr of the next session", r.err, "crashed in this folder", strings.TrimSuffix(filepath.Base(path[1]), ".log")+".seen")
	w.Model.Add(apptest.Reply{Text: "ok"})
	run(t, w, "-p", "hello").wantCode(t, 0).wantNoErr(t)
}

// A fault in gnosh itself ends the program with a code of its own, and
// says where what happened is written and how to get the session back.
func TestOwnFaultIsWrittenDown(t *testing.T) {
	feature := Feature{
		Name: "faulty",
		Session: func(ctx context.Context, s *Session) (*Contribution, error) {
			panic("a fault in the wiring")
		},
	}
	w := apptest.NewWorld(t)
	o := testOptions(w, "-p", "hello")
	o.Features = []Feature{feature}
	r := runWith(t, t.Context(), o).wantCode(t, 2)
	if r.out != "" {
		t.Errorf("stdout = %q", r.out)
	}
	has(t, "stderr", r.err, "gnosh hit a fault of its own and stopped (a fault in the wiring)", "What happened is written in ", "gnosh -c continues it")
	path := regexp.MustCompile(`written in (\S+\.log)`).FindStringSubmatch(r.err)
	if path == nil {
		t.Fatalf("stderr names no crash file:\n%s", r.err)
	}
	defer os.Remove(path[1])
	data, err := os.ReadFile(path[1])
	if err != nil {
		t.Fatalf("the crash file: %v", err)
	}
	has(t, "the crash file", string(data), "a fault in the wiring", "goroutine ")
}

// An empty crash file that nobody holds is a run's that ended without
// closing it (killed, or the machine went down): the next session in the
// folder removes it, and says nothing. One a running session holds stays.
func TestAbandonedEmptyCrashFilesGo(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "ok"}, apptest.Reply{Text: "ok"})
	run(t, w, "-p", "hello").wantCode(t, 0)
	dir := filepath.Dir(w.Sessions()[0])
	left := filepath.Join(dir, "crash-20261115T024309-01a1dced.log")
	held := filepath.Join(dir, "crash-20261116T044219-01a1dd44.log")
	for _, path := range []string{left, held} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.OpenFile(held, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !transcript.Hold(f) {
		t.Fatal("the crash file could not be held")
	}
	r := run(t, w, "-p", "hello again").wantCode(t, 0)
	hasNot(t, "stderr", r.err, "crashed")
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("the empty crash file nobody holds is still there (%v)", err)
	}
	if _, err := os.Stat(held); err != nil {
		t.Errorf("the crash file a session holds was removed: %v", err)
	}
}
