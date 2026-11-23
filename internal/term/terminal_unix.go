//go:build unix

package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"
)

// suspendWait is how long Suspend, once the process runs again, waits to
// be told that it was continued, before it takes the terminal back
// anyway. It waits that long at most twice.
const suspendWait = 200 * time.Millisecond

// Terminal is a terminal in raw mode: every key arrives as it is pressed,
// nothing is echoed, and what gnosh writes is passed through untouched (a
// line feed moves down and does not return the carriage, so lines end with
// "\r\n").
//
// In raw mode the terminal sends no signals for keys. Ctrl+C, Ctrl+Z and
// Ctrl+\ arrive as Key events like any other; what they do is up to the
// program. (Suspend does what Ctrl+Z usually does.)
//
// The terminal is put back exactly as it was found by Close, by Release
// and Suspend for as long as they last, and when the process is told to
// end (SIGTERM, SIGHUP, SIGINT, SIGQUIT) or to stop (SIGTSTP). Go runs no
// deferred calls when a goroutine other than the one that deferred them
// panics, so every goroutine that could panic while the terminal is raw
// should defer RestoreOnPanic.
type Terminal struct {
	in, out     *os.File
	inFd, outFd int
	opts        Options
	escWait     time.Duration
	events      chan Event

	// The reader waits in select(2) on the terminal and on wakeR; a
	// byte written to wakeW, with a token in kick for when the reader
	// is waiting to hand over an event instead, gets its attention.
	wakeR, wakeW int
	kick         chan struct{}
	done         chan struct{} // closed by Close
	readerDone   chan struct{} // closed when the reader has ended
	paused       chan struct{} // the reader says it has stopped reading
	unpause      chan struct{} // tells the reader to read again
	wg           sync.WaitGroup

	mu         sync.Mutex
	dec        Decoder
	saved      *xterm.State // the terminal's settings as they were found
	raw        bool         // the terminal is in raw mode
	released   bool         // between Release and Resume
	suspending bool         // Suspend is waiting for the process to be continued
	closed     bool
	modes      Modes
	ctl        requests
	err        error

	sigs chan os.Signal
	cont chan struct{} // the process was continued while Suspend waited

	// screen says a Panel has the whole screen: the alternate screen,
	// with mouse reporting on and the line wrapping off (noteScreen). It
	// is read without the lock by RestoreOnPanic.
	screen atomic.Bool
}

// requests are what other goroutines ask of the reader.
type requests struct {
	stop    bool // end
	pause   bool // stop reading until unpaused
	winch   bool // report the terminal's size
	resumed bool // report that the terminal is gnosh's again
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool {
	return f != nil && xterm.IsTerminal(int(f.Fd()))
}

// GetSize is the size in character cells of the terminal f is open on. It
// needs no Terminal, so a program whose input is not a terminal can still
// lay out what it prints.
func GetSize(f *os.File) (cols, rows int, err error) {
	if f == nil {
		return 0, 0, errors.New("terminal size: no file")
	}
	cols, rows, err = xterm.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0, fmt.Errorf("terminal size of %s: %w", f.Name(), err)
	}
	return cols, rows, nil
}

// Open takes over the terminal that in and out are open on (usually
// standard input and standard output): it puts it in raw mode and starts
// reading keys. It fails, changing nothing, when either is not a terminal;
// a program can then carry on without a Terminal, using IsTerminal, GetSize
// and a Renderer on its output.
func Open(in, out *os.File, opts Options) (*Terminal, error) {
	if !IsTerminal(in) {
		return nil, fmt.Errorf("open terminal: input %s is not a terminal", fileName(in))
	}
	if !IsTerminal(out) {
		return nil, fmt.Errorf("open terminal: output %s is not a terminal", fileName(out))
	}
	t := &Terminal{
		in: in, out: out, inFd: int(in.Fd()), outFd: int(out.Fd()),
		opts:       opts,
		escWait:    DefaultEscWait,
		events:     make(chan Event),
		kick:       make(chan struct{}, 1),
		done:       make(chan struct{}),
		readerDone: make(chan struct{}),
		paused:     make(chan struct{}),
		unpause:    make(chan struct{}),
		cont:       make(chan struct{}, 1),
	}
	if opts.EscWait > 0 {
		t.escWait = opts.EscWait
	}
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		return nil, fmt.Errorf("open terminal: make a pipe: %w", err)
	}
	t.wakeR, t.wakeW = p[0], p[1]
	for _, fd := range p {
		unix.CloseOnExec(fd)
		unix.SetNonblock(fd, true)
	}
	if t.inFd >= unix.FD_SETSIZE || t.wakeR >= unix.FD_SETSIZE {
		unix.Close(t.wakeR)
		unix.Close(t.wakeW)
		return nil, fmt.Errorf("open terminal: file descriptor %d is too large to wait on", max(t.inFd, t.wakeR))
	}
	saved, err := xterm.MakeRaw(t.inFd)
	if err != nil {
		unix.Close(t.wakeR)
		unix.Close(t.wakeW)
		return nil, fmt.Errorf("open terminal: put %s in raw mode: %w", in.Name(), err)
	}
	t.saved, t.raw = saved, true

	t.wg.Add(1)
	go t.readLoop()
	if !opts.NoSignals {
		t.sigs = make(chan os.Signal, 8)
		signal.Notify(t.sigs, syscall.SIGWINCH, syscall.SIGCONT, syscall.SIGTSTP,
			syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
		t.wg.Add(1)
		go t.signalLoop()
	}
	return t, nil
}

func fileName(f *os.File) string {
	if f == nil {
		return "(none)"
	}
	return f.Name()
}

// Events is where everything the terminal says arrives: keys, pastes,
// answers, Resize and Resume. The channel is closed when the terminal's
// input ends or the Terminal is closed; Err then says which.
func (t *Terminal) Events() <-chan Event { return t.events }

// Err is why the events channel was closed: nil after Close, io.EOF when
// the terminal went away or its input ended, or the error that stopped the
// reading.
func (t *Terminal) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// Size is the terminal's size in character cells.
func (t *Terminal) Size() (cols, rows int, err error) {
	return GetSize(t.out)
}

// Write writes to the terminal.
func (t *Terminal) Write(p []byte) (int, error) { return t.out.Write(p) }

// WriteString writes to the terminal.
func (t *Terminal) WriteString(s string) (int, error) { return t.out.WriteString(s) }

// SetModes turns terminal modes on and off to match m, and remembers them.
func (t *Terminal) SetModes(m Modes) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	old := t.modes
	t.modes = m
	if !t.raw {
		return nil
	}
	seq := ""
	change := func(was, want bool, on, off string) {
		switch {
		case want && !was:
			seq += on
		case was && !want:
			seq += off
		}
	}
	change(old.BracketedPaste, m.BracketedPaste, BracketedPasteOn, BracketedPasteOff)
	change(old.FocusReporting, m.FocusReporting, FocusReportingOn, FocusReportingOff)
	change(old.KittyKeyboard, m.KittyKeyboard, KittyKeyboardPush, KittyKeyboardPop)
	if seq == "" {
		return nil
	}
	_, err := t.out.WriteString(seq)
	return err
}

// QueryCursorPosition asks the terminal where the cursor is. The answer
// arrives as a Reply of kind ReplyCursorPosition.
func (t *Terminal) QueryCursorPosition() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	t.dec.ExpectCursorPosition()
	t.mu.Unlock()
	_, err := t.out.WriteString(QueryCursorPosition)
	return err
}

// CopyToClipboard puts text on the person's clipboard through the terminal
// (OSC 52). It does nothing but return ErrClipboardOff unless
// Options.Clipboard was set.
func (t *Terminal) CopyToClipboard(text string) error {
	if !t.opts.Clipboard {
		return ErrClipboardOff
	}
	_, err := t.out.WriteString(ClipboardWrite(text))
	return err
}

// noteScreen is told by a Panel when it takes the whole screen and when it
// gives it back, so that whatever gives the terminal up (a panic, a stop
// from outside, an editor) gives the screen back too, and takes it again
// with the terminal. They were left on: the shell ran on the alternate
// screen with every click typed into it (the TUI review of 2026-11-19).
func (t *Terminal) noteScreen(taken bool) { t.screen.Store(taken) }

// leave gives the terminal up: modes off, settings as they were found.
// The caller holds t.mu.
func (t *Terminal) leave() error {
	if !t.raw {
		return nil
	}
	t.raw = false
	seq := leaveSequence(t.modes)
	if t.screen.Load() {
		seq = screenOff + seq
	}
	_, werr := t.out.WriteString(seq)
	if err := xterm.Restore(t.inFd, t.saved); err != nil {
		return fmt.Errorf("restore terminal %s: %w", t.in.Name(), err)
	}
	if werr != nil {
		return fmt.Errorf("restore terminal %s: turn modes off: %w", t.in.Name(), werr)
	}
	return nil
}

// enter takes the terminal back: raw mode, modes on. The caller holds
// t.mu.
func (t *Terminal) enter() error {
	if t.raw {
		return nil
	}
	if _, err := xterm.MakeRaw(t.inFd); err != nil {
		return fmt.Errorf("put terminal %s in raw mode: %w", t.in.Name(), err)
	}
	t.raw = true
	seq := t.modes.on()
	if t.screen.Load() {
		seq = screenOn + seq
	}
	if seq != "" {
		if _, err := t.out.WriteString(seq); err != nil {
			return fmt.Errorf("terminal %s: turn modes on: %w", t.in.Name(), err)
		}
	}
	return nil
}

// Release hands the terminal to someone else, such as an editor gnosh is
// about to run: it stops reading, turns the modes off and puts the
// terminal's settings back as they were found. Nothing typed after it
// returns is read by gnosh. Resume takes the terminal back.
func (t *Terminal) Release() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	if t.released {
		t.mu.Unlock()
		return nil
	}
	t.released = true
	t.ctl.pause = true
	t.mu.Unlock()
	t.wake()
	select {
	case <-t.paused:
	case <-t.readerDone:
	case <-t.done:
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.leave()
}

// Resume takes the terminal back after Release: raw mode, the modes on
// again, reading again. A Resume event follows, and a Resize with the
// size the terminal has now; the interface should redraw.
func (t *Terminal) Resume() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	if !t.released {
		t.mu.Unlock()
		return nil
	}
	err := t.enter()
	t.released = false
	t.ctl.resumed = true
	t.mu.Unlock()
	select {
	case t.unpause <- struct{}{}:
	case <-t.readerDone:
	case <-t.done:
	}
	t.wake()
	return err
}

// Suspend does what Ctrl+Z does to an ordinary program: it gives the
// terminal back, stops the process (and its process group) so that the
// shell takes over, and when the shell continues the process it takes the
// terminal back as Resume does. The interface should erase its live region
// before calling it.
//
// A process with no shell behind it, that is, one that nothing would
// continue, is not stopped: Suspend then does nothing at all and returns
// ErrNoShell, so that the interface can say so.
//
// The process is stopped with SIGSTOP, not with the SIGTSTP that the
// terminal itself would send. A Go program that has once asked to be told
// of SIGTSTP, as a Terminal does so that a stop asked for from outside
// finds the terminal put back first, cannot be stopped by it again: the
// runtime keeps its own handler for the signal after os/signal.Reset, and
// that handler drops a SIGTSTP nobody listens for. SIGSTOP cannot be
// caught, and a shell treats a job stopped by either the same way. The
// system stops a process with SIGSTOP even when nothing could continue it,
// which SIGTSTP never does, and that is why the shell is looked for
// first.
func (t *Terminal) Suspend() error {
	if !shellBehind() {
		return ErrNoShell
	}
	t.mu.Lock()
	if t.suspending {
		// One stop is under way already: Ctrl+Z and a request from
		// outside came together. It serves for both.
		t.mu.Unlock()
		return nil
	}
	t.suspending = true
	t.mu.Unlock()
	if err := t.Release(); err != nil {
		t.mu.Lock()
		t.suspending = false
		t.mu.Unlock()
		return err
	}
	select {
	case <-t.cont:
	default:
	}
	// The stop takes hold as this call returns, or a moment after it when
	// another thread of the process is told first. Either way, what
	// follows runs when the shell has continued the process.
	if err := unix.Kill(0, unix.SIGSTOP); err != nil {
		t.mu.Lock()
		t.suspending = false
		t.mu.Unlock()
		if rerr := t.Resume(); rerr != nil {
			return rerr
		}
		return fmt.Errorf("stop the process: %w", err)
	}
	// The signal that says the process was continued is on its way to
	// signalLoop, which is let take it while suspending still says whose
	// stop this was. The clock went on while the process was stopped, so
	// a first wait that began before the stop took hold is over the
	// moment the process runs again. A second one cannot be.
	if t.sigs != nil {
	wait:
		for range 2 {
			timer := time.NewTimer(suspendWait)
			select {
			case <-t.cont:
				timer.Stop()
				break wait
			case <-t.done:
				timer.Stop()
				break wait
			case <-timer.C:
			}
		}
	}
	t.mu.Lock()
	t.suspending = false
	// Whatever the shell left the terminal's settings as is what to go
	// back to from now on.
	if state, err := xterm.GetState(t.inFd); err == nil && !t.closed {
		t.saved = state
	}
	t.mu.Unlock()
	return t.Resume()
}

// RestoreOnPanic puts the terminal back as it was found if the goroutine
// is panicking, and lets the panic go on. Defer it in every goroutine that
// may run while the terminal is raw:
//
//	defer t.RestoreOnPanic()
func (t *Terminal) RestoreOnPanic() {
	r := recover()
	if r == nil {
		return
	}
	if t.mu.TryLock() {
		t.leave()
		t.mu.Unlock()
	} else {
		// The lock is held, perhaps by the code that panicked.
		seq := leaveSequence(Modes{BracketedPaste: true, FocusReporting: true})
		if t.screen.Load() {
			seq = screenOff + seq
		}
		t.out.WriteString(seq)
		xterm.Restore(t.inFd, t.saved)
	}
	panic(r)
}

// Close puts the terminal back as it was found and stops the Terminal's
// goroutines. It is safe to call more than once.
func (t *Terminal) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.ctl.stop = true
	t.mu.Unlock()
	if t.sigs != nil {
		signal.Stop(t.sigs)
	}
	close(t.done)
	t.wake()
	t.wg.Wait()
	t.mu.Lock()
	err := t.leave()
	t.mu.Unlock()
	unix.Close(t.wakeR)
	unix.Close(t.wakeW)
	return err
}

// wake gets the reader's attention.
func (t *Terminal) wake() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
	// The pipe does not block: when it is full a wake is already waiting.
	unix.Write(t.wakeW, []byte{0})
}

// attend carries out what was asked of the reader, adding any events that
// come of it to pending. It reports false when the reader must end.
func (t *Terminal) attend(pending *[]Event) bool {
	for {
		t.mu.Lock()
		c := t.ctl
		t.ctl = requests{}
		t.mu.Unlock()
		if c.stop {
			return false
		}
		if c.pause {
			select {
			case t.paused <- struct{}{}:
			case <-t.done:
				return false
			}
			select {
			case <-t.unpause:
			case <-t.done:
				return false
			}
			continue
		}
		if c.resumed {
			// Half a sequence read before the terminal was given up
			// means nothing now.
			t.mu.Lock()
			t.dec.Reset()
			t.mu.Unlock()
			*pending = append(*pending, Resume{})
			c.winch = true
		}
		if c.winch {
			if cols, rows, err := xterm.GetSize(t.outFd); err == nil {
				*pending = append(*pending, Resize{Cols: cols, Rows: rows})
			}
		}
		return true
	}
}

// readLoop reads the terminal, decodes what it reads and hands the events
// over, until the input ends or Close says to stop.
func (t *Terminal) readLoop() {
	defer t.wg.Done()
	defer close(t.readerDone)
	defer close(t.events)
	buf := make([]byte, 4096)
	var pending []Event
	held := false // the decoder is holding back something a Flush would settle
	for {
		for len(pending) > 0 {
			select {
			case t.events <- pending[0]:
				pending = pending[1:]
			case <-t.kick:
				if !t.attend(&pending) {
					return
				}
			}
		}
		pending = pending[:0]

		// Wait for input or for someone to want something. While the
		// decoder holds back a possible Esc key, wait only so long.
		var set unix.FdSet
		set.Zero()
		set.Set(t.inFd)
		set.Set(t.wakeR)
		var timeout *unix.Timeval
		if held {
			tv := unix.NsecToTimeval(t.escWait.Nanoseconds())
			timeout = &tv
		}
		n, err := unix.Select(max(t.inFd, t.wakeR)+1, &set, nil, nil, timeout)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			t.end(fmt.Errorf("wait for terminal input: %w", err))
			return
		}
		if set.IsSet(t.wakeR) {
			var drain [64]byte
			for {
				if n, err := unix.Read(t.wakeR, drain[:]); n <= 0 || err != nil {
					break
				}
			}
			select {
			case <-t.kick:
			default:
			}
			if !t.attend(&pending) {
				return
			}
			continue
		}
		if n == 0 {
			// Nothing more came: the held byte was a key on its own.
			t.mu.Lock()
			pending = append(pending, t.dec.Flush()...)
			held = t.dec.Pending()
			t.mu.Unlock()
			continue
		}
		got, err := unix.Read(t.inFd, buf)
		switch {
		case got > 0:
			t.mu.Lock()
			pending = append(pending, t.dec.Feed(buf[:got])...)
			held = t.dec.Pending()
			t.mu.Unlock()
		case err == unix.EINTR || err == unix.EAGAIN:
		default:
			// The end of the input (the terminal went away, or input
			// was a file that ran out), or an error.
			t.mu.Lock()
			pending = append(pending, t.dec.Flush()...)
			t.mu.Unlock()
			for _, ev := range pending {
				select {
				case t.events <- ev:
				case <-t.done:
					return
				}
			}
			if got == 0 || err == unix.EIO {
				t.end(io.EOF)
			} else {
				t.end(fmt.Errorf("read terminal %s: %w", t.in.Name(), err))
			}
			return
		}
	}
}

// end records why the reader stopped.
func (t *Terminal) end(err error) {
	t.mu.Lock()
	if t.err == nil && !t.closed {
		t.err = err
	}
	t.mu.Unlock()
}

// signalLoop acts on the signals the process receives.
func (t *Terminal) signalLoop() {
	defer t.wg.Done()
	for {
		select {
		case <-t.done:
			return
		case sig := <-t.sigs:
			switch sig {
			case syscall.SIGWINCH:
				t.mu.Lock()
				t.ctl.winch = true
				t.mu.Unlock()
				t.wake()
			case syscall.SIGCONT:
				t.mu.Lock()
				if t.suspending {
					t.mu.Unlock()
					select {
					case t.cont <- struct{}{}:
					default:
					}
					continue
				}
				// Continued after a stop the Terminal did not arrange
				// (SIGSTOP). The shell may have changed the terminal's
				// settings meanwhile: set raw mode and the modes again.
				if t.raw && !t.closed {
					xterm.MakeRaw(t.inFd)
					t.out.WriteString(t.modes.on())
					t.ctl.resumed = true
				}
				t.mu.Unlock()
				t.wake()
			case syscall.SIGTSTP:
				t.wg.Add(1)
				go func() {
					defer t.wg.Done()
					// A request to stop a process that nothing would
					// continue is dropped, as the system itself drops
					// it for a program that does not listen for it.
					if !shellBehind() {
						return
					}
					if t.opts.OnStop != nil {
						t.opts.OnStop()
					}
					t.Suspend()
				}()
			default:
				t.terminate(sig)
			}
		}
	}
}

// terminate puts the terminal back as it was found because the process
// was told to end, and then sees that it does.
func (t *Terminal) terminate(sig os.Signal) {
	t.mu.Lock()
	t.leave()
	t.mu.Unlock()
	if t.opts.OnTerminate != nil {
		go t.opts.OnTerminate(sig)
		return
	}
	// Pass the signal on with its usual effect.
	signal.Reset(sig)
	if s, ok := sig.(syscall.Signal); ok {
		unix.Kill(unix.Getpid(), s)
	}
}
