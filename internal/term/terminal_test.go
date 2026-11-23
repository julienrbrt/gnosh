//go:build linux || darwin

package term

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// onFd runs fn with f's file descriptor, without changing how f is read.
func onFd(f *os.File, fn func(fd int) error) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := conn.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

// settings reads a terminal's settings (termios).
func settings(t *testing.T, f *os.File) unix.Termios {
	t.Helper()
	var out unix.Termios
	err := onFd(f, func(fd int) error {
		tio, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
		if err == nil {
			out = *tio
		}
		return err
	})
	if err != nil {
		t.Fatalf("read the terminal's settings: %v", err)
	}
	return out
}

// pty is a pseudo-terminal with a Terminal open on its slave side.
type pty struct {
	t      *testing.T
	master *os.File
	slave  *os.File
	term   *Terminal
	before unix.Termios // the settings before Open
	output string       // what the Terminal has written, as read so far
}

func openTerminal(t *testing.T, opts Options) *pty {
	t.Helper()
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	p := &pty{t: t, master: master, slave: slave}
	p.before = settings(t, slave)
	p.term, err = Open(slave, slave, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.term.Close()
		master.Close()
		slave.Close()
	})
	return p
}

// typeKeys sends bytes as if the person typed them.
func (p *pty) typeKeys(s string) {
	p.t.Helper()
	if _, err := p.master.WriteString(s); err != nil {
		p.t.Fatal(err)
	}
}

// next waits for the next event.
func (p *pty) next() Event {
	p.t.Helper()
	select {
	case ev, ok := <-p.term.Events():
		if !ok {
			p.t.Fatal("the events channel is closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		p.t.Fatal("no event arrived")
	}
	return nil
}

// want checks the next events.
func (p *pty) want(events ...Event) {
	p.t.Helper()
	for _, want := range events {
		if got := p.next(); !reflect.DeepEqual(got, want) {
			p.t.Fatalf("event %T{%s}, want %T{%s}", got, got, want, want)
		}
	}
}

// quiet checks that no event arrives for a little while.
func (p *pty) quiet() {
	p.t.Helper()
	select {
	case ev, ok := <-p.term.Events():
		p.t.Fatalf("unexpected event %v (channel open: %v)", ev, ok)
	case <-time.After(30 * time.Millisecond):
	}
}

// wrote waits until the Terminal has written text to the terminal.
func (p *pty) wrote(text string) {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1024)
	for !strings.Contains(p.output, text) {
		if time.Now().After(deadline) {
			p.t.Fatalf("the terminal was sent %q, which lacks %q", p.output, text)
		}
		p.master.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		n, _ := p.master.Read(buf)
		p.output += string(buf[:n])
	}
	p.output = p.output[strings.Index(p.output, text)+len(text):]
}

func TestOpenNeedsATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, err := Open(r, w, Options{}); err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("Open on a pipe: error %v, want one that says it is not a terminal", err)
	}
	if _, err := Open(nil, nil, Options{}); err == nil {
		t.Error("Open with no files succeeded")
	}
	if IsTerminal(r) || IsTerminal(nil) {
		t.Error("IsTerminal says a pipe or nothing is a terminal")
	}
	if _, _, err := GetSize(w); err == nil {
		t.Error("GetSize on a pipe succeeded")
	}
	// Output that is not a terminal is refused too, and the input's
	// settings are left alone.
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	before := settings(t, slave)
	if _, err := Open(slave, w, Options{}); err == nil {
		t.Error("Open with a pipe for output succeeded")
	}
	if settings(t, slave) != before {
		t.Error("a failed Open changed the terminal's settings")
	}
	if !IsTerminal(slave) {
		t.Error("IsTerminal says a pseudo-terminal is not a terminal")
	}
}

func TestRawModeAndRestore(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true})
	raw := settings(t, p.slave)
	if raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG|unix.IEXTEN) != 0 {
		t.Errorf("local flags %#x: canonical mode, echo, signals or extensions still on", raw.Lflag)
	}
	if raw.Oflag&unix.OPOST != 0 || raw.Iflag&(unix.ICRNL|unix.IXON) != 0 {
		t.Errorf("output flags %#x, input flags %#x: not raw", raw.Oflag, raw.Iflag)
	}
	if err := p.term.Close(); err != nil {
		t.Fatal(err)
	}
	if after := settings(t, p.slave); after != p.before {
		t.Errorf("settings after Close differ from those before Open:\n got %+v\nwant %+v", after, p.before)
	}
	if err := p.term.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := p.term.SetModes(Modes{}); !errors.Is(err, ErrClosed) {
		t.Errorf("SetModes after Close: %v, want ErrClosed", err)
	}
	if _, ok := <-p.term.Events(); ok {
		t.Error("the events channel is open after Close")
	}
	if p.term.Err() != nil {
		t.Errorf("Err after Close = %v, want nil", p.term.Err())
	}
}

func TestKeysArrive(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true})
	// Ctrl+C is a key here, not a signal.
	p.typeKeys("a\x1b[A\x03\x1a\x1b[13;2u世")
	p.want(txt('a'), key(KeyUp, 0), chr('c', ModCtrl), chr('z', ModCtrl), key(KeyEnter, ModShift), txt('世'))
	p.typeKeys("\x1b[200~pasted\rtext\x1b[201~")
	p.want(Paste{Text: "pasted\ntext"})
}

func TestLoneEscapeWaits(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true, EscWait: 30 * time.Millisecond})
	start := time.Now()
	p.typeKeys("\x1b")
	p.want(key(KeyEscape, 0))
	if waited := time.Since(start); waited < 25*time.Millisecond {
		t.Errorf("Esc was reported after %v, before the wait for a longer sequence ran out", waited)
	}
	// Alt+[ on its own is reported when nothing follows.
	p.typeKeys("\x1b[")
	p.want(chr('[', ModAlt))
	// Half a sequence is not given up on: its tail is never typed text.
	p.typeKeys("\x1b[1;")
	p.quiet()
	p.typeKeys("5C")
	p.want(key(KeyRight, ModCtrl))

	// A sequence that arrives in two pieces within the wait is one key.
	// (The wait is long here so that a busy machine cannot stretch the
	// pause between the pieces past it.)
	q := openTerminal(t, Options{NoSignals: true, EscWait: 5 * time.Second})
	q.typeKeys("\x1b")
	time.Sleep(5 * time.Millisecond)
	q.typeKeys("[")
	time.Sleep(5 * time.Millisecond)
	q.typeKeys("B")
	q.want(key(KeyDown, 0))
}

func TestResizeEvent(t *testing.T) {
	p := openTerminal(t, Options{})
	err := onFd(p.master, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 100})
	})
	if err != nil {
		t.Fatal(err)
	}
	// The pseudo-terminal is not this process's controlling terminal, so
	// the system does not send the signal itself.
	unix.Kill(unix.Getpid(), unix.SIGWINCH)
	p.want(Resize{Cols: 100, Rows: 30})
	if cols, rows, err := p.term.Size(); err != nil || cols != 100 || rows != 30 {
		t.Errorf("Size = %d, %d, %v; want 100, 30", cols, rows, err)
	}
}

func TestCloseLeavesNoGoroutines(t *testing.T) {
	// The first use of os/signal in a process starts a goroutine of its
	// own that never ends; start it before counting.
	warm := make(chan os.Signal, 1)
	signal.Notify(warm, syscall.SIGWINCH)
	signal.Stop(warm)
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		master, slave, err := openPTY()
		if err != nil {
			t.Skipf("no pseudo-terminal: %v", err)
		}
		term, err := Open(slave, slave, Options{})
		if err != nil {
			t.Fatal(err)
		}
		master.WriteString("abc\x1b")
		if i%2 == 0 {
			<-term.Events() // sometimes someone is reading, sometimes not
		}
		if err := term.Close(); err != nil {
			t.Fatal(err)
		}
		master.Close()
		slave.Close()
	}
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines before, %d after:\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// When the terminal goes away the reader ends by itself.
func TestTerminalGoesAway(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true})
	p.typeKeys("x")
	p.want(txt('x'))
	p.master.Close()
	select {
	case ev, ok := <-p.term.Events():
		if ok {
			t.Fatalf("event %v after the terminal went away", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the events channel was not closed")
	}
	if err := p.term.Err(); err != io.EOF {
		t.Errorf("Err = %v, want io.EOF", err)
	}
	p.term.Close() // the settings cannot be restored on a terminal that is gone
}

func TestReleaseAndResume(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true})
	if err := p.term.SetModes(Modes{BracketedPaste: true, FocusReporting: true, KittyKeyboard: true}); err != nil {
		t.Fatal(err)
	}
	p.wrote(BracketedPasteOn + FocusReportingOn + KittyKeyboardPush)
	p.typeKeys("a")
	p.want(txt('a'))

	if err := p.term.Release(); err != nil {
		t.Fatal(err)
	}
	if got := settings(t, p.slave); got != p.before {
		t.Errorf("settings while released differ from those before Open")
	}
	p.wrote(ShowCursor + KittyKeyboardPop + FocusReportingOff + BracketedPasteOff)
	// What is typed now belongs to whoever has the terminal.
	p.typeKeys("child\n")
	p.quiet()

	if err := p.term.Resume(); err != nil {
		t.Fatal(err)
	}
	if got := settings(t, p.slave); got.Lflag&unix.ICANON != 0 {
		t.Error("the terminal is not raw again after Resume")
	}
	p.wrote(BracketedPasteOn + FocusReportingOn + KittyKeyboardPush)
	p.want(Resume{})
	if _, isResize := p.next().(Resize); !isResize {
		t.Error("no Resize after Resume")
	}
	// The child did not read its input; it is still there.
	p.want(txt('c'), txt('h'), txt('i'), txt('l'), txt('d'), chr('j', ModCtrl))

	// Turning one mode off writes only that.
	p.term.SetModes(Modes{BracketedPaste: true, FocusReporting: true})
	p.wrote(KittyKeyboardPop)
	p.term.Close()
	p.wrote(FocusReportingOff + BracketedPasteOff)
}

func TestCursorPositionAndClipboard(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true})
	// CSI 1 ; 5 R is Ctrl+F3 until a cursor position has been asked for.
	p.typeKeys("\x1b[1;5R")
	p.want(key(KeyF3, ModCtrl))
	if err := p.term.QueryCursorPosition(); err != nil {
		t.Fatal(err)
	}
	p.wrote(QueryCursorPosition)
	p.typeKeys("\x1b[1;5R")
	p.want(Reply{Kind: ReplyCursorPosition, Row: 1, Col: 5})

	if err := p.term.CopyToClipboard("secret"); !errors.Is(err, ErrClipboardOff) {
		t.Errorf("CopyToClipboard with the clipboard off: %v, want ErrClipboardOff", err)
	}
	p.term.WriteString("marker")
	p.wrote("marker")
	if strings.Contains(p.output, "]52") {
		t.Errorf("something was written to the clipboard: %q", p.output)
	}

	q := openTerminal(t, Options{NoSignals: true, Clipboard: true})
	if err := q.term.CopyToClipboard("hello"); err != nil {
		t.Fatal(err)
	}
	q.wrote("\x1b]52;c;aGVsbG8=\x1b\\")
}

// A panic in a goroutine that deferred RestoreOnPanic leaves the terminal
// as it was found.
func TestRestoreOnPanic(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true})
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want the original panic", r)
			}
		}()
		defer p.term.RestoreOnPanic()
		panic("boom")
	}()
	if got := settings(t, p.slave); got != p.before {
		t.Error("the terminal's settings were not restored after a panic")
	}
	// With no panic it does nothing.
	q := openTerminal(t, Options{NoSignals: true})
	func() { defer q.term.RestoreOnPanic() }()
	if got := settings(t, q.slave); got.Lflag&unix.ICANON != 0 {
		t.Error("RestoreOnPanic restored the terminal although nothing panicked")
	}
}

// The whole screen a Panel takes is given back with the terminal, whatever
// gives it up (an editor, a stop, a panic), and taken again with it. The
// shell was left on the alternate screen with mouse reporting on, every
// click typed into it (the TUI review of 2026-11-19).
func TestPanelScreenGoesWithTheTerminal(t *testing.T) {
	p := openTerminal(t, Options{NoSignals: true})
	panel, err := OpenPanel(p.term, 120, 30, func(s string) int { return len(s) }, false)
	if err != nil {
		t.Fatal(err)
	}
	p.wrote(altScreenOn + AutoWrapOff + mouseOn)
	if err := p.term.Release(); err != nil {
		t.Fatal(err)
	}
	p.wrote(mouseOff + altScreenOff)
	if err := p.term.Resume(); err != nil {
		t.Fatal(err)
	}
	p.wrote(altScreenOn + AutoWrapOff + mouseOn)
	func() {
		defer func() { _ = recover() }()
		defer p.term.RestoreOnPanic()
		panic("boom")
	}()
	p.wrote(mouseOff + altScreenOff)

	// A panel that was closed leaves nothing to give back.
	q := openTerminal(t, Options{NoSignals: true})
	panel2, err := OpenPanel(q.term, 120, 30, func(s string) int { return len(s) }, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := panel2.Close(); err != nil {
		t.Fatal(err)
	}
	q.wrote(altScreenOff)
	if err := q.term.Release(); err != nil {
		t.Fatal(err)
	}
	q.wrote(ShowCursor)
	if strings.Contains(q.output, altScreenOff) {
		t.Errorf("a closed panel's screen was given back again: %q", q.output)
	}
	_ = panel
}
