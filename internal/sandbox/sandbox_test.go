package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFS is a stat over a fixed set of paths: true for a folder.
func fakeFS(paths map[string]bool) func(string) (bool, bool) {
	return func(p string) (bool, bool) {
		isDir, ok := paths[p]
		return isDir, ok
	}
}

// The bubblewrap command line: the machine read-only, the sandbox's own
// /dev, /proc and /tmp, the folders it writes in, the read-only paths in
// them, the denied ones hidden, those opened again, and a denied path
// inside one opened again hidden again; no network unless given; only
// what is there, each folder once.
func TestBwrapArgs(t *testing.T) {
	fs := fakeFS(map[string]bool{"/w": true, "/w/sub": true, "/w/.gnosh": true, "/w/gnosh.json": false, "/t": true,
		"/home/u": true, "/home/u/proj": true, "/home/u/proj/.env": false, "/x/cwd": false, "/x/env.sh": false})
	p := Policy{
		Write:     []string{"/w", "/w/sub", "/w/", "/missing"},
		ReadOnly:  []string{"/w/.gnosh", "/w/gnosh.json", "/w/none"},
		DenyRead:  []string{"/home/u", "/home/u/proj/.env"},
		AllowRead: []string{"/home/u/proj", "/elsewhere"},
		Temp:      "/t",
		Expose:    []Exposed{{Path: "/x/cwd", Write: true}, {Path: "/x/env.sh"}, {Path: "/x/gone"}},
	}
	got := strings.Join(bwrapArgs("bwrap", p, "/w/sub/", []string{"/bin/bash", "-c", "x"}, fs), " ")
	want := "bwrap --die-with-parent --unshare-pid --unshare-ipc --unshare-net --ro-bind / / --dev /dev --proc /proc" +
		" --bind /t /tmp --bind /w /w" +
		" --ro-bind /w/.gnosh /w/.gnosh --ro-bind /w/gnosh.json /w/gnosh.json --bind /x/cwd /x/cwd --ro-bind /x/env.sh /x/env.sh" +
		" --tmpfs /home/u --ro-bind /home/u/proj /home/u/proj" +
		" --ro-bind /dev/null /home/u/proj/.env --remount-ro /home/u --chdir /w/sub -- /bin/bash -c x"
	if got != want {
		t.Errorf("bwrap args:\n got %s\nwant %s", got, want)
	}
	p.Network = true
	if got := strings.Join(bwrapArgs("bwrap", Policy{Network: true}, "/", []string{"true"}, fs), " "); strings.Contains(got, "--unshare-net") {
		t.Errorf("with the network: %s", got)
	}
}

// The Seatbelt profile: writes only in the folders named, the temporary
// one and the devices a shell needs; the read-only and denied paths after
// them, a denied path inside one opened again denied again; strings
// quoted.
func TestSeatbeltProfile(t *testing.T) {
	p := Policy{
		Write:     []string{"/Users/u/proj", `/Users/u/we"ird\dir`},
		ReadOnly:  []string{"/Users/u/proj/.gnosh"},
		DenyRead:  []string{"/Users/u", "/Users/u/proj/.env"},
		AllowRead: []string{"/Users/u/proj"},
		Temp:      "/private/tmp/gnosh-sandbox-501",
		Expose:    []Exposed{{Path: "/private/var/x/cwd", Write: true}, {Path: "/private/var/x/env.sh"}},
	}
	got := seatbeltProfile(p)
	for _, piece := range []string{
		"(version 1)\n(allow default)\n", "(deny network*)\n(deny file-read* file-write* (literal \"/dev/tty\") (regex #\"^/dev/ttys[0-9]+$\"))\n(deny file-mount)\n(deny user-preference-write)\n(deny file-write*)\n(allow file-write*\n",
		`(subpath "/Users/u/proj")`, `(subpath "/Users/u/we\"ird\\dir")`, `(subpath "/private/tmp/gnosh-sandbox-501")`,
		`(literal "/private/var/x/cwd")`, `(literal "/dev/null")`, `(regex #"^/dev/fd/[0-9]+$")`,
		"(deny appleevent-send)\n(deny job-creation)\n", `(global-name "com.apple.coreservices.launchservicesd")`,
		`(deny file-read* file-write* (literal "/dev/tty") (regex #"^/dev/ttys[0-9]+$"))`,
		"(deny file-write*\n  (subpath \"/Users/u/proj/.gnosh\"))",
		"(deny file-read*\n  (subpath \"/Users/u\")\n  (subpath \"/Users/u/proj/.env\"))\n(allow file-read*\n  (subpath \"/Users/u/proj\")\n",
		"(deny file-read*\n  (subpath \"/Users/u/proj/.env\"))\n",
	} {
		if !strings.Contains(got, piece) {
			t.Errorf("the profile has no %q:\n%s", piece, got)
		}
	}
	if strings.Contains(got, `(literal "/private/var/x/env.sh")`) {
		t.Errorf("a file only read was made writable:\n%s", got)
	}
	if strings.Index(got, `(literal "/private/var/x/cwd")`) < strings.Index(got, `(subpath "/Users/u/proj/.gnosh")`) {
		t.Errorf("gnosh's own file is made writable before the read-only paths, which would take it back:\n%s", got)
	}
	if strings.Contains(seatbeltProfile(Policy{Network: true}), "network") {
		t.Error("the network was denied though the policy gives it")
	}
}

// What the settings loosen, on bubblewrap's command line: user namespaces
// refused (where bubblewrap can, Wrap says) unless let; the machine's
// /proc for an unprivileged container; /run left in sight for every Unix
// socket; and with the files left open, the machine mounted writable,
// nothing of the files confined, the relay given the proxy's socket where
// it is.
func TestBwrapLoosenings(t *testing.T) {
	fs := fakeFS(map[string]bool{"/w": true, "/t": true, "/run": true, "/home/u": true, "/x/cwd": false, "/t/proxy.sock": false})
	argv := []string{"/bin/sh", "-c", "x"}
	args := func(p Policy) string { return strings.Join(bwrapArgs("bwrap", p, "/w", argv, fs), " ") }
	if got := args(Policy{blockUserns: true}); !strings.HasPrefix(got, "bwrap --die-with-parent --unshare-user --disable-userns --unshare-pid ") {
		t.Errorf("user namespaces refused: %s", got)
	}
	if got := args(Policy{}); strings.Contains(got, "userns") || !strings.Contains(got, "--proc /proc") || !strings.Contains(got, "--tmpfs /run") {
		t.Errorf("the default: %s", got)
	}
	if got := args(Policy{HostProc: true}); strings.Contains(got, "--proc") {
		t.Errorf("the machine's /proc: %s", got)
	}
	if got := args(Policy{AllUnixSockets: true}); strings.Contains(got, "/run") {
		t.Errorf("every Unix socket: %s", got)
	}
	open := Policy{FilesystemOff: true, Write: []string{"/w"}, DenyRead: []string{"/home/u"}, Temp: "/t",
		Expose: []Exposed{{Path: "/x/cwd", Write: true}}, Proxy: "/t/proxy.sock", Relay: "/gnosh"}
	want := "bwrap --die-with-parent --unshare-pid --unshare-ipc --unshare-net --bind / / --dev /dev --proc /proc --tmpfs /run" +
		" --ro-bind /t/proxy.sock /t/proxy.sock --chdir /w -- /gnosh " + RelayWord + " /t/proxy.sock /bin/sh -c x"
	if got := args(open); got != want {
		t.Errorf("the files left open:\n got %s\nwant %s", got, want)
	}
	if env := strings.Join(Env(open), " "); strings.Contains(env, "TMPDIR") || strings.Contains(env, "XDG_CACHE_HOME") {
		t.Errorf("the files left open, the environment: %s", env)
	}
}

// What the settings loosen, in the Seatbelt profile: the trust service
// closed unless let; Apple Events; Unix sockets by path or all; a port of
// its own and the machine's; more of macOS's services by name, prefix or
// all; and with the files left open, no rule of the files but the
// terminals'.
func TestSeatbeltLoosenings(t *testing.T) {
	base := seatbeltProfile(Policy{})
	for _, piece := range []string{`(global-name "com.apple.trustd.agent")`, "(deny appleevent-send)", "(deny network*)"} {
		if !strings.Contains(base, piece) {
			t.Errorf("the default profile has no %q:\n%s", piece, base)
		}
	}
	if strings.Contains(base, "unix-socket") || strings.Contains(base, "network-bind") || strings.Contains(base, "(allow mach-lookup") {
		t.Errorf("the default profile loosens:\n%s", base)
	}
	got := seatbeltProfile(Policy{TrustService: true, AppleEvents: true, UnixSockets: []string{"/Users/u/.ssh/agent.sock"}, LocalBinding: true,
		MachLookup: []string{"com.apple.CoreSimulator.*", "com.example.one"}})
	for _, piece := range []string{
		"(allow network-outbound\n  (remote unix-socket (path-literal \"/Users/u/.ssh/agent.sock\")))",
		`(allow network-bind (local ip "*:*"))`, `(allow network-inbound (local ip "*:*"))`, `(allow network-outbound (remote ip "localhost:*"))`,
		`(allow mach-lookup (global-name-prefix "com.apple.CoreSimulator.") (global-name "com.example.one"))`,
	} {
		if !strings.Contains(got, piece) {
			t.Errorf("the loosened profile has no %q:\n%s", piece, got)
		}
	}
	if strings.Contains(got, "trustd") || strings.Contains(got, "appleevent") {
		t.Errorf("the trust service or Apple Events still closed:\n%s", got)
	}
	if got := seatbeltProfile(Policy{AllUnixSockets: true, MachLookup: []string{"x.*", "*"}}); !strings.Contains(got, "(allow network-outbound (remote unix-socket))") || !strings.Contains(got, "(allow mach-lookup)\n") {
		t.Errorf("every socket and service:\n%s", got)
	}
	open := seatbeltProfile(Policy{FilesystemOff: true, Write: []string{"/Users/u/proj"}, DenyRead: []string{"/Users/u"}})
	if strings.Contains(open, "file-write*)") || strings.Contains(open, "/Users/u") || !strings.Contains(open, `(literal "/dev/tty")`) || !strings.Contains(open, "(deny network*)") {
		t.Errorf("the files left open:\n%s", open)
	}
}

// A credential the settings hide is hidden last: an allowRead of it, a
// folder written in that holds it, or the folder itself among those
// written in, brings nothing back (the sandbox review). The variables
// the settings hide go first, so that hiding one of gnosh's own cannot
// take the sandbox's.
func TestHiddenLast(t *testing.T) {
	fs := fakeFS(map[string]bool{"/home/u": true, "/home/u/.ssh": true, "/home/u/.ssh/id_ed25519": false, "/w": true, "/w/.env": false, "/t": true})
	p := Policy{Write: []string{"/w", "/home/u/.ssh"}, AllowRead: []string{"/home/u/.ssh/id_ed25519"}, DenyRead: []string{"/home/u"},
		Hidden: []string{"/home/u/.ssh", "/w/.env"}, Temp: "/t"}
	got := strings.Join(bwrapArgs("bwrap", p, "/w", []string{"true"}, fs), " ")
	last := strings.LastIndex(got, "--bind /home/u/.ssh /home/u/.ssh")
	hide := strings.LastIndex(got, "--tmpfs /home/u/.ssh")
	envHide := strings.LastIndex(got, "--ro-bind /dev/null /w/.env")
	if last < 0 || hide < last || envHide < strings.LastIndex(got, "--bind /w /w") || !strings.Contains(got, "--remount-ro /home/u/.ssh") {
		t.Errorf("the hidden paths are not hidden after what opens them:\n%s", got)
	}
	prof := seatbeltProfile(p)
	if i := strings.LastIndex(prof, "(deny file-read* file-write*\n  (subpath \"/home/u/.ssh\")\n  (subpath \"/w/.env\"))"); i < 0 || i < strings.LastIndex(prof, "(allow file-read*") {
		t.Errorf("the hidden paths are not denied last:\n%s", prof)
	}
	p.HideVars = []string{"HTTPS_PROXY", "GITHUB_TOKEN"}
	p.Proxy = "/t/proxy.sock"
	env := strings.Join(Env(p), " ")
	if !strings.HasPrefix(env, "HTTPS_PROXY GITHUB_TOKEN ") || !strings.Contains(env, "HTTPS_PROXY=http://") {
		t.Errorf("the variables: %s", env)
	}
}

// Seatbelt matches paths, so a folder renamed takes what is in it out
// from under the rules: nothing may be moved out of a path the command may
// not read (but out of what is opened again in one), and no folder that
// holds a path kept from it may be renamed or taken away, up to the root
// (the sandbox review).
func TestSeatbeltKeepsFoldersInPlace(t *testing.T) {
	p := Policy{
		Write:     []string{"/Users/u/proj"},
		ReadOnly:  []string{"/Users/u/proj/.gnosh"},
		DenyRead:  []string{"/Users/u", "/Users/u/proj/conf/secret.yml"},
		AllowRead: []string{"/Users/u/proj"},
	}
	got := seatbeltProfile(p)
	for _, piece := range []string{
		"(deny file-write-unlink\n  (subpath \"/Users/u\")\n  (subpath \"/Users/u/proj/conf/secret.yml\"))",
		"(allow file-write-unlink\n  (subpath \"/Users/u/proj\")",
		"(deny file-write-unlink\n  (subpath \"/Users/u/proj/conf/secret.yml\"))",
		"(deny file-write-unlink\n  (literal \"/Users\")\n  (literal \"/Users/u\")\n  (literal \"/Users/u/proj\")\n  (literal \"/Users/u/proj/conf\"))",
	} {
		if !strings.Contains(got, piece) {
			t.Errorf("the profile has no %q:\n%s", piece, got)
		}
	}
	if strings.Index(got, "(allow file-write-unlink") > strings.Index(got, "(deny file-write*\n  (subpath \"/Users/u/proj/.gnosh\"))") {
		t.Errorf("the unlinks opened again come after the read-only paths, and open them:\n%s", got)
	}
	// No folder cloned whole, which would take a denied file in it along;
	// and what is opened again only to be read is not moved or deleted
	// either (the third review).
	p.AllowRead = append(p.AllowRead, "/Users/u/notes")
	got = seatbeltProfile(p)
	if !strings.Contains(got, "(deny file-clone (vnode-type DIRECTORY))") {
		t.Errorf("folders may be cloned:\n%s", got)
	}
	if !strings.Contains(got, `(global-name-prefix "com.apple.metadata.mds")`) {
		t.Errorf("Spotlight may be asked what the denied files hold:\n%s", got)
	}
	if i := strings.Index(got, "(allow file-write-unlink"); i < 0 || strings.Contains(got[i:i+strings.Index(got[i:], ")\n")], "/Users/u/notes") {
		t.Errorf("a path opened again to be read may be moved or deleted:\n%s", got)
	}
	if got := seatbeltProfile(Policy{Write: []string{"/w"}}); strings.Contains(got, "file-clone") || strings.Contains(got, "metadata") {
		t.Errorf("folders refused cloning, or Spotlight its questions, with nothing denied or hidden:\n%s", got)
	}
}

// A path not there yet is spelled as it will be when it is: the nearest
// folder above it that is there, its links resolved, then the rest; a
// path that is there, resolved whole (the third sandbox review: only its
// own folder was looked at, so two folders down under a link, as /tmp is
// on macOS, it matched neither spelling).
func TestRealPathOfWhatIsNotThereYet(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		filepath.Join(base, "link"):                         filepath.Join(base, "real"),
		filepath.Join(base, "link", "a.txt"):                filepath.Join(base, "real", "a.txt"),
		filepath.Join(base, "link", "x", "y", "secret.yml"): filepath.Join(base, "real", "x", "y", "secret.yml"),
		filepath.Join(base, "link", "..", "link", "z"):      filepath.Join(base, "real", "z"),
		"/no-such-root-folder/a/b":                          "/no-such-root-folder/a/b",
	} {
		if got := realPath(in); got != want {
			t.Errorf("realPath(%s) = %s, want %s", in, got, want)
		}
	}
}

// What lies on the way to a path kept from the command, where it may
// write: a link, by its own name (a ~/.zshrc a dotfile manager keeps by
// one, a .claude that is one), and the folders not there yet (a .gnosh,
// its agents folder); not a link outside the folders it writes in, nor
// the path's own place, which its rule covers; a loop of links ends.
func TestTheWayToAKeptPath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "root")
	for _, d := range []string{root, filepath.Join(base, "dot"), filepath.Join(base, "elsewhere")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(root, ".zshrc"):  filepath.Join(base, "dot", "zshrc"),
		filepath.Join(root, ".claude"): "../elsewhere",
		filepath.Join(base, "outside"): root,
		filepath.Join(root, "loop1"):   "loop2",
		filepath.Join(root, "loop2"):   "loop1",
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	in := func(s string) bool { return under(s, root) }
	for _, c := range []struct {
		path          string
		links, absent []string
	}{
		{filepath.Join(root, ".zshrc"), []string{filepath.Join(root, ".zshrc")}, nil},
		{filepath.Join(root, ".claude", "settings.json"), []string{filepath.Join(root, ".claude")}, nil},
		{filepath.Join(root, ".gnosh", "agents", "x.md"), nil, []string{filepath.Join(root, ".gnosh", "agents"), filepath.Join(root, ".gnosh")}},
		{filepath.Join(base, "outside", ".envrc"), nil, nil},
		// (A folder not there yet outside the folders it writes in, it
		// cannot make.)
		{filepath.Join(base, "outside", ".claude", "skills", "s"), []string{filepath.Join(root, ".claude")}, nil},
		{filepath.Join(root, "loop1", "x"), []string{filepath.Join(root, "loop1"), filepath.Join(root, "loop2")}, nil},
	} {
		links, absent := way(c.path, in)
		if len(links) > 2 {
			links = links[:2] // (the loop's, met again and again)
		}
		if strings.Join(links, " ") != strings.Join(c.links, " ") || strings.Join(absent, " ") != strings.Join(c.absent, " ") {
			t.Errorf("way(%s) = links %q, folders not there %q; want %q, %q", c.path, links, absent, c.links, c.absent)
		}
	}
}

// The policy's kept paths, read-only, denied and hidden, each have their
// way looked at, in the folders the command writes in, its temporary
// folder among them; each link and folder once, in order.
func TestOnTheWayOfAPolicy(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, temp := filepath.Join(base, "root"), filepath.Join(base, "temp")
	for _, d := range []string{root, temp, filepath.Join(base, "elsewhere")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{filepath.Join(root, ".zshrc"): "../elsewhere/zshrc", filepath.Join(root, ".claude"): "../elsewhere"} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Write: []string{root}, Temp: temp, ReadOnly: []string{filepath.Join(root, ".zshrc"), filepath.Join(root, ".claude", "settings.json")},
		DenyRead: []string{filepath.Join(root, ".claude", "x")}, Hidden: []string{filepath.Join(root, ".gnosh", "y", "z"), filepath.Join(temp, "a", "b")}}
	links, absent := onTheWay(p)
	if want := []string{filepath.Join(root, ".claude"), filepath.Join(root, ".zshrc")}; strings.Join(links, " ") != strings.Join(want, " ") {
		t.Errorf("links %q, want %q", links, want)
	}
	if want := []string{filepath.Join(root, ".gnosh"), filepath.Join(root, ".gnosh", "y"), filepath.Join(temp, "a")}; strings.Join(absent, " ") != strings.Join(want, " ") {
		t.Errorf("folders not there %q, want %q", absent, want)
	}
}

// The folders not there yet on the way to a kept path take no link, made
// or renamed in, though a folder may be made there; nothing so without.
func TestSeatbeltRefusesLinksOnTheWay(t *testing.T) {
	got := seatbeltProfile(Policy{Write: []string{"/w"}, ReadOnly: []string{"/w/.gnosh/settings.json"}, noLinks: []string{"/w/.gnosh"}})
	if !strings.Contains(got, "(deny file-write-create\n  (require-all (literal \"/w/.gnosh\") (vnode-type SYMLINK)))") {
		t.Errorf("no rule against a link at the folder not there yet:\n%s", got)
	}
	if got := seatbeltProfile(Policy{Write: []string{"/w"}}); strings.Contains(got, "SYMLINK") {
		t.Errorf("links refused with nothing kept:\n%s", got)
	}
}

// No volume may be mounted over a folder, nor an application's
// preferences written, while the files are confined (both are done by a
// service outside the sandbox); with them left open, the command may do
// as the person may.
func TestSeatbeltRefusesMountsAndPreferences(t *testing.T) {
	got := seatbeltProfile(Policy{Write: []string{"/w"}})
	for _, want := range []string{"(deny file-mount)\n", "(deny user-preference-write)\n", "(deny signal)\n(allow signal (target same-sandbox))\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the profile lacks %q:\n%s", want, got)
		}
	}
	if got := seatbeltProfile(Policy{FilesystemOff: true}); strings.Contains(got, "file-mount") || strings.Contains(got, "user-preference") {
		t.Errorf("mounts or preferences refused with the files left open:\n%s", got)
	}
}
