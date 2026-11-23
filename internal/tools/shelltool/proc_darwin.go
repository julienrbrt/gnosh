package shelltool

import (
	"bytes"
	"syscall"

	"golang.org/x/sys/unix"
)

// zombieState is what the kernel's process table calls a process that has
// ended and waits to be reaped (SZOMB).
const zombieState = 5

// sessionProcs lists the processes of session sid that are still alive.
// Zombies are left out: they are already dead and only wait to be reaped.
// macOS has no /proc, so it asks the kernel for the whole process table and
// then for each process's session.
func sessionProcs(sid int) ([]procInfo, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var out []procInfo
	for i := range all {
		p := &all[i]
		pid := int(p.Proc.P_pid)
		if pid <= 0 || p.Proc.P_stat == zombieState {
			continue
		}
		if s, err := syscall.Getsid(pid); err != nil || s != sid {
			continue
		}
		name := p.Proc.P_comm[:]
		if i := bytes.IndexByte(name, 0); i >= 0 {
			name = name[:i]
		}
		start := uint64(p.Proc.P_starttime.Sec)*1_000_000 + uint64(p.Proc.P_starttime.Usec)
		// The name is all there is here to tell one program from the next.
		out = append(out, procInfo{pid: pid, pgid: int(p.Eproc.Pgid), start: start, name: string(name), image: string(name)})
	}
	return out, nil
}

// procEnvHas cannot tell here: macOS keeps a process's environment from
// other processes (what kern.procargs2 gives, and so ps -E, shows none,
// even of one's own child), so no process is known by the mark, and only
// a process seen as its command ended proves a session is the command's.
func procEnvHas(pid int, entry string) (bool, error) {
	return false, errNoEnviron
}
