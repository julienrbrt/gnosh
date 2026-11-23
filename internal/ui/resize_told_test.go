package ui

import (
	"testing"

	"github.com/gnosh-ai/gnosh/internal/text"
)

// A new size of the terminal is told to the session, whose status line
// command is told the size; the same size again is not.
func TestResizeIsTold(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.resize(80, 24)
	g.resize(80, 24)
	g.c.mu.Lock()
	n := g.c.resized
	g.c.mu.Unlock()
	if n != 1 {
		t.Errorf("Resized was called %d times, want 1", n)
	}
}
