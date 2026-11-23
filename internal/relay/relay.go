// Package relay is gnosh ssh: ssh as it runs by itself, with what it
// writes read on its way to the terminal, and the text of each clipboard
// write the far side sends (OSC 52) put on this machine's clipboard too.
// Apple's Terminal drops OSC 52, so without the relay what is copied on
// the far side (gnosh's /copy, tmux's copy mode) never reaches the Mac's
// clipboard; a shell function in Apple's Terminal can send each
// interactive ssh through gnosh ssh.
//
// ssh keeps the terminal it was given: it reads what is typed, sets the
// terminal up, asks for passwords on it, writes its own messages to it,
// and is stopped and continued with the shell's job, as it is by itself.
// Only its standard output passes through gnosh, which writes it on as it
// comes.
//
// Where there is no terminal, no clipboard of this machine's to put text
// on, or ssh is to go on in the background once it has logged in (-f),
// gnosh ssh is ssh: gnosh hands its process over to it.
//
// The relay puts on the clipboard what the far side sends, and never
// reads the clipboard back. As in a terminal that takes OSC 52 itself,
// anything shown on the far side's screen may put text on the clipboard;
// plain ssh in Apple's Terminal keeps the clipboard out of the far side's
// reach.
package relay

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gnosh-ai/gnosh/internal/clipboard"
	"github.com/gnosh-ai/gnosh/internal/term"
)

// Options is what Run needs.
type Options struct {
	// SSH is the ssh to run: a path, or a name on the PATH.
	SSH string
	// Args are ssh's arguments, as they were given.
	Args []string
	// In, Out and Err are gnosh's standard input, output and error.
	In, Out, Err *os.File
	// Copy puts text on this machine's clipboard, and gives up when ctx
	// ends. When it is nil, ssh runs with nothing in between.
	Copy func(ctx context.Context, text string) error
	// Exec lets gnosh hand its process over to ssh where there is nothing
	// to relay, as gnosh ssh does; otherwise ssh runs as gnosh's child.
	Exec bool
}

// drainWait is how long gnosh waits, once ssh has ended, for the rest of
// what it wrote: something it left running may hold its output open.
const drainWait = 2 * time.Second

// copyWait is how long the far side's last copy is given at the end.
const copyWait = 3 * time.Second

// Main is gnosh ssh with args: it runs ssh, through the relay where there
// is a clipboard to relay to, and returns ssh's exit status. When a signal
// ended ssh, gnosh ends by the same signal, so that a shell that stops a
// loop at Ctrl+C stops it here too.
func Main(args []string) int {
	o := Options{SSH: "ssh", Args: args, In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Exec: true}
	if tool := clipboard.Tool(runtime.GOOS, os.Getenv, exec.LookPath); tool != nil {
		o.Copy = func(ctx context.Context, text string) error { return clipboard.Copy(ctx, tool, nil, text) }
	}
	code, sig := run(o)
	if sig != 0 {
		dieOf(sig)
	}
	return code
}

// Run runs ssh as o says and returns its exit status: 128 and the
// signal's number when a signal ended it, and, as a shell's, 127 when
// there is no such ssh and 126 when it cannot be run.
func Run(o Options) int {
	code, _ := run(o)
	return code
}

// run is Run, and says which signal ended ssh, if one did.
func run(o Options) (int, syscall.Signal) {
	path, err := exec.LookPath(o.SSH)
	if err != nil {
		fmt.Fprintf(o.Err, "gnosh ssh: %v\n", err)
		return 127, 0
	}
	if o.Copy == nil || !term.IsTerminal(o.In) || !term.IsTerminal(o.Out) || forks(o.Args) {
		return plain(path, o)
	}
	return relay(path, o)
}

// command is ssh at path, run with o's arguments, input and output.
func command(path string, o Options) *exec.Cmd {
	cmd := exec.Command(path, o.Args...)
	// (Named as it was asked for, as a shell names what it runs.)
	cmd.Args[0] = o.SSH
	cmd.Stdin, cmd.Stdout, cmd.Stderr = o.In, o.Out, o.Err
	return cmd
}

// sshValued are the options of ssh that take a value.
const sshValued = "BDEFIJLOPQRSWbceilmopw"

// forks reports whether ssh's arguments have it go on in the background
// once it has logged in (-f, or -o ForkAfterAuthentication=yes): what it
// writes from then on is not for gnosh, which ends with ssh's first
// process, to pass on. ssh reads options after the host too; the command
// begins at the next word that is no option, and nothing after -- is one.
func forks(args []string) bool {
	host := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return false
		case len(a) < 2 || a[0] != '-':
			if host {
				return false
			}
			host = true
			continue
		}
		for j := 1; j < len(a); j++ {
			c := a[j]
			if c == 'f' {
				return true
			}
			if strings.IndexByte(sshValued, c) < 0 {
				continue
			}
			val := a[j+1:]
			if val == "" && i+1 < len(args) {
				i++
				val = args[i]
			}
			if c == 'o' && forkOption(val) {
				return true
			}
			break
		}
	}
	return false
}

// forkOption reports whether the value of an -o, "key=value" or "key
// value", is ForkAfterAuthentication yes (or true, which ssh takes as
// yes).
func forkOption(s string) bool {
	s = strings.TrimSpace(s)
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		k, v, ok = strings.Cut(s, " ")
	}
	v = strings.TrimSpace(v)
	return ok && strings.EqualFold(strings.TrimSpace(k), "ForkAfterAuthentication") && (strings.EqualFold(v, "yes") || strings.EqualFold(v, "true"))
}

// pass writes what comes from r on to out as it comes, and hands found
// the text of each clipboard write in it. When out takes no more (the
// terminal is gone), what comes is still read, so that ssh is never left
// waiting to write it.
func pass(r io.Reader, out io.Writer, found func(text string)) {
	var sc scanner
	buf := make([]byte, 32*1024)
	shown := true
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if shown {
				_, werr := out.Write(buf[:n])
				shown = werr == nil
			}
			sc.feed(buf[:n], found)
		}
		if err != nil {
			return
		}
	}
}
