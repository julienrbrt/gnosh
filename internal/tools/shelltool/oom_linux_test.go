package shelltool

import (
	"os"
	"strings"
	"testing"
)

// On Linux a command, and what it starts, is the first the kernel kills
// when memory runs out: oom_score_adj 1000. gnosh itself keeps its own.
func TestCommandsGoFirstWhenMemoryRunsOut(t *testing.T) {
	before, err := os.ReadFile("/proc/self/oom_score_adj")
	if err != nil {
		t.Skipf("no /proc/self/oom_score_adj here: %v", err)
	}
	m := newManager(t, Options{})
	env := newEnv(t)
	res := bash(t, m, env, "cat /proc/self/oom_score_adj; sh -c 'cat /proc/self/oom_score_adj'")
	if got := strings.Fields(printed(res)); len(got) != 2 || got[0] != "1000" || got[1] != "1000" {
		t.Errorf("the command and its child have oom_score_adj %q, want 1000 and 1000", printed(res))
	}
	after, err := os.ReadFile("/proc/self/oom_score_adj")
	if err != nil || string(after) != string(before) {
		t.Errorf("gnosh's own oom_score_adj went from %q to %q (%v)", before, after, err)
	}
}
