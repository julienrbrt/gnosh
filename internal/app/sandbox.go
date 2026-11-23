package app

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/permission"
	"github.com/gnosh-ai/gnosh/internal/sandbox"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
)

// This file joins the sandbox settings (config.Sandbox) to the shell tool
// and the permission engine (spec/DECISIONS.md 353): a command the shell
// runs on this machine runs in the operating system's sandbox, unless
// excludedCommands names it or the model asked out of it, and with
// autoAllowBashIfSandboxed such a command needs no approval.

// sandboxState is the sandbox as the session has it, worked out once:
// on when the settings turn it on, it can run here, and the session's own
// temporary folder could be made. The permission engine and the shell
// tool both take it from here, so that neither lets a command go as
// sandboxed that the other runs outside (the sandbox review: a temporary
// folder that could not be made had left the engine letting commands run
// without asking, and the shell running them outside).
type sandboxState struct {
	once sync.Once
	on   bool
	// why says why not, when the settings turn it on.
	why string
	// temp is the session's own temporary folder (made with a name of its
	// own, the person's alone, removed when the session ends): one per
	// session, so that one repository's commands cannot leave in the
	// caches of another's.
	temp string
	// weaker says what the sandbox here cannot do that it does elsewhere,
	// for the person, when there is such a thing.
	weaker string
	// found is the paths each pattern of the sandbox's lists matched,
	// found once for the session (sandboxFound).
	found expansions
}

// expansions are the paths the sandbox's patterns matched (a denyRead
// entry or a credentials file such as ~/**/.env, a Read deny rule such as
// Read(./**/*.pem)), each found the first time it is needed and kept for
// the session: a walk of the home folder for each command would be too
// slow. A file that comes to match later is not among them, which
// /sandbox says (the sandbox review: such patterns had hidden nothing).
type expansions struct {
	mu     sync.Mutex
	budget int
	got    map[string][]string
	// short are the patterns whose walk ran out of its budget.
	short []string
}

// sandboxFound is the list with each pattern in it replaced by the paths
// it matched, found once for the session; a path that is no pattern
// stays itself.
func (st *sandboxState) sandboxFound(list []string) []string {
	var out []string
	for _, p := range list {
		if !sandbox.IsPattern(p) {
			out = append(out, p)
			continue
		}
		e := &st.found
		e.mu.Lock()
		got, ok := e.got[p]
		if !ok {
			if e.got == nil {
				e.got, e.budget = map[string][]string{}, sandbox.ExpandBudget
			}
			var done bool
			got, done = sandbox.Expand(p, &e.budget)
			e.got[p] = got
			if !done {
				e.short = append(e.short, p)
			}
		}
		e.mu.Unlock()
		out = append(out, got...)
	}
	return out
}

// sandboxSetup is what of the settings decides whether the sandbox can
// run here: which bubblewrap, and whether it has a /proc of its own.
func sandboxSetup(sc config.Sandbox) sandbox.Setup {
	exe, _ := sandboxFilter(sc)
	return sandbox.Setup{Bwrap: sc.Bwrap, HostProc: sc.Loose("enableWeakerNestedSandbox"), Filter: exe}
}

// sandboxFilter is the program that puts the filter for Unix sockets on a
// sandboxed command: gnosh itself, on Linux, unless every Unix socket is
// let (DECISIONS 367). Where gnosh cannot find its own program, there is
// none, and why says so, for the person to be told (not a filter dropped
// without a word: the second sandbox review).
func sandboxFilter(sc config.Sandbox) (exe, why string) {
	if runtime.GOOS != "linux" || sc.Loose("network.allowAllUnixSockets") {
		return "", ""
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "nothing keeps sandboxed commands from opening Unix sockets: gnosh could not find its own program, which puts that filter on (" + err.Error() + ")"
	}
	return exe, ""
}

// sandboxOf works the session's sandbox out, the first time it is asked.
func (st *startup) sandboxOf(cfg *config.Config) *sandboxState {
	s := &st.sandbox
	s.once.Do(func() {
		if !cfg.Sandbox.Enabled {
			return
		}
		c := sandbox.CheckWith(sandboxSetup(cfg.Sandbox))
		if !c.OK {
			s.why = c.Why
			return
		}
		s.weaker = c.Notes(cfg.Sandbox.Loose("allowNestedUserNamespaces"))
		if _, why := sandboxFilter(cfg.Sandbox); why != "" {
			s.weaker = strings.TrimPrefix(s.weaker+", and "+why, ", and ")
		}
		temp, err := os.MkdirTemp("", "gnosh-sandbox-")
		if err != nil {
			s.why = "its temporary folder could not be made (" + err.Error() + ")"
			return
		}
		// gnosh's own folder in the project made now, so that it is there
		// to mount read-only: made by a sandboxed command, kinds of agent
		// in it would run their hooks outside, and gnosh makes it itself,
		// so it is not one taken away after a command (sandboxMade).
		ensureDataDir(cfg)
		s.on, s.temp = true, temp
	})
	return s
}

// sandboxPermission is what the permission engine is told of the sandbox.
func (st *startup) sandboxPermission(cfg *config.Config) permission.Sandbox {
	if !st.sandboxOf(cfg).on {
		return permission.Sandbox{}
	}
	// With the files left open a command could change what gnosh and the
	// shell obey later (the person's settings, a shell's startup files):
	// it asks as any command does, auto-allow or not (DECISIONS 364;
	// Claude Code lets such a command run, and says so).
	auto := cfg.Sandbox.AutoAllow && !cfg.Sandbox.Loose("filesystem.disabled")
	return permission.Sandbox{On: true, AutoAllow: auto, Outside: sandboxOutside(cfg.Sandbox)}
}

// sandboxOutside is the test, shared by the shell tool and the engine, of
// a command that runs outside the sandbox: one the settings exclude, or,
// with asked, one the model asked out of it, where the settings let it.
func sandboxOutside(sc config.Sandbox) func(command string, asked bool) bool {
	return func(command string, asked bool) bool {
		return asked && sc.AllowUnsandboxed || excludedCall(sc.Excluded, command)
	}
}

// shellSandbox is the shell tool's sandbox, or nil when it is off or
// cannot run here (the person was told at startup, sandboxNotice).
func (l *live) shellSandbox() *shelltool.Sandbox {
	sc := l.cfg.Sandbox
	state := l.st.sandboxOf(l.cfg)
	if !state.on {
		return nil
	}
	temp := state.temp
	// The proxy, whenever the sandbox is on: a host the lists do not name
	// is asked about (or refused), as Claude Code asks.
	hosts, network := listValues(sc.AllowedDomains), true
	if err := l.startSandboxProxy(temp); err != nil {
		l.notice(agent.LevelWarning, "sandbox", "The sandbox's proxy could not start ("+err.Error()+"), so sandboxed commands reach no host on the network.")
		hosts, network = nil, false
	}
	writes := "the session's folders and /tmp (this session's own)"
	if runtime.GOOS != "linux" {
		writes = "the session's folders and $TMPDIR (this session's own)"
	}
	before, after := sandboxBefore, sandboxAfter
	if sc.Loose("filesystem.disabled") {
		// (Nothing of the files is kept: what a command the person let
		// run makes stays.)
		writes, before, after = "", nil, nil
	}
	var ignored []shelltool.Ignored
	for _, iv := range sc.IgnoreViolations {
		ignored = append(ignored, shelltool.Ignored{Command: iv.Command, What: iv.What})
	}
	return &shelltool.Sandbox{
		Hosts:   hosts,
		Network: l.sandboxNetworkText(network),
		Wrap: func(dir string, roots, argv []string, expose []sandbox.Exposed) ([]string, []string, error) {
			p := l.sandboxPolicy(roots, temp, expose)
			out, err := sandbox.Wrap(p, dir, argv)
			return out, sandbox.Env(p), err
		},
		Outside:     sandboxOutside(sc),
		Unsandboxed: sc.AllowUnsandboxed,
		Writes:      writes,
		NoTrust:     runtime.GOOS == "darwin" && !sc.Loose("enableWeakerNetworkIsolation"),
		// (On macOS a refused read says what a refused write says.)
		HidesReads: runtime.GOOS == "darwin" && (len(sc.DenyRead) > 0 || len(sc.HiddenFiles) > 0),
		Ignored:    ignored,
		Before:     before,
		After:      after,
	}
}

// sandboxPolicy is what a sandboxed command may do: write in the session's
// folders and those the settings add, but not in what gnosh, Claude Code
// or another program would later run or obey outside the sandbox
// (sandboxGuarded, gnosh's own folders when they lie in one of the
// session's), or what the settings keep read-only; read all but what the
// settings deny.
func (l *live) sandboxPolicy(roots []string, temp string, expose []sandbox.Exposed) sandbox.Policy {
	sc := l.cfg.Sandbox
	p := sandbox.Policy{Temp: temp, Expose: expose, Proxy: l.sandboxNet.proxy, Relay: l.sandboxNet.relay}
	p.Write = append(append(p.Write, roots...), listValues(sc.AllowWrite)...)
	for _, root := range roots {
		for _, name := range sandboxGuarded {
			p.ReadOnly = append(p.ReadOnly, filepath.Join(root, name))
		}
		writes, guards := gitGuards(root)
		p.Write = append(p.Write, writes...)
		p.ReadOnly = append(p.ReadOnly, guards...)
	}
	// gnosh's own folders (its settings, its state, its sessions): but one
	// that holds one of the session's folders (a work tree gnosh made
	// under its data folder) is left as the folders inside it make it.
	for _, own := range []string{l.cfg.Paths.ConfigDir, l.cfg.Paths.StateDir, l.cfg.Paths.DataDir} {
		if own != "" && !slices.ContainsFunc(p.Write, func(w string) bool { return w != own && strings.HasPrefix(w, own+string(filepath.Separator)) }) {
			p.ReadOnly = append(p.ReadOnly, own)
		}
	}
	p.ReadOnly = append(append(p.ReadOnly, settingsFiles(l.cfg)...), listValues(sc.DenyWrite)...)
	found := l.st.sandboxOf(l.cfg).sandboxFound
	p.DenyRead, p.AllowRead = found(listValues(sc.DenyRead)), listValues(sc.AllowRead)
	// The credentials the settings hide: files never to be read, whatever
	// opens them again; variables not given.
	p.Hidden = found(listValues(sc.HiddenFiles))
	p.HideVars = listValues(sc.HiddenVars)
	if l.engine != nil {
		// The Read and Edit rules' paths, as Claude Code adds them: a
		// Read(./.env) deny rule keeps .env from a script's cat too.
		read, write, allow := l.engine.SandboxPaths()
		p.DenyRead = append(p.DenyRead, found(read)...)
		p.ReadOnly = append(p.ReadOnly, write...)
		p.Write = append(p.Write, allow...)
	}
	// What the settings loosen (DECISIONS 364).
	p.Bwrap = sc.Bwrap
	p.FilesystemOff = sc.Loose("filesystem.disabled")
	p.HostProc = sc.Loose("enableWeakerNestedSandbox")
	p.UserNamespaces = sc.Loose("allowNestedUserNamespaces")
	p.AppleEvents = sc.Loose("allowAppleEvents")
	p.TrustService = sc.Loose("enableWeakerNetworkIsolation")
	p.AllUnixSockets = sc.Loose("network.allowAllUnixSockets")
	p.LocalBinding = sc.Loose("network.allowLocalBinding")
	p.UnixSockets, p.MachLookup = listValues(sc.UnixSockets), listValues(sc.MachLookup)
	// gnosh itself, run inside to refuse the command new Unix sockets
	// (DECISIONS 367; allowAllUnixSockets lifts it).
	p.Filter, _ = sandboxFilter(sc)
	return p
}

// sandboxGuarded are the paths in each of the session's folders that a
// sandboxed command may not change, since something runs or obeys them
// outside the sandbox later, as Claude Code's sandbox keeps its own:
// gnosh's and Claude Code's folders whole (settings, skills, kinds of
// agent, commands, rules, hooks: a folder mounted read-only cannot be
// renamed away and made again, as a file inside one could), the MCP and
// language servers' files; the files a shell, git, direnv or an editor
// reads as it starts there (the folder may be the person's home). Only
// what is there is kept so on Linux, where bubblewrap mounts what exists:
// what a command makes of these is taken away after it (sandboxMade).
// git's own are gitGuards'.
var sandboxGuarded = []string{
	"gnosh.json", ".gnosh", ".claude", ".mcp.json", ".lsp.json",
	".bashrc", ".bash_profile", ".bash_login", ".profile", ".zshrc", ".zshenv", ".zprofile", ".zlogin", ".gitconfig", ".envrc",
	".vscode", ".idea", ".ssh", ".config/gnosh",
}

// sandboxMade are the paths in each of the session's folders that a
// sandboxed command may not make where they were not there before it,
// and are taken away after it if it did: gnosh's and Claude Code's
// settings and folders (gnosh's own .gnosh, which gnosh itself makes, is
// not among them), a top-level HEAD (which would make the folder a bare
// repository whose config git then obeys), and git's way to settings
// kept elsewhere.
var sandboxMade = []string{"gnosh.json", ".claude", ".mcp.json", ".lsp.json", "HEAD", ".git/commondir", ".git/config.worktree"}

// gitGuards are the git paths a command in root may not change (hooks,
// settings, the ways to settings kept elsewhere, the submodules'), and
// the git folders it must write in for git to work in a work tree of
// another repository: the work tree's own, and the repository's, its
// hooks and settings kept read-only. A folder that is a bare repository's
// keeps its HEAD, config and hooks read-only too (the sandbox review).
func gitGuards(root string) (writes, guards []string) {
	names := []string{"hooks", "config", "commondir", "config.worktree", "modules"}
	if info, err := os.Lstat(filepath.Join(root, "HEAD")); err == nil && !info.IsDir() {
		for _, n := range []string{"HEAD", "config", "hooks"} {
			guards = append(guards, filepath.Join(root, n))
		}
	} else if info, err := os.Lstat(filepath.Join(root, "config")); err == nil && info.Mode().IsRegular() {
		guards = append(guards, filepath.Join(root, "config"))
	}
	dotgit := filepath.Join(root, ".git")
	info, err := os.Lstat(dotgit)
	switch {
	case err != nil:
		return writes, guards
	case info.IsDir():
		for _, n := range names {
			guards = append(guards, filepath.Join(dotgit, n))
		}
		return writes, guards
	}
	// A work tree: .git is a file that names its git folder.
	guards = append(guards, dotgit)
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return writes, guards
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return writes, guards
	}
	gd = strings.TrimSpace(gd)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(root, gd)
	}
	gd = filepath.Clean(gd)
	common := gd
	if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		cd := strings.TrimSpace(string(c))
		if !filepath.IsAbs(cd) {
			cd = filepath.Join(gd, cd)
		}
		common = filepath.Clean(cd)
	}
	writes = append(writes, gd, common)
	for _, n := range names {
		guards = append(guards, filepath.Join(common, n))
	}
	guards = append(guards, filepath.Join(gd, "config.worktree"), filepath.Join(gd, "commondir"), filepath.Join(gd, "gitdir"))
	return writes, guards
}

// sandboxBefore notes which of sandboxMade are in the session's folders as
// a sandboxed command starts.
func sandboxBefore(roots []string) any {
	was := map[string]bool{}
	for _, root := range roots {
		for _, name := range sandboxMade {
			p := filepath.Join(root, name)
			_, err := os.Lstat(p)
			was[p] = err == nil
		}
	}
	return was
}

// sandboxAfter takes away what the command made of sandboxMade, and says
// so; nothing when it made none.
func sandboxAfter(state any) string {
	was, _ := state.(map[string]bool)
	var gone []string
	for _, p := range slices.Sorted(maps.Keys(was)) {
		if was[p] {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			if os.RemoveAll(p) == nil {
				gone = append(gone, p)
			}
		}
	}
	if len(gone) == 0 {
		return ""
	}
	return "the sandbox took away what the command made of gnosh's, Claude Code's or git's settings, which would have been obeyed outside it: " + strings.Join(gone, ", ")
}

// excludedCall reports whether excludedCommands takes a command out of the
// sandbox: a single plain command (no list, pipe, redirection, command
// substitution, subshell or variable: Claude Code keeps those in it) that
// fits one of the entries as the content of a Bash(...) rule fits: "docker"
// is that command alone, "docker *" it with any arguments too, and a * in
// the middle any text.
func excludedCall(entries []config.ListItem, command string) bool {
	c := strings.TrimSpace(command)
	// (A redirection that only joins one stream to another is no file.)
	for _, dup := range []string{" 2>&1", " 1>&2", " >&2"} {
		c = strings.TrimSuffix(c, dup)
	}
	if c == "" || len(entries) == 0 || strings.ContainsAny(c, ";&|<>()$`{}\n\\\t\r\v\f") {
		return false
	}
	for _, r := range c {
		if r > 127 {
			// (A space of another kind, or a letter that looks like one
			// of the entry's: plain text alone is matched.)
			return false
		}
	}
	words := strings.Fields(c)
	switch words[0] {
	case "sudo", "eval", "xargs", "cd", "pushd", "popd", "exec", "env", "command", "builtin":
		return false
	case "git":
		// A clone or a new work tree writes a whole tree of files where its
		// path points: one outside the folder stays in the sandbox; and
		// the options that point git at another repository, another
		// template (whose hooks would run outside) or another program stay
		// in too (the sandbox review).
		for _, w := range words[1:] {
			if strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~") || strings.Contains(w, "..") {
				return false
			}
			for _, opt := range []string{"--git-dir", "--work-tree", "--template", "--exec-path", "--upload-pack", "--receive-pack", "--config", "-c", "-C", "--separate-git-dir", "--reference"} {
				if w == opt || strings.HasPrefix(w, opt+"=") || len(opt) == 2 && strings.HasPrefix(w, opt) {
					return false
				}
			}
		}
	}
	c = strings.Join(words, " ")
	for _, e := range entries {
		if ruleContentFits(strings.TrimSpace(e.V), c) {
			return true
		}
	}
	return false
}

// ruleContentFits reports whether a command fits a Bash rule's content:
// without a *, exactly; ending in " *", that prefix alone or with
// arguments; otherwise with each * standing for any text.
func ruleContentFits(pattern, command string) bool {
	switch {
	case !strings.Contains(pattern, "*"):
		return command == pattern
	case strings.HasSuffix(pattern, " *") && !strings.Contains(pattern[:len(pattern)-2], "*"):
		prefix := pattern[:len(pattern)-2]
		return command == prefix || strings.HasPrefix(command, prefix+" ")
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(command, parts[0]) {
		return false
	}
	rest := command[len(parts[0]):]
	for i, part := range parts[1:] {
		last := i == len(parts)-2
		switch {
		case last:
			return strings.HasSuffix(rest, part)
		default:
			at := strings.Index(rest, part)
			if at < 0 {
				return false
			}
			rest = rest[at+len(part):]
		}
	}
	return true
}

// sandboxNotice is what the person is told at startup of a sandbox the
// settings turn on that cannot run here, and the error that stops gnosh
// when failIfUnavailable says to.
func (l *live) sandboxNotice() (notice string, stop error) {
	cfg := l.cfg
	state := l.st.sandboxOf(cfg)
	if state.on && state.weaker != "" {
		return "The sandbox is on, but " + state.weaker + ".", nil
	}
	if !cfg.Sandbox.Enabled || state.on {
		return "", nil
	}
	if cfg.Sandbox.FailIfUnavailable {
		return "", &sandboxError{why: state.why, origin: cfg.Sandbox.Origin.String()}
	}
	return "The sandbox is on (" + cfg.Sandbox.Origin.String() + ") and cannot run here: " + state.why + ". Commands run outside it, asking as they would without it.", nil
}

// closeSandbox stops the session's proxy and removes its temporary folder,
// once its commands are gone.
func (l *live) closeSandbox() {
	if l.sandboxProxy != nil {
		_ = l.sandboxProxy.Close()
	}
	if l.st != nil {
		if t := l.st.sandbox.temp; t != "" {
			_ = os.RemoveAll(t)
		}
	}
}

// sandboxError stops gnosh where the sandbox must run and cannot.
type sandboxError struct{ why, origin string }

func (e *sandboxError) Error() string {
	return "the sandbox is on (" + e.origin + "), with failIfUnavailable, and cannot run here: " + e.why
}

// startSandboxProxy starts the sandbox's proxy for the session: on Linux
// at a socket in the sandbox's temporary folder (its /tmp), reached
// through gnosh itself run inside as the relay; on macOS on this machine's
// loopback, the one address Seatbelt lets a sandboxed command reach.
func (l *live) startSandboxProxy(temp string) error {
	sc := l.cfg.Sandbox
	d := sandbox.Domains{Allowed: sandboxAllowed(sc), Denied: listValues(sc.DeniedDomains)}
	network, address := "tcp", "127.0.0.1:0"
	relay := ""
	if runtime.GOOS == "linux" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		network, address, relay = "unix", filepath.Join(temp, fmt.Sprintf("proxy-%d-%d.sock", os.Getpid(), time.Now().UnixNano()%1e9)), exe
	}
	p, err := sandbox.ListenProxy(network, address, d)
	if err != nil {
		return err
	}
	p.Told = func(host, why string) {
		l.flog.logf("sandbox", "a sandboxed command was not let reach %s: %s", host, why)
	}
	if e := l.engine; e != nil {
		p.Rules = func(host string) sandbox.HostRule {
			switch e.FetchRule(host, sc.Strict) {
			case permission.HostDenied:
				return sandbox.HostDenied
			case permission.HostAsked:
				return sandbox.HostAsked
			case permission.HostAllowed:
				return sandbox.HostAllowed
			}
			return sandbox.HostUnruled
		}
	}
	if !sc.Strict {
		p.Ask = l.askNetHost
	}
	l.sandboxProxy = p
	l.sandboxNet.proxy, l.sandboxNet.relay = p.Addr().String(), relay
	return nil
}

// ensureDataDir makes the project's data folder (.gnosh) when it is not
// there, as gnosh makes it to save there, with what is in it kept out of
// git; never through a link that leads out of the project.
func ensureDataDir(cfg *config.Config) {
	dir := cfg.Paths.DataDir
	if dir == "" || dataDirLeadsOut(cfg) != "" {
		return
	}
	if _, err := os.Lstat(dir); err == nil {
		return
	}
	if os.MkdirAll(dir, 0o700) == nil {
		_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o600)
	}
}

// sandboxAllowed are the hosts allowedDomains lets through: with
// strictAllowlist, those of the person's own files alone, as Claude Code
// leaves a repository's out then (the sandbox review; a project's local
// file is left out with them, as gnosh cannot tell here whose it is).
func sandboxAllowed(sc config.Sandbox) []string {
	var out []string
	for _, it := range sc.AllowedDomains {
		if !sc.Strict || it.Origin.Layer != config.LayerProject {
			out = append(out, it.V)
		}
	}
	return out
}

// sandboxNetworkText says what of the network a sandboxed command reaches,
// for the model.
func (l *live) sandboxNetworkText(network bool) string {
	sc := l.cfg.Sandbox
	if !network {
		return "reach no network"
	}
	reach := "reach the network only through a proxy that HTTP_PROXY and HTTPS_PROXY name (a program that ignores those reaches nothing), which lets through "
	let := "the hosts WebFetch(domain:...) allow rules name"
	if hosts := sandboxAllowed(sc); len(hosts) > 0 {
		let = strings.Join(hosts, ", ") + " and " + let
	}
	switch {
	case sc.Strict:
		return reach + let + ", and no other host"
	case !l.interactive:
		// (The mode may change while it runs: the sandbox review.)
		return reach + let + ", and any other host only as the permission mode says, as nobody is here to be asked about it"
	}
	return reach + let + " at once, and any other host once the person allows it (the command waits while they are asked)"
}

// askNetHost asks the person whether a sandboxed command may reach a host
// nothing lets through, as Claude Code asks: in bypassPermissions mode it
// may, in dontAsk mode and where nobody can be asked it may not. "Yes"
// lets it reach the host for the session; "Yes, and don't ask again"
// keeps a WebFetch(domain:...) allow rule for the project, which Claude
// Code's sandbox takes in too.
//
// forced is for a host an ask rule names: asked even in bypassPermissions
// mode, as an ask rule asks of a tool call. An answer the mode gives is
// not kept for the session (the mode may change); the person's is.
func (l *live) askNetHost(ctx context.Context, host string, port int, forced bool) (yes bool, why string, kept bool) {
	no := host + " is not in sandbox.network.allowedDomains, and "
	if forced {
		no = "a WebFetch(domain:...) ask rule names " + host + ", and "
	}
	switch l.engine.Mode() {
	case permission.ModeBypass:
		if !forced {
			return true, "", false
		}
	case permission.ModeDontAsk:
		return false, no + "dontAsk mode refuses what would ask", false
	}
	if !l.interactive || l.agent == nil {
		return false, no + "nobody is here to be asked about it", false
	}
	const yesLabel, keep, refuse = "Yes", "Yes, and don't ask again for this host", "No"
	q := "Let a command in the sandbox reach " + host + "?"
	lead := "A command running in the sandbox wants to reach " + host + " (port " + strconv.Itoa(port) + "), which no setting lets it reach."
	if forced {
		lead = "A command running in the sandbox wants to reach " + host + " (port " + strconv.Itoa(port) + "), which an ask rule says to ask about."
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		lead += " It is an address of this machine or its network: its own servers listen there."
	}
	// (The answer is for the host on every port, as Claude Code's is: the
	// sandbox review found "port 3000" read as the whole of it.)
	lead += " The answer is for the host on every port."
	form := QuestionForm{EscCancels: true, Lead: lead,
		Questions: []FormQuestion{{Question: q, Header: "Network", NoOther: true,
			Options: []FormOption{{Label: yesLabel, Description: "for this session"}, {Label: keep, Description: "keeps WebFetch(domain:" + host + ") for this project"}, {Label: refuse}}}}}
	ans, err := l.agent.Ask(ctx, agent.Question{Kind: questionKind, Title: "Let a sandboxed command reach " + host + "?", Text: form.Lead, Data: form})
	if err != nil {
		return false, no + "the question was not answered", false
	}
	values, _ := ans.Data.(map[string]string)
	rule := "WebFetch(domain:" + host + ")"
	switch values[q] {
	case yesLabel:
		_ = l.engine.Grant(rule, permission.GrantSession)
		l.flog.logf("sandbox", "the person let sandboxed commands reach %s for the session", host)
		return true, "", true
	case keep:
		if err := l.engine.Grant(rule, permission.GrantLocal); err == nil {
			if err := l.saveRules([]string{rule}); err != nil {
				l.engine.Unkeep(rule, permission.ListAllow)
				_ = l.engine.Grant(rule, permission.GrantSession)
				l.notice(agent.LevelWarning, "sandbox", "Sandboxed commands may reach "+host+" for this session, but it could not be written down ("+err.Error()+"), so gnosh will ask again next time.")
			}
		}
		return true, "", true
	}
	return false, "the person did not let the command reach " + host, true
}
