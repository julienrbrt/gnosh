package shelltool

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
)

// How a command is stopped.
//
// Its shell was started as the leader of a new session, so the shell's pid
// is also the id of its process group and of its session. Nearly every
// process a command starts stays in that group, and one signal to the
// group reaches them all. A few move themselves to a group of their own
// (timeout(1) does; so does a job of a shell with job control); those stay
// in the session, so the session is searched for them and their groups are
// signalled too. Only a process that starts a session of its own (setsid, a
// proper daemon) gets away, and that is a process that means to outlive
// whatever started it.
//
// First every group gets SIGTERM, and SIGCONT in case it was stopped and
// could not otherwise act on the SIGTERM. If the shell or anything else in
// the session is still alive after the grace, every group gets SIGKILL.
//
// A signal to a group reaches the processes that are in it at that
// instant, as they are at that instant. Two kinds slip through. A process
// forked a moment later has missed the signal. And a process that was, at
// that instant, still its parent shell's copy, about to become the program
// it was forked to run, may take note of the signal the way the shell does
// and then forget it in becoming the program. Either way the shell, which
// did get the signal, sits and waits for a child that knows nothing of it.
// So the session is looked at again and again during the wait, and a
// process that was not there when the signal went out, or has become a
// different program since, is sent the signal by itself.

// procKey names one process for good: a pid alone can be handed out again.
type procKey struct {
	pid   int
	start uint64
}

// toldAll returns the record that every process in procs has been sent a
// signal: each process, with the program it was at the time.
func toldAll(procs []procInfo) map[procKey]string {
	told := make(map[procKey]string, len(procs))
	for _, p := range procs {
		told[procKey{p.pid, p.start}] = p.image
	}
	return told
}

// kill stops the command and everything it started. It returns the
// processes that were still alive after SIGKILL and the wait that follows
// it, which in the ordinary case is none.
func (j *job) kill() []procInfo {
	m := j.m
	told := j.signalAll(syscall.SIGTERM)
	if m.outwait(j.pid, j.exited, m.opt.TermGrace, syscall.SIGTERM, told, nil) {
		return nil
	}
	told = j.signalAll(syscall.SIGKILL)
	if m.outwait(j.pid, j.exited, m.opt.KillWait, syscall.SIGKILL, told, nil) {
		return nil
	}
	alive, _ := m.procs(j.pid)
	if !closed(j.exited) {
		// The shell itself has not been reaped. Name it even if the list
		// could not be read.
		listed := false
		for _, p := range alive {
			listed = listed || p.pid == j.pid
		}
		if !listed {
			alive = append(alive, procInfo{pid: j.pid, pgid: j.pid, name: "the shell"})
		}
	}
	return alive
}

// signalAll sends sig to every process group of the command, and returns
// the record of the processes that were in its session just before.
func (j *job) signalAll(sig syscall.Signal) map[procKey]string {
	m := j.m
	reaped := closed(j.exited)
	procs, err := m.procs(j.pid)
	// The shell's own group is signalled without looking for as long as the
	// shell is there to keep its id from being given to anything else: that
	// one call reaches every process in the group at once. It is also all
	// there is on a system where the session cannot be listed.
	m.signalGroups(sig, procs, j.pid, !reaped || err != nil)
	return toldAll(procs)
}

// signalGroups sends sig to the process group of every process in procs,
// and first, if own is set, to group leader. A SIGTERM is followed by a
// SIGCONT, since a stopped process cannot act on a SIGTERM.
func (m *Manager) signalGroups(sig syscall.Signal, procs []procInfo, leader int, own bool) {
	seen := make(map[int]bool)
	hit := func(pgid int) {
		// Group 0 is our own group and group 1 is everything: never those.
		if pgid <= 1 || pgid == syscall.Getpgrp() || seen[pgid] {
			return
		}
		seen[pgid] = true
		m.signal(-pgid, sig)
		if sig == syscall.SIGTERM {
			m.signal(-pgid, syscall.SIGCONT)
		}
	}
	if own {
		hit(leader)
	}
	for _, p := range procs {
		hit(p.pgid)
	}
}

// outwait waits up to d for session sid to have no live process left and,
// when exited is not nil, for the shell that led it to have been reaped.
// It reports whether that came about. With only set, the session's
// processes are those only keeps of them, and the others are not counted
// or signalled.
//
// While it waits it looks at the session again and again, at first every
// millisecond and then less often, and sends sig to each process it finds
// that told, the record of what sig has gone to already, does not hold as
// the program it now is: such a one was forked too late for the signal its
// group got, or changed groups just then, or has become another program
// since and may have lost the signal in doing so.
func (m *Manager) outwait(sid int, exited <-chan struct{}, d time.Duration, sig syscall.Signal, told map[procKey]string, only func([]procInfo) []procInfo) bool {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	pause := time.Millisecond
	for {
		reaped := exited == nil || closed(exited)
		procs, err := m.procs(sid)
		if only != nil && err == nil {
			procs = only(procs)
		}
		empty := len(procs) == 0
		if err != nil {
			// The session cannot be listed here. The process group is the
			// next best thing to ask about.
			err := m.signal(-sid, 0)
			empty = err != nil && !errors.Is(err, syscall.EPERM)
		}
		for _, p := range procs {
			key := procKey{p.pid, p.start}
			if image, ok := told[key]; (ok && image == p.image) || p.pid <= 1 || p.pid == os.Getpid() {
				continue
			}
			told[key] = p.image
			m.signal(p.pid, sig)
			if sig == syscall.SIGTERM {
				m.signal(p.pid, syscall.SIGCONT)
			}
		}
		if reaped && empty {
			return true
		}
		// The shell's exit is news worth looking again for at once.
		wake := exited
		if reaped {
			wake = nil
		}
		wait := time.NewTimer(pause)
		select {
		case <-deadline.C:
			wait.Stop()
			return false
		case <-wake:
			wait.Stop()
		case <-wait.C:
			pause = min(2*pause, 50*time.Millisecond)
		}
	}
}

// sweep kills what a finished command left running in its session sid:
// then is what was there when the command ended (or when what held its
// output let go of it). By now the session's leader is long gone and its id
// may have been given to something else. When one of the processes seen
// then is still there, in that session, started at the same moment, the
// session is still the command's, and all of it is stopped. Otherwise only
// the processes that carry the manager's mark in their environment are
// surely the command's: a launcher seen then may have ended after
// starting a server, which runs on in the session. Those are stopped, and
// nothing else is touched.
func (m *Manager) sweep(sid int, then []procInfo) {
	if sid <= 1 || len(then) == 0 {
		return
	}
	now, err := m.procs(sid)
	if err != nil || len(now) == 0 {
		return
	}
	var only func([]procInfo) []procInfo
	if !sameProcess(now, then) {
		// A process once seen with the mark stays the command's until it
		// is gone: Linux stops showing the environment of a process on its
		// way out, and one told to stop is not stopped until it has left.
		ours := make(map[procKey]bool)
		only = func(ps []procInfo) []procInfo {
			return slices.DeleteFunc(ps, func(p procInfo) bool {
				key := procKey{p.pid, p.start}
				if !ours[key] && len(m.marked([]procInfo{p})) == 1 {
					ours[key] = true
				}
				return !ours[key]
			})
		}
		if now = only(now); len(now) == 0 {
			return
		}
	}
	m.signalGroups(syscall.SIGTERM, now, sid, false)
	if m.outwait(sid, nil, m.opt.TermGrace, syscall.SIGTERM, toldAll(now), only) {
		return
	}
	// The processes are the same ones as a moment ago, with whatever they
	// have started since.
	if now, err = m.procs(sid); err != nil {
		return
	}
	if only != nil {
		now = only(now)
	}
	m.signalGroups(syscall.SIGKILL, now, sid, false)
	m.outwait(sid, nil, m.opt.KillWait, syscall.SIGKILL, toldAll(now), only)
}

// describeProcs lists processes for a message: "pid 4242 (sleep), pid 4243
// (sleep)".
func describeProcs(procs []procInfo) string {
	parts := make([]string, 0, len(procs))
	for _, p := range procs {
		if p.name != "" {
			parts = append(parts, fmt.Sprintf("pid %d (%s)", p.pid, p.name))
		} else {
			parts = append(parts, fmt.Sprintf("pid %d", p.pid))
		}
	}
	return strings.Join(parts, ", ")
}

// describeGroups lists the process groups of procs for a message, each
// with the programs in it: "process group 4242 (sleep, cat)". It also
// returns the group ids.
func describeGroups(procs []procInfo) (text string, pgids []int) {
	names := make(map[int][]string)
	for _, p := range procs {
		list, ok := names[p.pgid]
		if !ok {
			pgids = append(pgids, p.pgid)
		}
		if p.name != "" && len(list) < 4 {
			list = append(list, p.name)
		}
		names[p.pgid] = list
	}
	parts := make([]string, 0, len(pgids))
	for _, pgid := range pgids {
		if list := names[pgid]; len(list) > 0 {
			parts = append(parts, fmt.Sprintf("process group %d (%s)", pgid, strings.Join(list, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf("process group %d", pgid))
		}
	}
	return strings.Join(parts, ", "), pgids
}
