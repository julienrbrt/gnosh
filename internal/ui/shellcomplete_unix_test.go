//go:build unix

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A PATH entry that is a fifo is passed over without being opened: an
// open of a fifo waits for ever for a writer.
func TestPathProgramsPassesOverAFifo(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	got := make(chan []string, 1)
	go func() { got <- pathPrograms(strings.Join([]string{fifo, bin}, string(os.PathListSeparator))) }()
	select {
	case names := <-got:
		if strings.Join(names, ",") != "tool" {
			t.Errorf("found %q", names)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the list of programs is still being made: the fifo on the PATH was opened")
	}
}
