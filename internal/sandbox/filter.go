package sandbox

// This file is the filter that refuses a sandboxed command new Unix
// sockets on Linux, as Claude Code's sandbox does (its seccomp filter;
// sandbox.network.allowAllUnixSockets lifts it): a read-only mount does
// not stop a connection to a socket, and one in sight (an editor's
// server, an agent in the home folder) could run what it is told outside
// the sandbox (spec/DECISIONS.md 367). gnosh puts it on itself, run inside
// the sandbox as FilterWord, and then becomes the command, which keeps
// it; the relay, which must make Unix sockets, runs before it.

// FilterWord is the first word that makes gnosh, started inside the Linux
// sandbox, put the filter on itself and become the command (FilterMain).
const FilterWord = "sandbox-filter"

// sockFilter is one instruction of a classic BPF program, as the kernel
// takes it (linux/filter.h).
type sockFilter struct {
	Code   uint16
	Jt, Jf uint8
	K      uint32
}

// The instructions and answers the filter is made of.
const (
	bpfLdAbs = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJeq   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfJset  = 0x45 // BPF_JMP | BPF_JSET | BPF_K
	bpfRet   = 0x06 // BPF_RET | BPF_K

	seccompAllow = 0x7fff0000
	seccompErrno = 0x00050000
	seccompKill  = 0x80000000 // SECCOMP_RET_KILL_PROCESS

	// Where seccomp_data holds the call's number, its architecture and
	// its first argument's low half (on the little-endian machines the
	// filter is for).
	dataNr, dataArch, dataArg0 = 0, 4, 16

	afUnix = 1
	// eafnosupport is the error a refused socket gets: "Address family
	// not supported by protocol", which no other refusal of the
	// sandbox's says, so the note under a failed command can tell it.
	eafnosupport = 97
	x32Bit       = 0x40000000
)

// filterArch is what the filter knows of a machine: its calls' numbers,
// and those of the 32-bit calls its kernel takes too.
type filterArch struct {
	native, compat uint32 // AUDIT_ARCH_*
	socket, uring  uint32 // socket(2), io_uring_setup(2)
	x32            bool   // the x32 calls are there, and refused whole
	// compatSocketcall is the 32-bit socketcall(2), refused whole (its
	// arguments are out of a filter's sight); 0 where there is none.
	compatSocket, compatSocketcall, compatUring uint32
}

// filterArches are the machines the filter is made for, by GOARCH.
var filterArches = map[string]filterArch{
	"amd64": {native: 0xc000003e, socket: 41, uring: 425, x32: true, compat: 0x40000003, compatSocket: 359, compatSocketcall: 102, compatUring: 425},
	"arm64": {native: 0xc00000b7, socket: 198, uring: 425, compat: 0x40000028, compatSocket: 281, compatUring: 425},
}

// unixFilter is the program for a machine: socket(AF_UNIX, ...) refused
// with EAFNOSUPPORT, and io_uring_setup, whose rings make sockets where
// the filter does not look; the same of the 32-bit calls, socketcall
// whole; an x32 call, or one of an architecture the filter does not
// know, kills the process. socketpair(2) stays, as programs need it: a
// stream pair reaches nothing outside, but a datagram one can still be
// pointed (connect, sendto) at a datagram socket in sight by its path,
// and few are, the sandbox's /run, /dev and /tmp being its own (the second
// sandbox review).
func unixFilter(a filterArch) []sockFilter {
	deny := uint32(seccompErrno | eafnosupport)
	prog := []insn{
		{code: bpfLdAbs, k: dataArch},
		{code: bpfJeq, k: a.native, jf: "compat"},
		{code: bpfLdAbs, k: dataNr},
	}
	if a.x32 {
		prog = append(prog, insn{code: bpfJset, k: x32Bit, jt: "kill"})
	}
	prog = append(prog,
		insn{code: bpfJeq, k: a.socket, jt: "socket"},
		insn{code: bpfJeq, k: a.uring, jt: "deny"},
		insn{code: bpfRet, k: seccompAllow},
		insn{label: "compat", code: bpfJeq, k: a.compat, jf: "kill"},
		insn{code: bpfLdAbs, k: dataNr},
	)
	if a.compatSocketcall != 0 {
		prog = append(prog, insn{code: bpfJeq, k: a.compatSocketcall, jt: "deny"})
	}
	prog = append(prog,
		insn{code: bpfJeq, k: a.compatSocket, jt: "socket"},
		insn{code: bpfJeq, k: a.compatUring, jt: "deny"},
		insn{code: bpfRet, k: seccompAllow},
		insn{label: "kill", code: bpfRet, k: seccompKill},
		insn{label: "socket", code: bpfLdAbs, k: dataArg0},
		insn{code: bpfJeq, k: afUnix, jt: "deny"},
		insn{code: bpfRet, k: seccompAllow},
		insn{label: "deny", code: bpfRet, k: deny},
	)
	return assemble(prog)
}

// insn is an instruction with its jumps by label ("" for the next one).
type insn struct {
	label  string
	code   uint16
	k      uint32
	jt, jf string
}

// assemble turns the labels into the forward counts classic BPF jumps by.
// A jump nowhere, or backwards, is a mistake in this file: it panics.
func assemble(prog []insn) []sockFilter {
	at := map[string]int{}
	for i, in := range prog {
		if in.label != "" {
			at[in.label] = i
		}
	}
	out := make([]sockFilter, len(prog))
	for i, in := range prog {
		rel := func(label string) uint8 {
			if label == "" {
				return 0
			}
			j, ok := at[label]
			if !ok || j <= i || j-i-1 > 255 {
				panic("sandbox: the filter jumps to " + label + " where it cannot")
			}
			return uint8(j - i - 1)
		}
		out[i] = sockFilter{Code: in.code, K: in.k, Jt: rel(in.jt), Jf: rel(in.jf)}
	}
	return out
}
