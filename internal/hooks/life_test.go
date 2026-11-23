package hooks

import (
	"regexp"
	"sync"
	"testing"
	"time"
)

// Options.Lifecycle is told each hook's start and its end, with one ID
// for both: the name is the moment and what its matcher was tested
// against, the end has what the hook wrote, its exit status and how it
// went; a hook in the background is told at its start and when it ends.
func TestLifecycle(t *testing.T) {
	var mu sync.Mutex
	var lives []Life
	opts := options(t)
	opts.Lifecycle = func(l Life) {
		mu.Lock()
		lives = append(lives, l)
		mu.Unlock()
	}
	r := newRunner(t, opts,
		Hook{Event: PostToolUse, Matcher: "Bash", Command: "echo fine; echo warn >&2"},
		Hook{Event: PostToolUse, Matcher: "Bash", Command: "echo no >&2; exit 2"})
	in := input(PostToolUse)
	in.ToolName = "Bash"
	run(t, r, in)
	mu.Lock()
	got := append([]Life(nil), lives...)
	mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("told %d times, want 4: %+v", len(got), got)
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ends := map[string]Life{}
	starts := 0
	for _, l := range got {
		if l.Event != PostToolUse || l.Name != "PostToolUse:Bash" || !uuid.MatchString(l.ID) {
			t.Errorf("a life: %+v", l)
		}
		if l.Ended {
			ends[l.ID] = l
		} else {
			starts++
		}
	}
	if starts != 2 || len(ends) != 2 {
		t.Fatalf("starts %d, ends %v", starts, ends)
	}
	outcomes := map[string]bool{}
	for _, l := range ends {
		switch l.Outcome {
		case "success":
			if l.Stdout != "fine\n" || l.Stderr != "warn\n" || l.ExitCode != 0 {
				t.Errorf("the hook that went well: %+v", l)
			}
		case "error":
			if l.Stderr != "no\n" || l.ExitCode != 2 {
				t.Errorf("the hook that exited 2: %+v", l)
			}
		}
		outcomes[l.Outcome] = true
	}
	if !outcomes["success"] || !outcomes["error"] {
		t.Errorf("outcomes: %v", outcomes)
	}

	// In the background: its start now, its end when it is done.
	mu.Lock()
	lives = nil
	mu.Unlock()
	r = newRunner(t, opts, Hook{Event: Stop, Async: true, Command: "sleep 0.2; echo later"})
	run(t, r, input(Stop))
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(lives)
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lives) != 2 || lives[0].Ended || !lives[1].Ended || lives[0].ID != lives[1].ID || lives[1].Stdout != "later\n" || lives[1].Name != "Stop" {
		t.Errorf("a hook in the background: %+v", lives)
	}
}
