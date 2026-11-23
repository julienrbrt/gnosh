package instructions

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The limits a zero Options gets.
const (
	// DefaultMaxDepth is how many imports may follow one another: a file
	// may import a file that imports a file, four hops deep.
	DefaultMaxDepth = 4
	// DefaultMaxBytes is the largest file that is read: 4 MiB.
	DefaultMaxBytes = 4 << 20
	// DefaultLateMaxChars is how much of a file that arrives during the
	// session is given to the model: its first 40,000 characters, which is
	// about a twentieth of a 200,000-token context window.
	DefaultLateMaxChars = 40_000
	// DefaultRulesDir is the name of the rules directory inside a dot
	// directory.
	DefaultRulesDir = "rules"
)

// FallbackPolicy says when the files named in Options.FallbackNames are read.
type FallbackPolicy int

const (
	// FallbackInstead reads the fallback names only when the project keeps
	// no file under its main or local names: none in the working directory
	// and none in any directory above it. If there is one anywhere on that
	// path, no fallback file is read at all. Below the working directory
	// the question is asked of each directory by itself. This is the
	// default.
	FallbackInstead FallbackPolicy = iota
	// FallbackAlso reads the fallback names as well as the main ones, after
	// them in each directory.
	FallbackAlso
	// FallbackNever never reads the fallback names.
	FallbackNever
)

// Options says where instruction files are looked for and how far loading
// may go. Only Cwd is required. The zero value of every other field is the
// careful one: nothing is read from the project until Trusted is set, and no
// import leaves the project until ApproveImport says it may.
//
// Paths are absolute, in the host's form. Names are file names (or relative
// paths) looked for inside each directory.
type Options struct {
	// Cwd is the directory the session works in.
	Cwd string
	// ProjectRoot is the top directory of the project, usually the
	// repository's. It must be Cwd or a directory above it; empty means
	// Cwd. A project's file may import only files inside it without asking,
	// and directories above it are held to the ownership test.
	ProjectRoot string
	// Home is the person's home directory, which "~/" in an import stands
	// for. Empty means such imports are reported as missing.
	Home string

	// UserFiles are the person's own instruction files and folders, in the
	// order they are read. A folder gives every ".md" file under it, in
	// name order, each read as a rule: one that lists path patterns applies
	// to matching files under Cwd. A path with nothing at it is passed over
	// in silence.
	UserFiles []string

	// ProjectNames are the names a project's instruction file may have,
	// for example "GNOSH.md" and "CLAUDE.md". Every one that is there is
	// read, in this order, in each directory.
	ProjectNames []string
	// LocalNames are the names of the person's private file about a
	// project, for example "CLAUDE.local.md". They are read last in each
	// directory.
	LocalNames []string
	// FallbackNames are names read according to Fallback, for example
	// "AGENTS.md".
	FallbackNames []string
	// Fallback says when FallbackNames are read.
	Fallback FallbackPolicy
	// DotDirs are sub-directories of a project directory that may also hold
	// a file under the project and fallback names, and a rules directory:
	// for example ".gnosh" and ".claude".
	DotDirs []string
	// RulesDir is the name of the rules directory inside a dot directory.
	// Empty means DefaultRulesDir.
	RulesDir string

	// ProjectFiles are more instruction files of the project, beyond those
	// found by name: absolute paths of files, or of folders whose ".md"
	// files are all read as rules, with patterns relative to the project
	// root. They are read after the directories, in the order given, and
	// like them only when Trusted is set. They are read wherever they lie,
	// since whoever set the option named them; what they import is held to
	// the project like any other import.
	ProjectFiles []string

	// Exclude lists files that are never loaded, by any route: path
	// patterns of the kind rule files use, matched against absolute paths.
	// "/work/vendor" leaves out everything under that directory;
	// "CLAUDE.local.md" leaves out every file of that name.
	Exclude []string

	// Trusted says whether the folder's own files may be read. When it is
	// false, only UserFiles and what they import are loaded: nothing is
	// looked for in the working directory, above it or below it. What
	// trust means for instruction files is the caller's to decide: with
	// Trusted set and ApproveImport nil, a folder's files are read and can
	// bring in nothing from outside it.
	Trusted bool
	// ApproveImport is asked before a project's file brings in a file from
	// outside the project root, by an import or through a symbolic link.
	// from is the file that asks and target is the file that would be
	// read, with links followed. It is asked once for each target. Nil
	// refuses them all. It is called while the Set is locked, so it must
	// not call the Set's methods.
	ApproveImport func(from, target string) bool
	// Owned reports whether a file, directory or link belongs to the person
	// or to the system. In a directory above the project root a file is
	// read only if it passes and so does everything between that directory
	// and it, so that a directory shared with other accounts cannot hand
	// the model instructions. Nil means the host's test: the owner is the
	// account gnosh runs as, or root.
	Owned func(info fs.FileInfo) bool
	// Trees is a directory below the working directory whose folders are
	// work trees of the project (gnosh's own, for sub-agents that work in
	// one): the files at the top of each, and in Trees itself, are copies
	// of the project's own and are not read again as nested files, as
	// Claude Code does not read a work tree's top CLAUDE.md or rules for a
	// sub-agent working there. What lies deeper in a tree is read as
	// usual. Empty means none.
	Trees string

	// MaxDepth is how many imports may follow one another. Zero means
	// DefaultMaxDepth; a negative number turns imports off.
	MaxDepth int
	// MaxBytes is the largest file that is read. A bigger one is skipped
	// and reported. Zero or less means DefaultMaxBytes.
	MaxBytes int64
	// LateMaxChars is how many characters of a file that arrives during
	// the session are given to the model; the rest is cut and the model is
	// told to read the file. Zero means DefaultLateMaxChars; a negative
	// number means no cut.
	LateMaxChars int

	// FS is the file system, rooted where absolute paths start: the name
	// it is asked for is the path without its leading slash. Nil means the
	// real one. A file system that implements fs.ReadLinkFS has its
	// symbolic links followed.
	FS fs.FS
}

// Scope says whose a file is. Its values are the memory_type values of the
// InstructionsLoaded hook.
type Scope string

const (
	// ScopeUser is the person's own: it applies in every project.
	ScopeUser Scope = "User"
	// ScopeProject belongs to the project and is shared with everyone who
	// works on it.
	ScopeProject Scope = "Project"
	// ScopeLocal is the person's private file about one project.
	ScopeLocal Scope = "Local"
)

// Kind says what sort of file one is, by how it was found.
type Kind string

const (
	// KindMain is a file found under one of the configured names, or
	// listed by its own path.
	KindMain Kind = "main"
	// KindFallback is a file found under a fallback name.
	KindFallback Kind = "fallback"
	// KindRule is a file found in a rules directory or in a listed folder.
	KindRule Kind = "rule"
	// KindImport is a file another file brought in with "@path".
	KindImport Kind = "import"
)

// Reason says why a file was loaded. Its values are the load_reason values
// of the InstructionsLoaded hook.
type Reason string

const (
	// ReasonStart: the file is in effect from the start of the session.
	ReasonStart Reason = "session_start"
	// ReasonNested: the model touched a file in the file's directory, which
	// lies below the working directory.
	ReasonNested Reason = "nested_traversal"
	// ReasonRuleMatch: the model touched a file that one of the rule's path
	// patterns matches.
	ReasonRuleMatch Reason = "path_glob_match"
	// ReasonImport: another file imported it.
	ReasonImport Reason = "include"
)

// File is one loaded instruction file.
type File struct {
	// Path is where the file was found.
	Path string
	// RealPath is Path with every symbolic link followed. A file is loaded
	// once however many paths lead to it.
	RealPath string
	// Scope says whose the file is. An imported file has the scope of the
	// file that imported it.
	Scope Scope
	// Kind says how the file was found.
	Kind Kind
	// Reason says why it was loaded.
	Reason Reason
	// Late reports that the file arrived during the session, through
	// ForPath, and so is not part of what Render returns.
	Late bool
	// Parent is the file that imported this one. Empty unless Kind is
	// KindImport.
	Parent string
	// Depth is how many imports lead to the file: 0 for a file that was
	// found by name, 1 for a file it imports, and so on.
	Depth int
	// Base is the directory the file speaks for: the directory it was found
	// in, or for a file inside a dot directory the directory that holds the
	// dot directory. A rule's path patterns are relative to it: for a rule
	// from one of the person's folders it is the working directory, and
	// for one from a listed project folder the project root. Empty for a
	// file listed by its own path and for an import.
	Base string
	// Trigger is the file the model touched that brought a late file in.
	Trigger string
	// Globs are the path patterns of a rule that has them, as written.
	Globs []string
	// Size is the file's size on disk, in bytes.
	Size int64
	// ModTime is when the file was last changed.
	ModTime time.Time
	// Text is what the model is given: the file's content as UTF-8 with
	// "\n" line ends, without a rule's frontmatter and without whole-line
	// HTML comments.
	Text string
	// Truncated reports that Text is only the start of the file, because a
	// late file was longer than Options.LateMaxChars.
	Truncated bool
}

// WarningKind sorts warnings, so that a caller can decide which to show.
type WarningKind string

const (
	// WarnTooBig: the file is over Options.MaxBytes and was skipped.
	WarnTooBig WarningKind = "too_big"
	// WarnUnreadable: the file is there but could not be read.
	WarnUnreadable WarningKind = "unreadable"
	// WarnNotFile: the path leads to a directory or something else that is
	// not an ordinary file.
	WarnNotFile WarningKind = "not_file"
	// WarnNotText: the file holds a zero byte, so it is not text, and was
	// skipped.
	WarnNotText WarningKind = "not_text"
	// WarnInvalidUTF8: the file was loaded with its bad bytes replaced.
	WarnInvalidUTF8 WarningKind = "invalid_utf8"
	// WarnNotOwned: a file above the project root belongs to someone else
	// and was skipped.
	WarnNotOwned WarningKind = "not_owned"
	// WarnImportMissing: an imported file is not there.
	WarnImportMissing WarningKind = "import_missing"
	// WarnImportRefused: a file outside the project root was wanted and
	// not approved.
	WarnImportRefused WarningKind = "import_refused"
	// WarnImportDepth: an import lay deeper than Options.MaxDepth and was
	// not followed.
	WarnImportDepth WarningKind = "import_depth"
	// WarnFrontmatter: a rule's frontmatter was not understood; the rule
	// was loaded whole, for every file.
	WarnFrontmatter WarningKind = "frontmatter"
	// WarnPattern: one of a rule's path patterns cannot be used and
	// matches nothing.
	WarnPattern WarningKind = "pattern"
	// WarnCut: a late file was longer than Options.LateMaxChars and the
	// model was given only its start.
	WarnCut WarningKind = "cut"
	// WarnTooMany: a file imports more than importsMost files, or one
	// reading found more than rulesMost rules; the rest were left out.
	WarnTooMany WarningKind = "too_many"
	// WarnInvisible: a file held characters drawn as nothing that can hide
	// words or change their order; they were taken out.
	WarnInvisible WarningKind = "invisible"
)

// importsMost is how many imports of one file are followed, and rulesMost
// how many rule files one reading takes: a repository's AGENTS.md of half
// a million "@x/1 @x/2 …" cost as many lookups and warnings at each start,
// and a rules folder linked to the top of the repository made every .md
// in it a rule (the instruction-files review of 2026-11-20).
const (
	importsMost = 100
	rulesMost   = 200
)

// Warning is something that went wrong with one file. Loading goes on past
// it; nothing is printed.
type Warning struct {
	// Kind sorts the warning.
	Kind WarningKind
	// Path is the file the warning is about.
	Path string
	// From is the file that asked for it, when one did.
	From string
	// Text says what happened in plain words, as a full sentence.
	Text string
}

// String returns the warning's sentence.
func (w Warning) String() string { return w.Text }

// Set is the instruction files of one session: those in effect from the
// start, and those that arrive as the model touches files. It is safe for
// use by several goroutines.
type Set struct {
	mu sync.Mutex

	o     Options // as given, with the defaults filled in
	d     disk
	owned func(fs.FileInfo) bool

	// These are cleaned and slash-separated. The real ones have their links
	// followed.
	cwd, root, home   string
	realCwd, realRoot string
	// realHome is the home directory with its links followed, and secret
	// the places in it where the person keeps keys (secretNames).
	realHome string
	secret   []string
	// realTrees is Options.Trees with its links followed, or "".
	realTrees    string
	userFiles    []string
	projectFiles []string
	exclude      []pattern

	files    []File
	warnings []Warning
	warned   map[Warning]bool

	// loaded holds the real path of every loaded file. For a late file the
	// value is the real path of the file whose arrival brought it in (its
	// own, unless it was imported); for the others it is empty.
	loaded map[string]string
	// approved remembers what ApproveImport said about each target, and
	// unapproved lists those it did not let in, in the order they came.
	approved   map[string]bool
	unapproved []string
	// rules are the rules that wait for a matching path, in the order they
	// were found; ruleAt finds one by its real path.
	rules  []*pathRule
	ruleAt map[string]*pathRule
	// walked holds the real paths of the directories below the working
	// directory that have been searched.
	walked map[string]bool
	// stamps holds what every path the loader looked at was like, so that
	// Changed can tell when a reload would read something else.
	stamps map[string]stamp
	// fallback reports that, under FallbackInstead, no main file was found
	// on the way down to the working directory.
	fallback bool
}

// Load reads the instruction files that are in effect from the start of a
// session: the person's own files and folders, then, if the folder is
// trusted, every directory from the top of the file system down to the
// working directory, and last the project's extra files. Trouble with one
// file is a Warning and loading goes on; the error is for options that make
// no sense and for a cancelled context.
func Load(ctx context.Context, o Options) (*Set, error) {
	s, err := newSet(o)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	l := s.begin(ctx)
	user := want{scope: ScopeUser, kind: KindMain, reason: ReasonStart, free: true}
	for _, p := range s.userFiles {
		l.listed(user, p, s.cwd)
	}
	if s.o.Trusted {
		chain := ancestors(s.cwd)
		if s.o.Fallback == FallbackInstead && len(s.o.FallbackNames) > 0 {
			s.fallback = true
			for _, dir := range chain {
				if l.hasMain(dir) {
					s.fallback = false
					break
				}
			}
		}
		for _, dir := range chain {
			l.dir(dir, false, "")
		}
		extra := want{scope: ScopeProject, kind: KindMain, reason: ReasonStart, free: true}
		for _, p := range s.projectFiles {
			l.listed(extra, p, s.root)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("instructions: loading stopped before it finished: %w", err)
	}
	return s, nil
}

// newSet checks the options, fills in their defaults and makes an empty Set.
func newSet(o Options) (*Set, error) {
	s := &Set{
		warned:   map[Warning]bool{},
		loaded:   map[string]string{},
		approved: map[string]bool{},
		ruleAt:   map[string]*pathRule{},
		walked:   map[string]bool{},
		stamps:   map[string]stamp{},
	}
	var err error
	if s.cwd, err = absPath("the working directory", o.Cwd); err != nil {
		return nil, err
	}
	s.root = s.cwd
	if o.ProjectRoot != "" {
		if s.root, err = absPath("the project root", o.ProjectRoot); err != nil {
			return nil, err
		}
	}
	if o.Home != "" {
		if s.home, err = absPath("the home directory", o.Home); err != nil {
			return nil, err
		}
	}
	for _, p := range o.UserFiles {
		p, err := absPath("a user file", p)
		if err != nil {
			return nil, err
		}
		s.userFiles = append(s.userFiles, p)
	}
	for _, p := range o.ProjectFiles {
		p, err := absPath("a project file", p)
		if err != nil {
			return nil, err
		}
		s.projectFiles = append(s.projectFiles, p)
	}

	if o.ProjectNames, err = cleanNames("ProjectNames", o.ProjectNames); err != nil {
		return nil, err
	}
	if o.LocalNames, err = cleanNames("LocalNames", o.LocalNames); err != nil {
		return nil, err
	}
	if o.FallbackNames, err = cleanNames("FallbackNames", o.FallbackNames); err != nil {
		return nil, err
	}
	if o.DotDirs, err = cleanNames("DotDirs", o.DotDirs); err != nil {
		return nil, err
	}
	if o.RulesDir == "" {
		o.RulesDir = DefaultRulesDir
	}
	rulesDir, err := cleanNames("RulesDir", []string{o.RulesDir})
	if err != nil {
		return nil, err
	}
	o.RulesDir = rulesDir[0]

	switch o.Fallback {
	case FallbackInstead, FallbackAlso, FallbackNever:
	default:
		return nil, fmt.Errorf("instructions: Fallback is %d, which is not a policy", o.Fallback)
	}
	if o.MaxDepth == 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.LateMaxChars == 0 {
		o.LateMaxChars = DefaultLateMaxChars
	}
	for _, src := range o.Exclude {
		pats, problems := compilePatterns([]string{filepath.ToSlash(src)})
		if len(problems) > 0 {
			return nil, fmt.Errorf("instructions: the exclude pattern %q cannot be used: %s", src, problems[0].why)
		}
		s.exclude = append(s.exclude, pats...)
	}
	if o.FS == nil {
		o.FS = os.DirFS("/")
	}
	s.owned = o.Owned
	if s.owned == nil {
		s.owned = hostOwner()
	}
	s.o = o
	s.d = disk{fsys: o.FS, mine: append([]string{s.cwd, s.root, s.home}, s.userFiles...)}

	s.realCwd = s.d.resolveLoose(s.cwd)
	s.realRoot = s.d.resolveLoose(s.root)
	if o.Trees != "" {
		trees, err := absPath("the work trees' folder", o.Trees)
		if err != nil {
			return nil, err
		}
		s.realTrees = s.d.resolveLoose(trees)
	}
	if s.home != "" {
		s.realHome = s.d.resolveLoose(s.home)
		for _, name := range secretNames {
			s.secret = append(s.secret, path.Join(s.realHome, name))
		}
	}
	if !within(s.realCwd, s.realRoot) {
		return nil, fmt.Errorf("instructions: the working directory %s is not inside the project root %s", osPath(s.cwd), osPath(s.root))
	}
	return s, nil
}

// Files returns every file loaded so far, in the order they were loaded:
// those from the start of the session first, then the late ones as they
// arrived. A late file that was forgotten is not in the list.
func (s *Set) Files() []File {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneFiles(s.files)
}

// Warnings returns every warning so far, in the order they arose. The same
// trouble with the same file is reported once.
func (s *Set) Warnings() []Warning {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.warnings)
}

// ForPath is told that the model has just read, written or edited the file
// at p (or moved into the directory at p), and returns the instruction files
// that now apply and have not been given yet, with any warnings their
// loading raised. Those are, in order: for each directory between the
// working directory and the file, what starting the session there would
// have added; then every rule whose path patterns match the file. A file is
// returned once in a session, unless Forget is told that it left the
// conversation.
//
// A relative p is taken from the working directory. If ctx is cancelled
// part-way, what was not reached stays undelivered and comes with a later
// call.
//
// What counts as touching a file is the caller's to say, and so is where
// trust ends: for a file in a part of the tree the person has not trusted
// (a repository of its own below the working directory, say), not calling
// ForPath is what keeps that part's instruction files unread.
func (s *Set) ForPath(ctx context.Context, p string) ([]File, []Warning) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = s.cwd + "/" + p
	}
	p = path.Clean(p)
	real := s.d.resolveLoose(p)
	isDir := false
	if info, err := s.d.stat(real); err == nil {
		isDir = info.IsDir()
	}

	l := s.begin(ctx)
	if s.o.Trusted && real != s.realCwd && within(real, s.realCwd) {
		parts := strings.Split(relTo(real, s.realCwd), "/")
		if !isDir {
			parts = parts[:len(parts)-1]
		}
		shown, at := s.cwd, s.realCwd
		for _, part := range parts {
			shown, at = path.Join(shown, part), path.Join(at, part)
			if s.walked[at] {
				continue
			}
			if s.realTrees != "" && (at == s.realTrees || path.Dir(at) == s.realTrees) {
				// A work tree's top holds the project's own files again.
				s.walked[at] = true
				continue
			}
			l.dir(shown, true, p)
			if ctx.Err() != nil {
				break
			}
			s.walked[at] = true
		}
	}
	if !isDir {
		for _, r := range s.rules {
			if ctx.Err() != nil {
				break
			}
			if !r.done && r.matches(real) {
				l.deliver(r, p)
			}
		}
	}
	return cloneFiles(l.files), l.warnings
}

// Forget is told that late files have left the conversation (it was
// rewound, or summarised), so that ForPath reads and gives them again the
// next time a file they apply to is touched. A file leaves together with
// everything that arrived with it: the file that imported it and the files
// it imported. With no paths, every late file is forgotten. Files in effect
// from the start are never forgotten; to read those again, Load again.
func (s *Set) Forget(paths ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	units := map[string]bool{}
	named := map[string]bool{}
	for _, p := range paths {
		named[path.Clean(filepath.ToSlash(p))] = true
	}
	for _, f := range s.files {
		if !f.Late {
			continue
		}
		if len(paths) == 0 || named[filepath.ToSlash(f.Path)] || named[filepath.ToSlash(f.RealPath)] {
			units[s.loaded[filepath.ToSlash(f.RealPath)]] = true
		}
	}
	if len(units) == 0 {
		return
	}
	s.files = slices.DeleteFunc(s.files, func(f File) bool {
		real := filepath.ToSlash(f.RealPath)
		if !f.Late || !units[s.loaded[real]] {
			return false
		}
		delete(s.loaded, real)
		return true
	})
	for _, r := range s.rules {
		if units[r.real] {
			r.done = false
		}
	}
	// Searching a directory again costs a few lookups and loads only what
	// is no longer held, so it is simplest to forget all the searches.
	clear(s.walked)
}

// Reload reads again what Load read, in place of what the Set holds: the
// files in effect from the start as they are on disk now, and none of the
// late ones, which are given again as the model works where they apply.
// It returns the warnings of the new reading that the old one did not
// have. Claude Code reads its instruction files again after a summary;
// without this an edit, a file /init wrote or a rule taken out never
// reached a running session, and a summary gave back the copy read at
// the start (the instruction-files review of 2026-11-20).
func (s *Set) Reload(ctx context.Context) ([]Warning, error) {
	n, err := Load(ctx, s.o)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var fresh []Warning
	for _, w := range n.warnings {
		if !s.warned[w] {
			fresh = append(fresh, w)
		}
	}
	s.files, s.warnings, s.warned = n.files, n.warnings, n.warned
	s.loaded, s.approved, s.unapproved = n.loaded, n.approved, n.unapproved
	s.rules, s.ruleAt, s.walked = n.rules, n.ruleAt, n.walked
	s.stamps, s.fallback = n.stamps, n.fallback
	return fresh, nil
}

// Unapproved returns the files outside the project that a project's file
// would have brought in, by an import or through a link, and that were
// left out for want of approval (Options.ApproveImport), with their links
// followed; never the places keys are kept, which are left out whatever
// is approved. Claude Code asks the person once per project whether to
// load such files.
func (s *Set) Unapproved() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.unapproved))
	for i, p := range s.unapproved {
		out[i] = osPath(p)
	}
	return out
}

// Changed returns the paths that are no longer as they were when the loader
// looked at them: a loaded file that was edited or removed, a file that has
// appeared under one of the names, a rules directory that gained or lost a
// file. A file is compared by size and modification time. An empty result
// means that loading again would read the same files.
func (s *Set) Changed(ctx context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := newLookups(s.d)
	var changed []string
	for _, p := range slices.Sorted(maps.Keys(s.stamps)) {
		if ctx.Err() != nil {
			break
		}
		if !k.stamp(p).same(s.stamps[p]) {
			changed = append(changed, osPath(p))
		}
	}
	return changed
}

// cloneFiles copies a list of files so that the caller may keep and change
// it.
func cloneFiles(files []File) []File {
	out := slices.Clone(files)
	for i := range out {
		out[i].Globs = slices.Clone(out[i].Globs)
	}
	return out
}

// cleanNames checks that each name is a relative path that stays inside the
// directory it is looked for in, and returns the names cleaned and
// slash-separated. what names the option, for the error.
func cleanNames(what string, names []string) ([]string, error) {
	clean := make([]string, 0, len(names))
	for _, name := range names {
		n := path.Clean(filepath.ToSlash(name))
		if name == "" || n == "." || n == ".." || strings.HasPrefix(n, "/") || strings.HasPrefix(n, "../") {
			return nil, fmt.Errorf("instructions: %s holds %q, which is not a name inside a directory", what, name)
		}
		clean = append(clean, n)
	}
	return clean, nil
}

// absPath checks that p is an absolute path and returns it cleaned and
// slash-separated. what names the option, for the error.
func absPath(what, p string) (string, error) {
	slashed := filepath.ToSlash(p)
	if !strings.HasPrefix(slashed, "/") {
		return "", fmt.Errorf("instructions: %s must be an absolute path, and %q is not", what, p)
	}
	return path.Clean(slashed), nil
}

// osPath turns one of the package's slash-separated paths into the host's
// form.
func osPath(p string) string { return filepath.FromSlash(p) }

// secretNames are the places under the home directory where keys and
// passwords are kept: ssh's and gpg's keys, cloud, GitHub, container and
// cluster credentials, and the passwords of .netrc and git's store.
var secretNames = []string{".ssh", ".gnupg", ".aws", ".config/gh", ".netrc", ".git-credentials", ".docker", ".kube"}

// secretPath reports whether real, a link-free path, lies in one of the
// places the person keeps keys in.
func (s *Set) secretPath(real string) bool {
	for _, d := range s.secret {
		if within(real, d) {
			return true
		}
	}
	return false
}

// wide reports whether d, a link-free directory, is the home directory, a
// directory that holds it, or the top of the disk.
func (s *Set) wide(d string) bool {
	return d == "/" || (s.realHome != "" && within(s.realHome, d))
}

// within reports whether p is dir or lies under it. Both are cleaned,
// absolute, slash-separated paths.
func within(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// relTo returns p relative to dir, which it lies under.
func relTo(p, dir string) string {
	if dir == "/" {
		return strings.TrimPrefix(p, "/")
	}
	return strings.TrimPrefix(p[len(dir):], "/")
}

// ancestors returns dir and every directory above it, outermost first.
func ancestors(dir string) []string {
	chain := []string{dir}
	for dir != "/" {
		dir = path.Dir(dir)
		chain = append(chain, dir)
	}
	slices.Reverse(chain)
	return chain
}
