//go:build linux

package relay

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/gnosh-ai/gnosh/internal/term"
)

// screen is a terminal for a test: the master of a pseudo-terminal whose
// slave is gnosh ssh's input and output. What the test writes is typed;
// what it reads is what reached the screen.
type screen struct {
	t      *testing.T
	master *os.File
	slave  *os.File
	mu     sync.Mutex
	shown  bytes.Buffer
}

func newScreen(t *testing.T, rows, cols uint16) *screen {
	t.Helper()
	master, slave, err := term.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	s := &screen{t: t, master: master, slave: slave}
	s.size(rows, cols)
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			s.mu.Lock()
			s.shown.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		// (With the slave closed, and ssh gone, the reading ends.)
		slave.Close()
		select {
		case <-read:
		case <-time.After(5 * time.Second):
			t.Error("the screen was still being read after the test")
		}
		master.Close()
	})
	return s
}

// control runs fn with f's descriptor, leaving f as it was.
func control(t *testing.T, f *os.File, fn func(fd int) error) {
	t.Helper()
	c, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var ferr error
	if err := c.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		t.Fatal(err)
	}
	if ferr != nil {
		t.Fatal(ferr)
	}
}

// size sets the terminal's size.
func (s *screen) size(rows, cols uint16) {
	s.t.Helper()
	control(s.t, s.master, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
}

// settings are the terminal's settings.
func (s *screen) settings() *unix.Termios {
	s.t.Helper()
	var tio *unix.Termios
	control(s.t, s.slave, func(fd int) (err error) {
		tio, err = unix.IoctlGetTermios(fd, unix.TCGETS)
		return err
	})
	return tio
}

// see waits for the screen to show piece.
func (s *screen) see(piece string) {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s.mu.Lock()
		shown := s.shown.String()
		s.mu.Unlock()
		if strings.Contains(shown, piece) {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("the screen never showed %q; it shows:\n%q", piece, shown)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// shows reports whether the screen has shown piece.
func (s *screen) shows(piece string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(s.shown.String(), piece)
}

// script writes a stand-in for ssh, the shell script body, and returns
// its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeSSH is a stand-in for ssh that prints its arguments and its
// terminal's size, writes a line to its standard error, copies "copied
// text" with an escape that would end a bracketed paste in it (OSC 52),
// reads a line, prints the size again (after a resize, if the test made
// one), and ends with status 7.
func fakeSSH(t *testing.T) string {
	return script(t, `echo "args: $*"
echo "size: $(stty size)"
echo "to stderr" >&2
printf '\033]52;c;%s\007' "$(printf 'copied \033[201~text' | base64)"
IFS= read -r line
echo "got: $line"
echo "size: $(stty size)"
exit 7
`)
}

// gnosh ssh at a terminal: ssh gets its arguments and the terminal itself,
// so it reads what is typed and sees each size the terminal takes; what it
// writes reaches the screen as it was written, its errors go where gnosh's
// go, and the text it copies reaches the clipboard; and gnosh ssh ends with
// ssh's status, the terminal's settings never touched.
func TestRelay(t *testing.T) {
	scr := newScreen(t, 30, 100)
	before := scr.settings()
	errs, err := os.CreateTemp(t.TempDir(), "err")
	if err != nil {
		t.Fatal(err)
	}
	defer errs.Close()
	var mu sync.Mutex
	var copied []string
	ssh := fakeSSH(t)
	done := make(chan int, 1)
	go func() {
		done <- Run(Options{SSH: ssh, Args: []string{"-p", "2222", "box"}, In: scr.slave, Out: scr.slave, Err: errs,
			Copy: func(_ context.Context, text string) error {
				mu.Lock()
				copied = append(copied, text)
				mu.Unlock()
				return nil
			}})
	}()
	scr.see("args: -p 2222 box")
	scr.see("size: 30 100")
	scr.see("\x1b]52;c;")
	scr.size(40, 120)
	if _, err := io.WriteString(scr.master, "hello\r"); err != nil {
		t.Fatal(err)
	}
	scr.see("got: hello")
	scr.see("size: 40 120")
	select {
	case code := <-done:
		if code != 7 {
			t.Errorf("gnosh ssh ended with %d, want ssh's 7", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("gnosh ssh did not end when ssh did")
	}
	mu.Lock()
	// (Without the escape: the relay review of 2026-11-19.)
	if len(copied) != 1 || copied[0] != "copied [201~text" {
		t.Errorf("copied %q, want the one write, without its escape", copied)
	}
	mu.Unlock()
	if b, _ := os.ReadFile(errs.Name()); string(b) != "to stderr\n" || scr.shows("to stderr") {
		t.Errorf("ssh's errors went to %q and the screen showed them: %v; want them where gnosh's go only", b, scr.shows("to stderr"))
	}
	if after := scr.settings(); after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("the terminal's settings changed: %+v, were %+v", after, before)
	}
}

// Without a terminal, or with no clipboard to copy to, gnosh ssh is ssh:
// nothing in between, and ssh's status. One that cannot be found ends
// with 127, as a shell's would.
func TestPlain(t *testing.T) {
	ssh := fakeSSH(t)
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	called := false
	code := Run(Options{SSH: ssh, Args: []string{"box"}, In: in, Out: out, Err: out, Copy: func(context.Context, string) error { called = true; return nil }})
	if code != 7 || called {
		t.Errorf("status %d, copy called %v; want 7 and no copy", code, called)
	}
	b, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(b), "args: box") || !strings.Contains(string(b), "\x1b]52;c;") {
		t.Errorf("ssh's output was not passed on: %q", b)
	}
	if code := Run(Options{SSH: filepath.Join(t.TempDir(), "no-ssh"), In: in, Out: out, Err: out}); code != 127 {
		t.Errorf("a missing ssh ended with %d, want 127", code)
	}
}

// When a signal ends ssh, gnosh ssh says which; and a signal gnosh is
// sent that ends a program is passed on to ssh.
func TestSignals(t *testing.T) {
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	code, sig := run(Options{SSH: script(t, "kill -INT $$\nsleep 5\n"), In: in, Out: out, Err: out})
	if code != 130 || sig != syscall.SIGINT {
		t.Errorf("ssh ended by Ctrl+C: gnosh ssh says %d and %v, want 130 and SIGINT", code, sig)
	}

	pidFile := filepath.Join(t.TempDir(), "pid")
	ssh := script(t, "echo $$ > "+pidFile+".tmp && mv "+pidFile+".tmp "+pidFile+"\nexec sleep 30\n")
	type ended struct {
		code int
		sig  syscall.Signal
	}
	done := make(chan ended, 1)
	go func() {
		code, sig := run(Options{SSH: ssh, In: in, Out: out, Err: out})
		done <- ended{code, sig}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(pidFile); err == nil {
			if _, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in for ssh never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-done:
		if e.code != 128+int(syscall.SIGTERM) || e.sig != syscall.SIGTERM {
			t.Errorf("gnosh was sent SIGTERM: ssh ended with %d and %v, want 143 and SIGTERM", e.code, e.sig)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SIGTERM sent to gnosh did not reach ssh")
	}
}
