package config

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// This file is Claude Code's "sandbox" settings key: whether the shell's
// commands run inside the operating system's sandbox (spec/DECISIONS.md
// 352 and 353), and what they may do there. What only takes a power away
// is in force from a repository's file before the folder is trusted
// (turning the sandbox on, failIfUnavailable, allowUnsandboxedCommands
// false, autoAllowBashIfSandboxed false, the deny lists, a loosening set
// false), and a repository's file cannot turn the person's sandbox off;
// what gives one waits for the trust (autoAllowBashIfSandboxed true,
// excludedCommands, allowWrite, allowRead, the loosenings). The keys
// Claude Code takes from the person's own settings alone are left out of
// a repository's (DECISIONS 364).

// Sandbox is what the sandbox settings say, every file's merged.
type Sandbox struct {
	// Enabled runs the shell's commands in the sandbox; Origin is the line
	// that turned it on.
	Enabled bool
	Origin  Origin
	// FailIfUnavailable stops gnosh at startup when the sandbox is on and
	// cannot run here, where otherwise commands run outside it, with a
	// notice.
	FailIfUnavailable bool
	// AutoAllow runs a command that runs in the sandbox without asking,
	// as an allow rule would (autoAllowBashIfSandboxed).
	AutoAllow bool
	// AllowUnsandboxed lets the model ask for a command to run outside the
	// sandbox (the Bash tool's dangerouslyDisableSandbox), which then asks
	// as any command would: true unless a file says false.
	AllowUnsandboxed bool
	// Excluded are the commands that run outside the sandbox, as the
	// content of a Bash(...) rule (excludedCommands).
	Excluded []ListItem
	// AllowWrite and DenyWrite are more paths a sandboxed command may
	// write, and paths it may not; AllowRead and DenyRead, the same for
	// reading. Each is absolute.
	AllowWrite, DenyWrite, AllowRead, DenyRead []ListItem
	// HiddenFiles are credential files a sandboxed command may not read,
	// and HiddenVars variables it is not given (credentials.files and
	// envVars; a "mask" entry, not built, is hidden as "deny").
	HiddenFiles, HiddenVars []ListItem
	// Strict refuses a host the lists do not name, where the person would
	// otherwise be asked (network.strictAllowlist, the person's own files
	// alone).
	Strict bool
	// AllowedDomains are the hosts a sandboxed command may reach, through
	// gnosh's proxy, and DeniedDomains those it may not whatever else says
	// (network.allowedDomains and network.deniedDomains): a name,
	// *.name, or an address, each with :port for that port alone.
	AllowedDomains, DeniedDomains []ListItem
	// Loosened are the sandbox's loosenings in force, each by its key
	// under "sandbox" (Loosenings) with the line that set it.
	Loosened map[string]Origin
	// UnixSockets are the Unix sockets a sandboxed command may connect to
	// on macOS (network.allowUnixSockets), and MachLookup macOS's services
	// it may reach (network.allowMachLookup).
	UnixSockets, MachLookup []ListItem
	// IgnoreViolations are reports of what the sandbox stopped that are
	// not made (ignoreViolations).
	IgnoreViolations []IgnoredViolation
	// Bwrap is bubblewrap's path, from the person's own settings
	// (bwrapPath), at BwrapOrigin.
	Bwrap       string
	BwrapOrigin Origin
	// Scrubbed is CLAUDE_CODE_SUBPROCESS_ENV_SCRUB in force (envscrub),
	// which takes back, as Claude Code does, filesystem.disabled from
	// every file, and on Linux what lets a command out (excludedCommands,
	// the model asking out of it, allowNestedUserNamespaces): there every
	// command runs in it, the person's own ! commands too.
	Scrubbed bool
}

// scrub takes back what the scrub takes back (Scrubbed), on goos.
func (s *Sandbox) scrub(goos string) {
	s.Scrubbed = true
	delete(s.Loosened, "filesystem.disabled")
	if goos == "linux" {
		s.Excluded, s.AllowUnsandboxed = nil, false
		delete(s.Loosened, "allowNestedUserNamespaces")
	}
}

// Loosenings are the sandbox's switches that let its commands do more,
// each Claude Code's key under "sandbox": on only where a file in force
// says true and none says false.
var Loosenings = []string{"enableWeakerNestedSandbox", "enableWeakerNetworkIsolation", "allowAppleEvents",
	"allowNestedUserNamespaces", "filesystem.disabled", "network.allowAllUnixSockets", "network.allowLocalBinding"}

// Loose reports whether the loosening of that key is in force.
func (s Sandbox) Loose(key string) bool {
	_, ok := s.Loosened[key]
	return ok
}

// IgnoredViolation is a report of the sandbox's that is not made: for a
// command holding Command ("*" for every command), of what was stopped
// holding What (a path, usually).
type IgnoredViolation struct {
	Command, What string
	Origin        Origin
}

// sandboxBuild is the sandbox settings as the builder gathers them: for
// each switch, the places that set it, so that a project's can be taken
// out again.
type sandboxBuild struct {
	// projectOn are the project's files that turn it on; person, the
	// person's own that turn it on or off, in the order they apply (a
	// later file wins).
	projectOn                                 []Origin
	person                                    []sandboxToggle
	failIfOff, noUnsandboxed, autoOn, autoOff []Origin
	// autoOnTheirs are a repository's files that turn auto-allow on: in
	// force only with the sandbox the repository's own files turned on.
	autoOnTheirs                               []Origin
	excluded, allowWrite, denyWrite, allowRead allowList
	denyRead, allowedDomains, deniedDomains    allowList
	hiddenFiles, hiddenVars                    allowList
	strict                                     []Origin
	// looseOn and looseOff are, for each loosening, the lines that turn it
	// on and those that keep it off.
	looseOn, looseOff       map[string][]Origin
	unixSockets, machLookup allowList
	ignored                 []IgnoredViolation
	bwrap                   string
	bwrapOrigin             Origin
}

// put adds a line to a loosening's list.
func putLoose(m *map[string][]Origin, key string, o Origin) {
	if *m == nil {
		*m = map[string][]Origin{}
	}
	(*m)[key] = append((*m)[key], o)
}

// sandboxToggle is one of the person's files turning the sandbox on or
// off.
type sandboxToggle struct {
	on bool
	o  Origin
}

// sandboxKeys are the keys of "sandbox" gnosh reads.
var sandboxKeys = []keyDef{
	{"enabled", "", "run the shell's commands in the sandbox (bubblewrap on Linux, Seatbelt on macOS)", fromClaudeCode},
	{"autoAllowBashIfSandboxed", "", "run a command that runs in the sandbox without asking, as an allow rule would", fromClaudeCode},
	{"failIfUnavailable", "", "stop at startup when the sandbox is on and cannot run here", fromClaudeCode},
	{"allowUnsandboxedCommands", "", "false: the model cannot ask for a command to run outside the sandbox", fromClaudeCode},
	{"excludedCommands", "", "commands that run outside the sandbox, as a Bash(...) rule's content", fromClaudeCode},
	{"filesystem", "", "allowWrite, denyWrite, allowRead and denyRead: lists of paths", fromClaudeCode},
	{"network", "", "allowedDomains and deniedDomains: the hosts a sandboxed command may reach through gnosh's proxy, and those it may not", fromClaudeCode},
	{"credentials", "", "files and envVars: credential files a sandboxed command may not read, and variables it is not given ({\"path\" or \"name\", \"mode\": \"deny\"})", fromClaudeCode},
	{"ignoreViolations", "", "what the sandbox stopped that is not reported: {\"a piece of the command, or *\": [\"a piece of what was stopped, such as a path\"]}", fromClaudeCode},
	{"enableWeakerNestedSandbox", "", "on Linux, the machine's /proc in the sandbox, for an unprivileged container (shows the command your other processes)", fromClaudeCode},
	{"enableWeakerNetworkIsolation", "", "on macOS, let sandboxed commands reach the certificate trust service (Go's programs check certificates with it; it can fetch for them)", fromClaudeCode},
	{"allowAppleEvents", "", "on macOS, let sandboxed commands send Apple Events (open, osascript), and so start programs outside the sandbox; your own settings only", fromClaudeCode},
	{"allowNestedUserNamespaces", "", "on Linux, let sandboxed commands make user namespaces (Chromium's sandbox, a nested bubblewrap); your own settings only", fromClaudeCode},
	{"bwrapPath", "", "bubblewrap's path, where it is not /usr/bin/bwrap; your own settings only", fromClaudeCode},
	{"socatPath", "", "Claude Code's socat; gnosh relays the network itself, and leaves it out", fromClaudeCode},
	{"ripgrep", "", "Claude Code's ripgrep for its sandbox; gnosh's sandbox runs none, and leaves it out", fromClaudeCode},
}

// sandbox reads the "sandbox" key.
func (s *source) sandbox(f jfield) {
	if f.val.kind != jObject {
		s.errorf(f.line, "\"sandbox\" is an object of the sandbox's settings ({\"enabled\": true, ...}), and here it is %s.", f.val.kind.word())
		return
	}
	// (A local file the repository shipped is the repository's too: the
	// sandbox review found its "enabled": false taken for the person's.)
	theirs := s.theirs || s.scope == "project"
	for _, m := range s.unique(f.val, "sandbox.") {
		o := s.at(m.line)
		switch m.key {
		case "enabled", "failIfUnavailable", "autoAllowBashIfSandboxed", "allowUnsandboxedCommands":
			if m.val.kind != jBool {
				s.errorf(m.line, "\"sandbox.%s\" is true or false, and here it is %s.", m.key, m.val.kind.word())
				continue
			}
			s.sandboxSwitch(m.key, m.val.b, theirs, o)
		case "excludedCommands":
			items, ok := s.sandboxList(m, "commands, each the content of a Bash(...) rule such as \"docker *\"")
			if ok && len(items) > 0 {
				s.add(op{kind: KindAllowRule, text: "sandbox.excludedCommands: " + strings.Join(items, ", "), origin: o, what: "a sandbox setting",
					apply: func(b *builder) { b.sandbox.excluded.add(items, o) }})
			}
		case "filesystem":
			s.sandboxFilesystem(m)
		case "network":
			s.sandboxNetwork(m)
		case "credentials":
			s.sandboxCredentials(m)
		case "enableWeakerNestedSandbox", "enableWeakerNetworkIsolation":
			s.sandboxLoose(m.key, m, false)
		case "allowAppleEvents", "allowNestedUserNamespaces":
			s.sandboxLoose(m.key, m, true)
		case "ignoreViolations":
			s.sandboxIgnore(m)
		case "bwrapPath":
			s.sandboxBwrap(m)
		case "socatPath":
			s.probs.warnf(s.file, m.line, "\"sandbox.socatPath\" is left out: gnosh relays a sandboxed command's network itself, with no socat.")
		case "ripgrep":
			s.probs.warnf(s.file, m.line, "\"sandbox.ripgrep\" is left out: gnosh's sandbox runs no ripgrep of its own.")
		default:
			s.errorf(m.line, "%s is not a key gnosh reads under \"sandbox\". The keys are: %s.", showName(m.key), keyNames(sandboxKeys))
		}
	}
}

// sandboxLoose puts one of the sandbox's loosenings in force: true from a
// file in force loosens it, a repository's once the folder is trusted;
// false from any keeps it off, as a switch that takes a power away does.
// own is for the keys Claude Code takes from the person's own settings
// alone: a repository's file can turn them neither on nor off.
func (s *source) sandboxLoose(key string, m jfield, own bool) {
	o := s.at(m.line)
	if m.val.kind != jBool {
		s.errorf(m.line, "\"sandbox.%s\" is true or false, and here it is %s.", key, m.val.kind.word())
		return
	}
	if own && (s.theirs || s.scope == "project") {
		s.probs.warnf(s.file, m.line, "\"sandbox.%s\" in a project's file is not taken: as in Claude Code, only your own settings can set it. It is left out.", key)
		return
	}
	text := "sandbox." + key + ": " + strconv.FormatBool(m.val.b)
	if m.val.b {
		s.add(op{kind: KindSandboxWider, text: text, origin: o, what: "a sandbox setting",
			apply: func(b *builder) { putLoose(&b.sandbox.looseOn, key, o) }})
		return
	}
	s.add(op{kind: KindRestriction, text: text, origin: o, what: "a sandbox setting",
		apply: func(b *builder) { putLoose(&b.sandbox.looseOff, key, o) }})
}

// sandboxIgnore reads "sandbox.ignoreViolations": for a command (a piece
// of it, or "*" for every one), the pieces of what the sandbox stopped
// that are not reported, to the person or the model. The sandbox stops
// it all the same. It waits for the trust: a repository's could hide
// what its commands reach for.
func (s *source) sandboxIgnore(m jfield) {
	o := s.at(m.line)
	if m.val.kind != jObject {
		s.errorf(m.line, "\"sandbox.ignoreViolations\" is an object: for a piece of a command, or \"*\", a list of what not to report (a path, usually). Here it is %s.", m.val.kind.word())
		return
	}
	var add []IgnoredViolation
	var text []string
	for _, c := range s.unique(m.val, "sandbox.ignoreViolations.") {
		items, ok := c.val.strings()
		switch {
		case strings.TrimSpace(c.key) == "":
			s.errorf(c.line, "a command of \"sandbox.ignoreViolations\" is empty: give a piece of the command, or \"*\" for every one.")
			continue
		case !ok:
			s.errorf(c.line, "%s of \"sandbox.ignoreViolations\" is a list of what not to report (a path, usually), and here it is %s.", showName(c.key), c.val.kind.word())
			continue
		}
		for _, it := range items {
			if strings.TrimSpace(it.str) == "" {
				s.errorf(it.line, "an entry of \"sandbox.ignoreViolations\" is empty.")
				continue
			}
			add = append(add, IgnoredViolation{Command: c.key, What: it.str, Origin: o})
			text = append(text, c.key+": "+it.str)
		}
	}
	if len(add) == 0 {
		return
	}
	s.add(op{kind: KindOption, text: "sandbox.ignoreViolations: " + strings.Join(text, ", "), origin: o, what: "a sandbox setting",
		apply: func(b *builder) { b.sandbox.ignored = append(b.sandbox.ignored, add...) }})
}

// sandboxBwrap reads "sandbox.bwrapPath": bubblewrap's path, absolute,
// from the person's own settings alone (Claude Code's, from managed ones
// alone: a repository's could name a program that makes no sandbox).
func (s *source) sandboxBwrap(m jfield) {
	o := s.at(m.line)
	switch {
	case m.val.kind != jString:
		s.errorf(m.line, "\"sandbox.bwrapPath\" is a path, and here it is %s.", m.val.kind.word())
	case s.theirs || s.scope == "project":
		s.probs.warnf(s.file, m.line, "\"sandbox.bwrapPath\" in a project's file is not taken: a repository could name a program that makes no sandbox. It is left out.")
	case !filepath.IsAbs(expandHome(m.val.str, s.home)):
		s.probs.warnf(s.file, m.line, "\"sandbox.bwrapPath\" is %s, not a whole path, so it is left out, as Claude Code leaves it, and bubblewrap is looked for in /usr/bin and /bin.", showName(m.val.str))
	default:
		path := filepath.Clean(expandHome(m.val.str, s.home))
		s.add(op{kind: KindRestriction, text: "sandbox.bwrapPath: " + path, origin: o, what: "a sandbox setting",
			apply: func(b *builder) { b.sandbox.bwrap, b.sandbox.bwrapOrigin = path, o }})
	}
}

// sandboxSwitch puts one of the sandbox's switches in force, by what it
// gives or takes away: a repository's file cannot turn the person's
// sandbox off.
func (s *source) sandboxSwitch(key string, v, theirs bool, o Origin) {
	text := "sandbox." + key + ": " + strconv.FormatBool(v)
	put := func(kind string, set func(b *sandboxBuild) *[]Origin) {
		s.add(op{kind: kind, text: text, origin: o, what: "a sandbox setting",
			apply: func(b *builder) { p := set(&b.sandbox); *p = append(*p, o) }})
	}
	switch {
	case key == "enabled" && theirs && v:
		put(KindRestriction, func(b *sandboxBuild) *[]Origin { return &b.projectOn })
	case key == "enabled" && theirs:
		s.probs.warnf(s.file, o.Line, "\"sandbox.enabled\": false in a project's file does not turn the sandbox off: a repository cannot take away a sandbox the person turned on. It is left out.")
	case key == "enabled":
		s.add(op{kind: KindRestriction, text: text, origin: o, what: "a sandbox setting",
			apply: func(b *builder) { b.sandbox.person = append(b.sandbox.person, sandboxToggle{on: v, o: o}) }})
	case key == "failIfUnavailable" && v:
		put(KindRestriction, func(b *sandboxBuild) *[]Origin { return &b.failIfOff })
	case key == "allowUnsandboxedCommands" && !v:
		put(KindRestriction, func(b *sandboxBuild) *[]Origin { return &b.noUnsandboxed })
	case key == "autoAllowBashIfSandboxed" && v && theirs:
		put(KindAllowRule, func(b *sandboxBuild) *[]Origin { return &b.autoOnTheirs })
	case key == "autoAllowBashIfSandboxed" && v:
		put(KindAllowRule, func(b *sandboxBuild) *[]Origin { return &b.autoOn })
	case key == "autoAllowBashIfSandboxed":
		put(KindRestriction, func(b *sandboxBuild) *[]Origin { return &b.autoOff })
	}
	// (failIfUnavailable false and allowUnsandboxedCommands true are what
	// holds when nothing is said: nothing to put in force.)
}

// sandboxList reads a list of strings, none of them empty.
func (s *source) sandboxList(m jfield, what string) ([]string, bool) {
	items, ok := m.val.strings()
	if !ok {
		s.errorf(m.line, "\"sandbox.%s\" is a list of %s, and here it is not.", m.key, what)
		return nil, false
	}
	var out []string
	for _, it := range items {
		v := strings.TrimSpace(it.str)
		if v == "" {
			s.errorf(it.line, "an entry of \"sandbox.%s\" is empty.", m.key)
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// sandboxFilesystem reads "sandbox.filesystem": four lists of paths, in
// Claude Code's spelling: /x and //x from the root, ~/x from the home
// folder, and ./x or x from the project's root in a project's file, or
// from the folder of the person's own.
func (s *source) sandboxFilesystem(f jfield) {
	if f.val.kind != jObject {
		s.errorf(f.line, "\"sandbox.filesystem\" is an object of lists of paths (allowWrite, denyWrite, allowRead, denyRead), and here it is %s.", f.val.kind.word())
		return
	}
	base := s.baseDir
	if (s.scope == "project" || s.scope == "local") && s.ruleDir != "" {
		base = s.ruleDir
	}
	if s.scope == "command line" {
		base = s.cwd
	}
	for _, m := range s.unique(f.val, "sandbox.filesystem.") {
		o := s.at(m.line)
		key := "filesystem." + m.key
		switch m.key {
		case "allowWrite", "denyWrite", "allowRead", "denyRead":
		case "disabled":
			s.sandboxLoose(key, m, true)
			continue
		case "allowManagedReadPathsOnly":
			s.errorf(m.line, "\"sandbox.filesystem.allowManagedReadPathsOnly\" is a setting of Claude Code's managed settings, which gnosh does not have. Take it out of this file.")
			continue
		default:
			s.errorf(m.line, "%s is not a key gnosh reads under \"sandbox.filesystem\". The keys are allowWrite, denyWrite, allowRead, denyRead and disabled.", showName(m.key))
			continue
		}
		m.key = key
		items, ok := s.sandboxList(m, "paths")
		if !ok || len(items) == 0 {
			continue
		}
		paths := make([]string, 0, len(items))
		for _, it := range items {
			if strings.HasPrefix(it, "//") {
				it = it[1:]
			}
			// (A trailing /** is the folder with what is in it, which is
			// what a folder's path is to the sandbox.)
			it = strings.TrimSuffix(it, "/**")
			if strings.ContainsAny(it, "*?[") && m.key != "filesystem.denyRead" {
				// A pattern names the paths it matches: those a command may
				// not read are found as the session starts; where it may
				// write or read, a pattern is not taken, as Claude Code
				// takes none there on Linux (the sandbox review).
				s.probs.warnf(s.file, m.line, "%s in \"sandbox.%s\" is a pattern, and only a path, or a folder with a last /**, is taken there. It is left out.", showName(it), m.key)
				continue
			}
			paths = append(paths, absPath(it, s.home, base))
		}
		if len(paths) == 0 {
			continue
		}
		kind, list := KindRestriction, func(b *builder) *allowList { return &b.sandbox.denyWrite }
		switch m.key {
		case "filesystem.allowWrite":
			kind, list = KindAddDir, func(b *builder) *allowList { return &b.sandbox.allowWrite }
		case "filesystem.allowRead":
			kind, list = KindAddDir, func(b *builder) *allowList { return &b.sandbox.allowRead }
		case "filesystem.denyRead":
			list = func(b *builder) *allowList { return &b.sandbox.denyRead }
		}
		s.add(op{kind: kind, text: "sandbox." + m.key + ": " + strings.Join(paths, ", "), origin: o, what: "a sandbox setting",
			apply: func(b *builder) { list(b).add(paths, o) }})
	}
}

// sweepSandbox takes out of the sandbox settings what a project's file
// put there that the kind of it may not contribute (build.go's sweep).
func (b *sandboxBuild) sweep(ok func(string) bool, from func(Origin) bool, note func(string)) {
	drop := func(kind, what string, list *[]Origin) {
		if ok(kind) {
			return
		}
		n := len(*list)
		*list = slices.DeleteFunc(*list, from)
		if len(*list) != n {
			note(what)
		}
	}
	drop(KindRestriction, "sandbox.enabled", &b.projectOn)
	drop(KindRestriction, "sandbox.failIfUnavailable", &b.failIfOff)
	drop(KindRestriction, "sandbox.allowUnsandboxedCommands", &b.noUnsandboxed)
	drop(KindRestriction, "sandbox.autoAllowBashIfSandboxed", &b.autoOff)
	drop(KindAllowRule, "sandbox.autoAllowBashIfSandboxed", &b.autoOn)
	drop(KindAllowRule, "sandbox.autoAllowBashIfSandboxed", &b.autoOnTheirs)
	lists := []struct {
		kind, what string
		l          *allowList
	}{
		{KindAllowRule, "sandbox.excludedCommands", &b.excluded},
		{KindAddDir, "sandbox.filesystem.allowWrite", &b.allowWrite},
		{KindAddDir, "sandbox.filesystem.allowRead", &b.allowRead},
		{KindRestriction, "sandbox.filesystem.denyWrite", &b.denyWrite},
		{KindRestriction, "sandbox.filesystem.denyRead", &b.denyRead},
		{KindAddress, "sandbox.network.allowedDomains", &b.allowedDomains},
		{KindRestriction, "sandbox.network.deniedDomains", &b.deniedDomains},
		{KindRestriction, "sandbox.credentials.files", &b.hiddenFiles},
		{KindRestriction, "sandbox.credentials.envVars", &b.hiddenVars},
	}
	for _, l := range lists {
		if !ok(l.kind) && l.l.drop(from) {
			note(l.what)
		}
	}
	for _, l := range []struct {
		kind, what string
		l          *allowList
	}{
		{KindSandboxWider, "sandbox.network.allowUnixSockets", &b.unixSockets},
		{KindSandboxWider, "sandbox.network.allowMachLookup", &b.machLookup},
	} {
		if !ok(l.kind) && l.l.drop(from) {
			note(l.what)
		}
	}
	for _, key := range Loosenings {
		if on := b.looseOn[key]; !ok(KindSandboxWider) && len(on) > 0 {
			if b.looseOn[key] = slices.DeleteFunc(on, from); len(b.looseOn[key]) != len(on) {
				note("sandbox." + key)
			}
		}
		if off := b.looseOff[key]; !ok(KindRestriction) && len(off) > 0 {
			if b.looseOff[key] = slices.DeleteFunc(off, from); len(b.looseOff[key]) != len(off) {
				note("sandbox." + key)
			}
		}
	}
	if n := len(b.ignored); !ok(KindOption) && n > 0 {
		if b.ignored = slices.DeleteFunc(b.ignored, func(iv IgnoredViolation) bool { return from(iv.Origin) }); len(b.ignored) != n {
			note("sandbox.ignoreViolations")
		}
	}
}

// finish is the merged settings: on when a project's file turned it on,
// or the last of the person's own files to say turned it on.
func (b *sandboxBuild) finish() Sandbox {
	var out Sandbox
	personOn := false
	if n := len(b.person); n > 0 && b.person[n-1].on {
		out.Enabled, out.Origin, personOn = true, b.person[n-1].o, true
	}
	if n := len(b.projectOn); n > 0 && !out.Enabled {
		out.Enabled, out.Origin = true, b.projectOn[n-1]
	}
	out.FailIfUnavailable = len(b.failIfOff) > 0
	out.AllowUnsandboxed = len(b.noUnsandboxed) == 0
	// Running without asking is the person's when the sandbox is theirs;
	// with a sandbox only a repository turned on it is the repository's
	// (trusted) word alone: the person's auto-allow was given for their
	// own sandbox (the sandbox review).
	switch {
	case len(b.autoOff) > 0:
	case personOn:
		out.AutoAllow = len(b.autoOn) > 0 || len(b.autoOnTheirs) > 0
	default:
		out.AutoAllow = len(b.autoOnTheirs) > 0
	}
	out.Excluded = b.excluded.items
	out.AllowWrite, out.DenyWrite = b.allowWrite.items, b.denyWrite.items
	out.AllowRead, out.DenyRead = b.allowRead.items, b.denyRead.items
	out.AllowedDomains, out.DeniedDomains = b.allowedDomains.items, b.deniedDomains.items
	out.Strict = len(b.strict) > 0
	out.HiddenFiles, out.HiddenVars = b.hiddenFiles.items, b.hiddenVars.items
	for _, key := range Loosenings {
		if on := b.looseOn[key]; len(on) > 0 && len(b.looseOff[key]) == 0 {
			if out.Loosened == nil {
				out.Loosened = map[string]Origin{}
			}
			out.Loosened[key] = on[len(on)-1]
		}
	}
	out.UnixSockets, out.MachLookup = b.unixSockets.items, b.machLookup.items
	out.IgnoreViolations = b.ignored
	out.Bwrap, out.BwrapOrigin = b.bwrap, b.bwrapOrigin
	return out
}

// sandboxNetwork reads "sandbox.network": the hosts a sandboxed command
// may reach through gnosh's proxy, and those it may not. The person is
// asked about a host the lists do not name, unless strictAllowlist (from
// the person's own files) refuses it.
func (s *source) sandboxNetwork(f jfield) {
	if f.val.kind != jObject {
		s.errorf(f.line, "\"sandbox.network\" is an object (allowedDomains, deniedDomains), and here it is %s.", f.val.kind.word())
		return
	}
	for _, m := range s.unique(f.val, "sandbox.network.") {
		o := s.at(m.line)
		switch m.key {
		case "allowedDomains", "deniedDomains":
			m.key = "network." + m.key
			items, ok := s.sandboxList(m, "hosts: a name, *.name for the names below it, or an address, each with :port for that port alone")
			if !ok || len(items) == 0 {
				continue
			}
			kind, list := KindAddress, func(b *builder) *allowList { return &b.sandbox.allowedDomains }
			if m.key == "network.deniedDomains" {
				kind, list = KindRestriction, func(b *builder) *allowList { return &b.sandbox.deniedDomains }
			}
			s.add(op{kind: kind, text: "sandbox." + m.key + ": " + strings.Join(items, ", "), origin: o, what: "a sandbox setting",
				apply: func(b *builder) { list(b).add(items, o) }})
		case "strictAllowlist":
			switch {
			case m.val.kind != jBool:
				s.errorf(m.line, "\"sandbox.network.strictAllowlist\" is true or false, and here it is %s.", m.val.kind.word())
			case s.theirs || s.scope == "project":
				s.probs.warnf(s.file, m.line, "\"sandbox.network.strictAllowlist\" in a project's file is not taken: as in Claude Code, a repository can turn it neither on nor off. It is left out.")
			case m.val.b:
				s.add(op{kind: KindRestriction, text: "sandbox.network.strictAllowlist: true", origin: o, what: "a sandbox setting",
					apply: func(b *builder) { b.sandbox.strict = append(b.sandbox.strict, o) }})
			}
		case "allowAllUnixSockets", "allowLocalBinding":
			s.sandboxLoose("network."+m.key, m, false)
		case "allowUnixSockets", "allowMachLookup":
			s.sandboxNetList(m)
		case "tlsTerminate":
			s.probs.warnf(s.file, m.line, "\"sandbox.network.tlsTerminate\" is left out: gnosh's proxy does not read HTTPS, and has no certificate authority to read it with.")
		case "allowManagedDomainsOnly":
			s.errorf(m.line, "\"sandbox.network.allowManagedDomainsOnly\" is a setting of Claude Code's managed settings, which gnosh does not have. Take it out of this file.")
		case "httpProxyPort", "socksProxyPort":
			s.errorf(m.line, "\"sandbox.network.%s\" is a setting Claude Code has and gnosh has not built. Take it out of this file.", m.key)
		default:
			s.errorf(m.line, "%s is not a key gnosh reads under \"sandbox.network\". The keys are allowedDomains, deniedDomains, strictAllowlist, allowUnixSockets, allowAllUnixSockets, allowLocalBinding, allowMachLookup and tlsTerminate.", showName(m.key))
		}
	}
}

// sandboxNetList reads network.allowUnixSockets (the Unix sockets a
// sandboxed command may connect to on macOS: paths, in the spelling of
// the filesystem lists) and network.allowMachLookup (macOS's services it
// may reach: a name, a name ending in *, or "*"). Either gives a power,
// and waits for the trust.
func (s *source) sandboxNetList(m jfield) {
	o := s.at(m.line)
	sockets := m.key == "allowUnixSockets"
	what := "Unix sockets' paths"
	if !sockets {
		what = "macOS services' names (a name, a name ending in *, or *)"
	}
	m.key = "network." + m.key
	items, ok := s.sandboxList(m, what)
	if !ok || len(items) == 0 {
		return
	}
	list := func(b *builder) *allowList { return &b.sandbox.machLookup }
	if sockets {
		base := s.baseDir
		if (s.scope == "project" || s.scope == "local") && s.ruleDir != "" {
			base = s.ruleDir
		}
		if s.scope == "command line" {
			base = s.cwd
		}
		for i, it := range items {
			if strings.HasPrefix(it, "//") {
				it = it[1:]
			}
			items[i] = absPath(it, s.home, base)
		}
		list = func(b *builder) *allowList { return &b.sandbox.unixSockets }
	} else {
		for _, it := range items {
			if strings.Contains(strings.TrimSuffix(it, "*"), "*") {
				s.errorf(m.line, "%s in \"sandbox.network.allowMachLookup\" has a * other than at its end: a name, a name ending in * for those it begins, or * alone.", showName(it))
				return
			}
		}
	}
	s.add(op{kind: KindSandboxWider, text: "sandbox." + m.key + ": " + strings.Join(items, ", "), origin: o, what: "a sandbox setting",
		apply: func(b *builder) { list(b).add(items, o) }})
}

// sandboxCredentials reads "sandbox.credentials": credential files a
// sandboxed command may not read (as denyRead hides them) and variables it
// is not given. Claude Code's "mask" (a stand-in value, the real one put
// in by its proxy on the way out) is not built: such an entry is hidden as
// "deny" is, which keeps the credential from the command all the same, and
// the person is told. Either only takes a power away, so a project's
// counts before trust.
func (s *source) sandboxCredentials(f jfield) {
	if f.val.kind != jObject {
		s.errorf(f.line, "\"sandbox.credentials\" is an object (files, envVars), and here it is %s.", f.val.kind.word())
		return
	}
	base := s.baseDir
	if (s.scope == "project" || s.scope == "local") && s.ruleDir != "" {
		base = s.ruleDir
	}
	if s.scope == "command line" {
		base = s.cwd
	}
	for _, m := range s.unique(f.val, "sandbox.credentials.") {
		o := s.at(m.line)
		files := m.key == "files"
		switch m.key {
		case "files", "envVars":
		case "allowPlaintextInject", "awsPairs", "sigv4":
			s.errorf(m.line, "\"sandbox.credentials.%s\" is a setting Claude Code has and gnosh has not built (its proxy puts in no credential). Take it out of this file.", m.key)
			continue
		default:
			s.errorf(m.line, "%s is not a key gnosh reads under \"sandbox.credentials\". The keys are files and envVars.", showName(m.key))
			continue
		}
		if m.val.kind != jArray {
			s.errorf(m.line, "\"sandbox.credentials.%s\" is a list of entries, each an object, and here it is %s.", m.key, m.val.kind.word())
			continue
		}
		var items []string
		for _, it := range m.val.items {
			if it.kind != jObject {
				s.errorf(it.line, "an entry of \"sandbox.credentials.%s\" is an object, and here it is %s.", m.key, it.kind.word())
				continue
			}
			var name, mode string
			for _, fl := range s.unique(it, "sandbox.credentials."+m.key+".") {
				switch fl.key {
				case "path", "name", "mode":
					if fl.val.kind != jString {
						s.errorf(fl.line, "%q of a credentials entry is a string, and here it is %s.", fl.key, fl.val.kind.word())
						continue
					}
					if fl.key == "mode" {
						mode = fl.val.str
					} else {
						name = strings.TrimSpace(fl.val.str)
					}
				case "extract", "onExtractNoMatch", "decode", "maskClaims", "maskDuplicates", "injectHosts":
					// (A mask entry's own fields: taken, as the entry is hidden.)
				default:
					s.errorf(fl.line, "%s is not a field of a credentials entry.", showName(fl.key))
				}
			}
			want := "path"
			if !files {
				want = "name"
			}
			switch {
			case name == "":
				s.errorf(it.line, "an entry of \"sandbox.credentials.%s\" has no %q.", m.key, want)
				continue
			case mode != "deny" && mode != "mask":
				s.errorf(it.line, "the \"mode\" of a credentials entry is \"deny\" or \"mask\", and here it is %s.", showName(mode))
				continue
			case !files && !validVarName(name):
				s.errorf(it.line, "%s is not a variable's name: one starts with a letter or an underscore, and holds letters, digits and underscores.", showName(name))
				continue
			case !files && (name == "PATH" || name == "HOME" || name == "PWD"):
				// (No command runs without them, and a repository's file is
				// taken before trust: hiding one would only make every
				// command fail, the model then asking to leave the sandbox.)
				s.probs.warnf(s.file, it.line, "%s is not hidden from sandboxed commands: they cannot run without it. It is left out.", name)
				continue
			}
			if mode == "mask" {
				s.probs.warnf(s.file, it.line, "\"mode\": \"mask\" is not built in gnosh (its proxy puts in no credential), so %s is hidden from sandboxed commands, as \"deny\" hides it.", name)
			}
			if files {
				if strings.HasPrefix(name, "//") {
					name = name[1:]
				}
				name = absPath(strings.TrimSuffix(name, "/**"), s.home, base)
			}
			items = append(items, name)
		}
		if len(items) == 0 {
			continue
		}
		list := func(b *builder) *allowList { return &b.sandbox.hiddenVars }
		if files {
			list = func(b *builder) *allowList { return &b.sandbox.hiddenFiles }
		}
		s.add(op{kind: KindRestriction, text: "sandbox.credentials." + m.key + ": " + strings.Join(items, ", "), origin: o, what: "a sandbox setting",
			apply: func(b *builder) { list(b).add(items, o) }})
	}
}

// validVarName reports whether s can be an environment variable's name.
func validVarName(s string) bool {
	for i, r := range s {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return s != ""
}
