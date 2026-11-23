// Package sandbox runs a shell command inside the operating system's own
// sandbox, as Claude Code's Bash sandbox does: on Linux under bubblewrap
// (bwrap), on macOS under Seatbelt (sandbox-exec). A sandboxed command
// writes only in the folders it is given and in the sandbox's own
// temporary folder, reads everything but the paths it is denied, sees
// only its own processes, and reaches no network (spec/DECISIONS.md 352).
//
// The package builds the command line and checks that the sandbox can
// run; which commands run in it, and what they may then do without
// asking, is the caller's.
package sandbox

import (
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Policy is what a sandboxed command may do.
type Policy struct {
	// Write are the folders, or files, it may write in: the session's
	// working directories and those the settings add. A path that is not
	// there is left out: bubblewrap binds only what exists.
	Write []string
	// ReadOnly are paths inside those that stay read-only: the settings
	// files gnosh guards, and the settings' denyWrite.
	ReadOnly []string
	// DenyRead are paths it may not read, and AllowRead paths inside them
	// it may read after all.
	DenyRead, AllowRead []string
	// Hidden are paths it may never read nor change, whatever else opens
	// them: the credentials the settings hide (no allowRead, folder it
	// writes in or file of gnosh's own brings one back: the sandbox
	// review).
	Hidden []string
	// Temp is the sandbox's own temporary folder, the session's alone,
	// kept from one command to the next: /tmp inside the sandbox on Linux
	// (the real /tmp out of sight), and a folder it may write in on macOS,
	// where TMPDIR names it.
	Temp string
	// Expose are files of gnosh's own the command's shell must reach: the
	// one it writes its last working directory to (Write), and the scripts
	// it reads.
	Expose []Exposed
	// Network lets it reach the network. Without it there is none, but
	// for the loopback of its own (Linux) where nothing listens.
	Network bool
	// HideVars are variables the command is not given (credentials it
	// may not hold).
	HideVars []string
	// Proxy is the sandbox's way out to the hosts the settings allow
	// (proxy.go), when there is one: on Linux the proxy's socket, which
	// must be in Temp, reached through Relay (gnosh itself, run inside the
	// sandbox: relay.go); on macOS the proxy's address on this machine
	// ("localhost:port"), the one Seatbelt lets the command reach.
	Proxy, Relay string

	// FilesystemOff leaves the files as they are to the command: it reads
	// and writes what the person may, and only the network (and the
	// rest below) is confined (filesystem.disabled). Write, ReadOnly,
	// DenyRead, AllowRead and Expose then do nothing, and Temp only holds
	// the proxy's socket.
	FilesystemOff bool

	// What the settings loosen, each one of Claude Code's sandbox keys
	// (spec/DECISIONS.md 364):
	//
	// HostProc shows the command the machine's /proc in place of one of
	// its own, where bubblewrap cannot mount one, as in an unprivileged
	// container (enableWeakerNestedSandbox): it then sees the person's
	// other processes, gnosh among them, and what they let it read.
	HostProc bool
	// UserNamespaces lets it make user namespaces of its own, which the
	// sandbox refuses where its bubblewrap can (allowNestedUserNamespaces;
	// Chromium's sandbox, a nested bubblewrap, unshare).
	UserNamespaces bool
	// AppleEvents lets it send Apple Events on macOS, so that open and
	// osascript work: it can then start another application outside the
	// sandbox (allowAppleEvents).
	AppleEvents bool
	// TrustService lets it reach macOS's certificate trust service, which
	// Go's programs check certificates with, and which fetches from the
	// network for whoever asks (enableWeakerNetworkIsolation).
	TrustService bool
	// UnixSockets are the Unix sockets it may connect to on macOS
	// (network.allowUnixSockets; Linux has no way to tell one socket
	// from another, as in Claude Code); AllUnixSockets lets it connect
	// to any, the machine's /run left in sight on Linux
	// (network.allowAllUnixSockets).
	UnixSockets    []string
	AllUnixSockets bool
	// LocalBinding lets it listen on a port and reach any port of this
	// machine's own on macOS (network.allowLocalBinding); on Linux it has
	// a loopback of its own already.
	LocalBinding bool
	// MachLookup are more of macOS's services it may reach, past those the
	// sandbox refuses: a name, a name ending in * for those it begins, or
	// "*" for every one (network.allowMachLookup).
	MachLookup []string
	// Bwrap is bubblewrap's path, when the settings name one (bwrapPath).
	Bwrap string
	// Filter is gnosh's own program, run inside on Linux to refuse the
	// command new Unix sockets before it becomes the command (filter.go),
	// unless AllUnixSockets; "" for no filter.
	Filter string

	// blockUserns refuses it user namespaces: set by Wrap, where the
	// machine's bubblewrap can and the policy does not let it make them.
	blockUserns bool
	// noLinks are the folders not there yet on the way to a path kept
	// from the command, where it may make no link (onTheWay): set by Wrap
	// on macOS.
	noLinks []string
}

// Setup is what of a policy decides whether the sandbox can run here:
// which bubblewrap, whether it mounts a /proc of its own, and the program
// that puts on the filter for Unix sockets, which is tried too.
type Setup struct {
	Bwrap    string
	HostProc bool
	Filter   string
}

// SetupOf is the policy's setup.
func SetupOf(p Policy) Setup { return Setup{Bwrap: p.Bwrap, HostProc: p.HostProc, Filter: p.Filter} }

// Exposed is a file of gnosh's own a sandboxed command reaches.
type Exposed struct {
	Path  string
	Write bool
}

// ErrUnsupported is Wrap's error where this system has no sandbox gnosh
// can use.
var ErrUnsupported = errors.New("gnosh has no sandbox for this system: it sandboxes commands on Linux (bubblewrap) and macOS (Seatbelt)")

// Env is what the command is given beside its own environment: where its
// temporary files go, and its caches, which the person's own folders
// (read-only to it) cannot hold.
func Env(p Policy) []string {
	// (A name alone takes the variable away: those the settings hide
	// first, so that hiding one of gnosh's own, HTTPS_PROXY say, cannot
	// take the sandbox's: the sandbox review.)
	env := slices.Clone(p.HideVars)
	if !p.FilesystemOff {
		tmp := sandboxTmp(p)
		env = append(env, "TMPDIR="+tmp, "XDG_CACHE_HOME="+filepath.Join(tmp, ".cache"))
	}
	if u := proxyURL(p); u != "" {
		// Every request through the proxy, the sandbox's own loopback's
		// too (there is nothing on it but the relay).
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env = append(env, name+"="+u)
		}
		env = append(env, "NO_PROXY=", "no_proxy=")
	}
	return env
}

// present is the paths of a list that are there, absolute, cleaned, each
// once, the shortest first.
func present(paths []string, there func(string) bool) []string {
	var out []string
	for _, p := range paths {
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		if there(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b string) int { return len(a) - len(b) })
	return out
}

// kept is present's paths without one inside another of them: bwrap
// mounts a folder with what is under it.
func kept(paths []string, there func(string) bool) []string {
	var flat []string
	for _, p := range present(paths, there) {
		if !slices.ContainsFunc(flat, func(q string) bool { return under(p, q) }) {
			flat = append(flat, p)
		}
	}
	return flat
}

// under reports whether p is q or a path inside it.
func under(p, q string) bool {
	return p == q || q == "/" || strings.HasPrefix(p, q+"/")
}

// sandboxTmp is the temporary folder as the command sees it.
func sandboxTmp(p Policy) string {
	if runtime.GOOS == "linux" {
		return "/tmp"
	}
	return p.Temp
}
