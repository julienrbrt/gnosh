package sandbox

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// isInit reports whether this process is the first of its pid namespace,
// as bubblewrap's --as-pid-1 starts the relay or the filter (bwrapArgs).
func isInit() bool { return os.Getpid() == 1 }

// runChild runs the program at path as this process's child and returns
// its status as a shell gives it (128 and the signal's number for one a
// signal ended). Meanwhile it takes what is sent to end it (SIGTERM,
// SIGINT, SIGHUP, SIGQUIT) and lets it be: the program, of the same
// process group, is sent the same and ends as it likes, and the relay's
// connections go on serving it while it does. As the pid namespace's
// first process it is the namespace's init, as bubblewrap's own is where
// bubblewrap has one: what is left to it is reaped, and when the program
// ends it ends, which ends whatever is left (DECISIONS 372).
func runChild(path string, argv []string) int {
	// (One the process was started ignoring stays ignored, as the program
	// is given it: taken here, it would come back to the program as the
	// default, and end a command run under nohup at SIGHUP. The review of
	// 2026-11-20.)
	var take []os.Signal
	for _, s := range []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT} {
		if !signal.Ignored(s) {
			take = append(take, s)
		}
	}
	signal.Notify(make(chan os.Signal, 1), take...)
	p, err := os.StartProcess(path, argv, &os.ProcAttr{Env: os.Environ(), Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gnosh: %s: %v\n", argv[0], err)
		return 126
	}
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, 0, nil)
		switch {
		case err == syscall.EINTR:
		case err != nil:
			fmt.Fprintf(os.Stderr, "gnosh: waiting for %s: %v\n", argv[0], err)
			return 127
		case pid != p.Pid:
			// (A process left to the namespace's init, reaped.)
		case ws.Signaled():
			return 128 + int(ws.Signal())
		case ws.Exited():
			return ws.ExitStatus()
		}
	}
}
