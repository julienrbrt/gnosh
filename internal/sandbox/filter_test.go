package sandbox

import (
	"strings"
	"testing"
)

// runFilter is the kernel's reading of a classic BPF program, enough of it
// for the filter's instructions: what it answers a call.
func runFilter(t *testing.T, prog []sockFilter, arch, nr, arg0 uint32) uint32 {
	t.Helper()
	var acc uint32
	for pc := 0; pc < len(prog); pc++ {
		in := prog[pc]
		switch in.Code {
		case bpfLdAbs:
			switch in.K {
			case dataNr:
				acc = nr
			case dataArch:
				acc = arch
			case dataArg0:
				acc = arg0
			default:
				t.Fatalf("a load from %d", in.K)
			}
		case bpfJeq:
			if acc == in.K {
				pc += int(in.Jt)
			} else {
				pc += int(in.Jf)
			}
		case bpfJset:
			if acc&in.K != 0 {
				pc += int(in.Jt)
			} else {
				pc += int(in.Jf)
			}
		case bpfRet:
			return in.K
		default:
			t.Fatalf("an instruction %#x", in.Code)
		}
	}
	t.Fatal("the program ran off its end")
	return 0
}

// The filter, call by call, on both machines it is for: socket(AF_UNIX)
// and io_uring_setup refused with EAFNOSUPPORT, another socket and any
// other call let through; the 32-bit calls the same, socketcall whole; an
// x32 call, or one of an architecture it does not know, kills.
func TestUnixFilter(t *testing.T) {
	deny := uint32(seccompErrno | eafnosupport)
	const afInet, read = 2, 0
	for name, a := range filterArches {
		prog := unixFilter(a)
		for _, c := range []struct {
			what           string
			arch, nr, arg0 uint32
			want           uint32
		}{
			{"socket(AF_UNIX)", a.native, a.socket, afUnix, deny},
			{"socket(AF_INET)", a.native, a.socket, afInet, seccompAllow},
			{"io_uring_setup", a.native, a.uring, 0, deny},
			{"read", a.native, read, afUnix, seccompAllow},
			{"32-bit socket(AF_UNIX)", a.compat, a.compatSocket, afUnix, deny},
			{"32-bit socket(AF_INET)", a.compat, a.compatSocket, afInet, seccompAllow},
			{"32-bit io_uring_setup", a.compat, a.compatUring, 0, deny},
			{"32-bit read", a.compat, 3, 0, seccompAllow},
			{"another architecture's", 0x12345678, a.socket, afUnix, seccompKill},
		} {
			if got := runFilter(t, prog, c.arch, c.nr, c.arg0); got != c.want {
				t.Errorf("%s: %s answered %#x, want %#x", name, c.what, got, c.want)
			}
		}
		if a.compatSocketcall != 0 {
			if got := runFilter(t, prog, a.compat, a.compatSocketcall, 1); got != deny {
				t.Errorf("%s: 32-bit socketcall answered %#x", name, got)
			}
		}
		if a.x32 {
			if got := runFilter(t, prog, a.native, a.socket|x32Bit, afInet); got != seccompKill {
				t.Errorf("%s: an x32 call answered %#x", name, got)
			}
		}
	}
}

// Through bubblewrap the command runs behind the filter, after the relay
// (which must make Unix sockets), with gnosh's program in sight and
// read-only; every Unix socket let, there is no filter.
func TestBwrapFilter(t *testing.T) {
	if !canFilter() {
		t.Skip("no filter for this machine")
	}
	fs := fakeFS(map[string]bool{"/w": true, "/t": true, "/t/proxy.sock": false, "/opt/gnosh": false})
	argv := []string{"/bin/sh", "-c", "x"}
	p := Policy{Write: []string{"/w"}, Temp: "/t", Filter: "/opt/gnosh"}
	got := strings.Join(bwrapArgs("bwrap", p, "/w", argv, fs), " ")
	if !strings.HasSuffix(got, "--ro-bind /opt/gnosh /opt/gnosh --chdir /w -- /opt/gnosh "+FilterWord+" /bin/sh -c x") {
		t.Errorf("behind the filter: %s", got)
	}
	p.Proxy, p.Relay = "/t/proxy.sock", "/opt/gnosh"
	got = strings.Join(bwrapArgs("bwrap", p, "/w", argv, fs), " ")
	if !strings.HasSuffix(got, "-- /opt/gnosh "+RelayWord+" /tmp/proxy.sock /opt/gnosh "+FilterWord+" /bin/sh -c x") || strings.Count(got, "--ro-bind /opt/gnosh") != 1 {
		t.Errorf("the relay, then the filter: %s", got)
	}
	p.AllUnixSockets = true
	if got := strings.Join(bwrapArgs("bwrap", p, "/w", argv, fs), " "); strings.Contains(got, FilterWord) {
		t.Errorf("every Unix socket let, still the filter: %s", got)
	}
}
