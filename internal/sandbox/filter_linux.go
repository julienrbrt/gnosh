package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

// What prctl(2) is asked.
const (
	prSetDumpable     = 4
	prSetSeccomp      = 22
	prSetNoNewPrivs   = 38
	seccompModeFilter = 2
)

// sockFprog is a classic BPF program as prctl takes it.
type sockFprog struct {
	Len    uint16
	_      [6]byte
	Filter *sockFilter
}

// FilterMain is gnosh run as "sandbox-filter <program> <args...>" inside
// the sandbox: it puts the filter on itself (unixFilter) and becomes the
// program, which keeps it, as do the programs it starts.
func FilterMain(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "gnosh sandbox-filter: a program to run")
		return 2
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gnosh: %v\n", err)
		return 127
	}
	// (The filter is this thread's, and the thread that becomes the
	// program must be the one that has it.)
	runtime.LockOSThread()
	if err := refuseUnixSockets(); err != nil {
		fmt.Fprintf(os.Stderr, "gnosh: the sandbox could not refuse Unix sockets here (%v)\n", err)
		return 126
	}
	if isInit() {
		// The sandbox's first process (no relay before it): it stays, as
		// the namespace's init, and starts the program from this thread,
		// whose filter the program takes with it. Not dumpable, as an init
		// a command could attach to would make sockets for it (bubblewrap's
		// own init has no filter: the third sandbox review).
		notDumpable()
		return runChild(path, args)
	}
	err = syscall.Exec(path, args, os.Environ())
	fmt.Fprintf(os.Stderr, "gnosh: %s: %v\n", args[0], err)
	return 126
}

// refuseUnixSockets puts the filter on the calling thread, for good.
func refuseUnixSockets() error {
	a, ok := filterArches[runtime.GOARCH]
	if !ok {
		return errors.New("gnosh has no filter for " + runtime.GOARCH)
	}
	prog := unixFilter(a)
	if _, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); e != 0 {
		return e
	}
	fp := sockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	if _, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetSeccomp, seccompModeFilter, uintptr(unsafe.Pointer(&fp)), 0, 0, 0); e != 0 {
		return e
	}
	runtime.KeepAlive(prog)
	return nil
}

// notDumpable keeps another process of the same user, a sandboxed
// command, from attaching to this one or reading its memory: the relay,
// which may make Unix sockets, is not to be made to make one for it.
func notDumpable() {
	_, _, _ = syscall.RawSyscall6(syscall.SYS_PRCTL, prSetDumpable, 0, 0, 0, 0, 0)
}

// canFilter reports whether there is a filter for this machine.
func canFilter() bool {
	_, ok := filterArches[runtime.GOARCH]
	return ok
}
