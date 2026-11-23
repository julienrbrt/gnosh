package instructions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/text"
)

// loading is one pass of reading files into a Set: the start of the session,
// or one touched path. It keeps what the pass added, so that the caller can
// be told, and what the pass has already looked up on disk.
type loading struct {
	s        *Set
	ctx      context.Context
	k        *lookups
	files    []File
	warnings []Warning
	// scanned holds the real paths of the rules directories this pass has
	// read, so that a link that leads back into one is not followed round.
	scanned map[string]bool
	// rules counts the rule files this pass has taken (rulesMost).
	rules int
}

// begin starts a pass. The Set must be locked.
func (s *Set) begin(ctx context.Context) *loading {
	return &loading{s: s, ctx: ctx, k: newLookups(s.d), scanned: map[string]bool{}}
}

// want describes a file the loader is about to try.
type want struct {
	path    string // where it is looked for
	scope   Scope
	kind    Kind
	reason  Reason
	late    bool
	base    string // the directory the file speaks for
	trigger string // the touched file behind a late load
	parent  string // the importing file, for an import
	written string // an import as it was written
	depth   int    // how many imports lead here
	unit    string // the real path of the late file this one arrives with
	// above marks a file found by name in a directory above the project
	// root: it must pass the ownership test.
	above bool
	// free marks a file that may lie outside the project root without
	// anyone being asked: the person's own files and what they import,
	// files an option lists by path, and files found by name above the
	// root.
	free bool
	// under is a directory outside the project root that was approved as a
	// whole: what really lies inside it needs no further asking.
	under string
	// fence is the directory, link-free, that a project's file may bring
	// files in from without asking (fenceAt); "" means the project root.
	fence string
}

// outside reports whether the file or directory at real, a link-free path,
// lies somewhere that w may not read from without asking. Where the person
// keeps keys is such a place for a project's file wherever its fence is.
func (w want) outside(s *Set, real string) bool {
	if w.free {
		return false
	}
	if s.secretPath(real) {
		return true
	}
	fence := w.fence
	if fence == "" {
		fence = s.realRoot
	}
	if within(real, fence) {
		return false
	}
	return w.under == "" || !within(real, w.under)
}

// fenceAt is the directory, with its links followed, that a project's file
// found in dir may bring files in from without asking: the top of the
// repository it lies in below the project root, a repository cloned or
// vendored there being a project of its own; otherwise the project root;
// otherwise, where the root is the home directory, a directory that holds
// it or the top of the disk, dir itself. A session started in one of those
// made every key the person has, and every other project's files, a
// nested CLAUDE.md's to import (the instruction-files review of
// 2026-11-20).
func (l *loading) fenceAt(dir string) string {
	s := l.s
	real, err := l.k.realDir(dir)
	if err != nil {
		real = dir
	}
	for d := real; d != s.realRoot && within(d, s.realRoot) && !s.wide(d); d = path.Dir(d) {
		if l.repoTop(d) {
			return d
		}
	}
	if !s.wide(s.realRoot) {
		return s.realRoot
	}
	return real
}

// repoTop reports whether d, a link-free directory, is the top of a git
// work tree.
func (l *loading) repoTop(d string) bool {
	return l.k.probe(path.Join(d, ".git")).err == nil
}

// fetched is a file that was found, allowed and read.
type fetched struct {
	real string
	info fs.FileInfo
	text string
}

// pathRule is a rule with path patterns, put by until the model touches a
// file one of them matches. Its text is read again when that happens, as a
// nested file's is.
type pathRule struct {
	w        want
	real     string // the rule file, with links followed
	globs    []string
	pats     []pattern
	baseReal string
	done     bool // delivered, or loaded by another route
}

// matches reports whether one of the rule's patterns matches the file at
// real, a link-free path.
func (r *pathRule) matches(real string) bool {
	if real == r.baseReal || !within(real, r.baseReal) {
		return false
	}
	rel := relTo(real, r.baseReal)
	for _, p := range r.pats {
		if p.match(rel) {
			return true
		}
	}
	return false
}

// named tries the file at p, as described by t, and loads it if it is there.
func (l *loading) named(t want, p string) {
	t.path = p
	if got, ok := l.fetch(t); ok {
		l.add(t, got, got.text)
	}
}

// listed loads a path that an option names: a file, or a folder whose
// rules are all read, with path patterns relative to base.
func (l *loading) listed(t want, p, base string) {
	if pr := l.k.probe(p); pr.err == nil && pr.info.IsDir() {
		t.base = base
		l.rulesDir(p, t)
		return
	}
	l.named(t, p)
}

// dir loads what one directory holds for the model: its files under the
// project names (and in its dot directories), its fallback files when the
// policy calls for them, the rules without path patterns in its rules
// directories, and last the person's private files. Rules with path
// patterns are put by for ForPath. late and trigger say that the directory
// lies below the working directory and which touched file led to it.
func (l *loading) dir(dir string, late bool, trigger string) {
	s := l.s
	above := l.above(dir)
	t := want{scope: ScopeProject, kind: KindMain, reason: ReasonStart, base: dir, above: above, free: above}
	if !above {
		t.fence = l.fenceAt(dir)
	}
	if late {
		t.reason, t.late, t.trigger = ReasonNested, true, trigger
	}
	dots := l.dots(dir)
	for _, name := range s.o.ProjectNames {
		l.named(t, path.Join(dir, name))
		for _, dot := range dots {
			l.named(t, path.Join(dir, dot, name))
		}
	}
	if l.wantsFallback(dir) {
		ft := t
		ft.kind = KindFallback
		for _, name := range s.o.FallbackNames {
			l.named(ft, path.Join(dir, name))
			for _, dot := range dots {
				l.named(ft, path.Join(dir, dot, name))
			}
		}
	}
	for _, dot := range dots {
		l.rulesDir(path.Join(dir, dot, s.o.RulesDir), t)
	}
	lt := t
	lt.scope = ScopeLocal
	for _, name := range s.o.LocalNames {
		l.named(lt, path.Join(dir, name))
	}
}

// dots are the dot directories read in dir: none in the home directory,
// whose ~/.claude holds Claude Code's own files for the person, which gnosh
// does not read (the person's own are gnosh's, CFG-20 and DECISIONS 245),
// and which a walk up from a project below the home directory read as the
// project's (the instruction-files review of 2026-11-20).
func (l *loading) dots(dir string) []string {
	if s := l.s; s.realHome != "" {
		if real, err := l.k.realDir(dir); err == nil && real == s.realHome {
			return nil
		}
	}
	return l.s.o.DotDirs
}

// above reports whether dir lies above the project root, going by where its
// links lead.
func (l *loading) above(dir string) bool {
	real, err := l.k.realDir(dir)
	if err != nil {
		real = dir
	}
	return !within(real, l.s.realRoot)
}

// wantsFallback reports whether the fallback names are read in dir.
func (l *loading) wantsFallback(dir string) bool {
	switch l.s.o.Fallback {
	case FallbackAlso:
		return true
	case FallbackInstead:
		return l.s.fallback && !l.hasMain(dir)
	}
	return false
}

// hasMain reports whether dir holds a file under one of the project or
// local names that the loader would read: an ordinary file that is not
// excluded and, above the project root, passes the ownership test.
func (l *loading) hasMain(dir string) bool {
	s := l.s
	above := l.above(dir)
	for _, name := range s.o.ProjectNames {
		if l.present(dir, path.Join(dir, name), above) {
			return true
		}
		for _, dot := range l.dots(dir) {
			if l.present(dir, path.Join(dir, dot, name), above) {
				return true
			}
		}
	}
	for _, name := range s.o.LocalNames {
		if l.present(dir, path.Join(dir, name), above) {
			return true
		}
	}
	return false
}

// present reports whether p, looked for by name in dir, is a file the
// loader would read.
func (l *loading) present(dir, p string, above bool) bool {
	s := l.s
	if s.excluded(p) {
		return false
	}
	pr := l.k.probe(p)
	if pr.err != nil || !pr.info.Mode().IsRegular() || s.excluded(pr.real) {
		return false
	}
	return !above || l.ownedFrom(dir, p)
}

// ownedFrom reports whether the file at p, found by name in or under dir,
// belongs to the person or to the system, along with everything on the way
// from dir to it: each directory, each link and what each link leads to. A
// stranger who can write in dir can then add nothing to what is read from
// it, not even by pointing a link at a file of the person's own.
func (l *loading) ownedFrom(dir, p string) bool {
	for q := p; q != dir && q != "/"; q = path.Dir(q) {
		pr := l.k.probe(q)
		if pr.err != nil || !l.s.owned(pr.linfo) || !l.s.owned(pr.info) {
			return false
		}
	}
	return true
}

// rulesDir reads the rules directory at dir and the directories under it, in
// name order. t describes the rules: whose they are, why they load, and the
// directory their path patterns are relative to.
func (l *loading) rulesDir(dir string, t want) {
	s := l.s
	if l.ctx.Err() != nil {
		return
	}
	pr := l.k.probe(dir)
	if pr.err != nil || !pr.info.IsDir() {
		s.stamps[dir] = l.k.stamp(dir)
		if pr.err != nil && !errors.Is(pr.err, fs.ErrNotExist) {
			l.warn(WarnUnreadable, dir, "", fmt.Sprintf("The rules directory %s could not be read: %s.", plain(osPath(dir)), reason(pr.err)))
		}
		return
	}
	if l.scanned[pr.real] || s.excluded(dir) || s.excluded(pr.real) {
		return
	}
	// A directory is not searched before it is known to be one that may be
	// read: a link named "rules" can point at the top of the disk.
	shown := plain(osPath(dir))
	if t.above && !l.ownedFrom(t.base, dir) {
		l.warn(WarnNotOwned, dir, "", fmt.Sprintf("The rules directory %s lies above the project and belongs to another account, or is reached through something that does, so it was not read.", shown))
		return
	}
	if t.outside(s, pr.real) {
		asker := t
		asker.path, asker.kind = dir, KindRule
		if !l.approve(asker, pr.real) {
			l.warn(WarnImportRefused, pr.real, dir, fmt.Sprintf("The rules directory %s leads, through a link, to %s, which is outside the project; that was not approved, so it was not read.", shown, plain(osPath(pr.real))))
			return
		}
		// The directory was let in as a whole: what lies inside it needs
		// no asking, and a link that leads out of it does.
		t.under = pr.real
	}
	l.scanned[pr.real] = true
	entries, err := s.d.readDir(pr.real)
	if err != nil {
		s.stamps[dir] = stamp{exists: true, dir: true}
		l.warn(WarnUnreadable, dir, "", fmt.Sprintf("The rules directory %s could not be read: %s.", plain(osPath(dir)), reason(err)))
		return
	}
	s.stamps[dir] = stamp{exists: true, dir: true, names: joinNames(entries)}
	for _, e := range entries {
		if l.ctx.Err() != nil {
			return
		}
		p := path.Join(dir, e.Name())
		child := l.k.probe(p)
		switch {
		case child.err != nil:
			// A link that leads nowhere, or round in a circle.
			if strings.HasSuffix(e.Name(), ".md") {
				s.stamps[p] = stamp{}
				l.warn(WarnUnreadable, p, "", fmt.Sprintf("The rule %s could not be read: %s.", plain(osPath(p)), reason(child.err)))
			}
		case child.info.IsDir():
			l.rulesDir(p, t)
		case strings.HasSuffix(e.Name(), ".md"):
			if l.rules >= rulesMost {
				l.warn(WarnTooMany, dir, "", fmt.Sprintf("More than %d rule files were found, so those after the first %d, %s among them, were not read.", rulesMost, rulesMost, plain(osPath(p))))
				return
			}
			l.rules++
			l.rule(p, t)
		}
	}
}

// rule reads the rule file at p. A rule with no path patterns is loaded now;
// one with patterns is put by until a matching file is touched.
func (l *loading) rule(p string, t want) {
	s := l.s
	t.path, t.kind = p, KindRule
	if pr := l.k.probe(p); pr.err == nil && s.ruleAt[pr.real] != nil {
		return // already put by
	}
	got, ok := l.fetch(t)
	if !ok {
		return
	}
	body, globs, problem := splitFrontmatter(got.text)
	if problem != "" {
		l.warn(WarnFrontmatter, p, "", fmt.Sprintf("The frontmatter of the rule %s was not understood (%s), so the rule was loaded whole and applies to every file.", plain(osPath(p)), problem))
		body, globs = got.text, nil
	}
	if len(globs) == 0 {
		l.add(t, got, body)
		return
	}
	pats, problems := compilePatterns(globs)
	for _, bad := range problems {
		l.warn(WarnPattern, p, "", fmt.Sprintf("The rule %s has a path pattern that cannot be used and matches nothing: %s (%s).", plain(osPath(p)), plain(bad.src), bad.why))
	}
	baseReal, err := l.k.realDir(t.base)
	if err != nil {
		baseReal = t.base
	}
	r := &pathRule{w: t, real: got.real, globs: globs, pats: pats, baseReal: baseReal}
	s.rules = append(s.rules, r)
	s.ruleAt[got.real] = r
}

// deliver loads a rule whose patterns matched the touched file at trigger.
// A rule that an import has brought in already, or that is no longer
// there, is passed over.
func (l *loading) deliver(r *pathRule, trigger string) {
	r.done = true
	w := r.w
	w.reason, w.late, w.trigger = ReasonRuleMatch, true, trigger
	got, ok := l.fetch(w)
	if !ok {
		return
	}
	body, _, problem := splitFrontmatter(got.text)
	if problem != "" {
		body = got.text
	}
	l.addFile(w, got, body, r.globs)
}

// fetch finds the file w describes, checks that it may be read, and reads
// it. It reports false, after any warning that is due, when the file is not
// to be loaded: it is not there, is already loaded, is excluded, is not an
// ordinary text file of a sane size, belongs to someone else, or lies
// outside the project without approval.
func (l *loading) fetch(w want) (fetched, bool) {
	s := l.s
	if l.ctx.Err() != nil || s.excluded(w.path) {
		return fetched{}, false
	}
	shown := plain(osPath(w.path))
	pr := l.k.probe(w.path)
	s.stamps[w.path] = l.k.stamp(w.path)
	if pr.err != nil {
		switch {
		case !errors.Is(pr.err, fs.ErrNotExist):
			l.warn(WarnUnreadable, w.path, w.parent, fmt.Sprintf("%s could not be read: %s.", shown, reason(pr.err)))
		case w.kind == KindImport && meantAsPath(w.written):
			l.warn(WarnImportMissing, w.path, w.parent, fmt.Sprintf("%s imports @%s, but %s is not there.", plain(osPath(w.parent)), plain(w.written), shown))
		case w.kind != KindImport && pr.linfo != nil:
			// Something is there under the name: a link to nothing.
			l.warn(WarnUnreadable, w.path, w.parent, fmt.Sprintf("%s is a link that leads nowhere.", shown))
		}
		return fetched{}, false
	}
	if _, held := s.loaded[pr.real]; held || s.excluded(pr.real) {
		return fetched{}, false
	}
	if !pr.info.Mode().IsRegular() {
		what := "is not an ordinary file"
		if pr.info.IsDir() {
			what = "is a directory, not a file"
		}
		l.warn(WarnNotFile, w.path, w.parent, fmt.Sprintf("%s %s, so it was not loaded.", shown, what))
		return fetched{}, false
	}
	if w.above && !l.ownedFrom(w.base, w.path) {
		l.warn(WarnNotOwned, w.path, w.parent, fmt.Sprintf("%s lies above the project and belongs to another account, or is reached through something that does, so it was not loaded.", shown))
		return fetched{}, false
	}
	if w.outside(s, pr.real) && !l.approve(w, pr.real) {
		from := w.path
		where := "which is outside the project; that was not approved"
		if s.secretPath(pr.real) {
			where = "where keys and passwords are kept, which a project's file never brings in"
		}
		text := fmt.Sprintf("%s leads, through a link, to %s, %s, so it was not loaded.", shown, plain(osPath(pr.real)), where)
		if w.kind == KindImport {
			from = w.parent
			target := plain(osPath(pr.real))
			if w.path != pr.real {
				target = fmt.Sprintf("%s (which leads, through a link, to %s)", shown, target)
			}
			text = fmt.Sprintf("%s imports %s, %s, so it was not loaded.", plain(osPath(w.parent)), target, where)
		}
		l.warn(WarnImportRefused, pr.real, from, text)
		return fetched{}, false
	}

	data, info, err := s.d.read(pr.real, s.o.MaxBytes)
	switch {
	case errors.Is(err, errTooBig):
		size := "more than " + sizeText(s.o.MaxBytes)
		if info != nil && info.Size() > s.o.MaxBytes {
			size = sizeText(info.Size())
		}
		l.warn(WarnTooBig, w.path, w.parent, fmt.Sprintf("%s is %s, over the %s limit for an instruction file, so it was not loaded.", shown, size, sizeText(s.o.MaxBytes)))
		return fetched{}, false
	case errors.Is(err, errNotRegular):
		l.warn(WarnNotFile, w.path, w.parent, fmt.Sprintf("%s is not an ordinary file, so it was not loaded.", shown))
		return fetched{}, false
	case err != nil:
		l.warn(WarnUnreadable, w.path, w.parent, fmt.Sprintf("%s could not be read: %s.", shown, reason(err)))
		return fetched{}, false
	}
	body, binary, repaired := cleanText(data)
	if binary {
		l.warn(WarnNotText, w.path, w.parent, fmt.Sprintf("%s is not text (it holds a zero byte), so it was not loaded.", shown))
		return fetched{}, false
	}
	if repaired {
		l.warn(WarnInvalidUTF8, w.path, w.parent, fmt.Sprintf("%s is not valid UTF-8; it was loaded with the bad bytes replaced.", shown))
	}
	// What a terminal draws as nothing is taken out, as from a typed
	// prompt: words in tag characters, or put in another order by the
	// controls of the drawing order, are read by the model as instructions
	// nobody reading the file saw (the instruction-files review of
	// 2026-11-20).
	if clean, found := text.CleanInvisible(body); found.Removed > 0 {
		body = clean
		if found.Hiding {
			l.warn(WarnInvisible, w.path, w.parent, fmt.Sprintf("%s holds characters that are drawn as nothing and can hide words or change their order; they were taken out of what the model is given.", shown))
		}
	}
	return fetched{real: pr.real, info: info, text: body}, true
}

// approve asks whether w may bring in the file at real, which lies outside
// the project root. Each target is asked about once.
func (l *loading) approve(w want, real string) bool {
	s := l.s
	if s.secretPath(real) {
		// Where keys are kept is never a project file's to bring in, an
		// approval of the project's outside files notwithstanding.
		return false
	}
	if ok, asked := s.approved[real]; asked {
		return ok
	}
	ok := false
	if s.o.ApproveImport != nil {
		from := w.parent
		if w.kind != KindImport {
			from = w.path
		}
		ok = s.o.ApproveImport(osPath(from), osPath(real))
	}
	s.approved[real] = ok
	if !ok {
		s.unapproved = append(s.unapproved, real)
	}
	return ok
}

// add records a fetched file whose text is body, then follows its imports.
func (l *loading) add(w want, got fetched, body string) {
	l.addFile(w, got, body, nil)
}

// addFile is add for a file that may carry a rule's path patterns.
func (l *loading) addFile(w want, got fetched, body string, globs []string) {
	s := l.s
	text := stripComments(body)
	cut := false
	if w.late {
		text, cut = cutText(text, s.o.LateMaxChars)
	}
	unit := w.unit
	if w.late && unit == "" {
		unit = got.real
	}
	f := File{
		Path:      osPath(w.path),
		RealPath:  osPath(got.real),
		Scope:     w.scope,
		Kind:      w.kind,
		Reason:    w.reason,
		Late:      w.late,
		Depth:     w.depth,
		Globs:     globs,
		Size:      got.info.Size(),
		ModTime:   got.info.ModTime(),
		Text:      text,
		Truncated: cut,
	}
	if w.kind == KindImport {
		f.Parent = osPath(w.parent)
	} else if w.base != "" {
		f.Base = osPath(w.base)
	}
	if w.trigger != "" {
		f.Trigger = osPath(w.trigger)
	}
	s.files = append(s.files, f)
	l.files = append(l.files, f)
	s.loaded[got.real] = unit
	if cut {
		l.warn(WarnCut, w.path, w.parent, fmt.Sprintf("%s is long, so the model was given only its first %d characters and told to read the rest.", plain(osPath(w.path)), s.o.LateMaxChars))
	}
	l.imports(w, got.real, unit, text)
}

// imports loads the files that the text of the file described by w brings in
// with "@path", in the order they are written, each with its own imports
// before the next.
func (l *loading) imports(w want, real, unit, text string) {
	s := l.s
	if s.o.MaxDepth < 0 {
		return
	}
	refs := scanImports(text)
	if len(refs) == 0 {
		return
	}
	if len(refs) > importsMost {
		l.warn(WarnTooMany, w.path, w.parent, fmt.Sprintf("%s imports %d files, so only its first %d imports were followed.", plain(osPath(w.path)), len(refs), importsMost))
		refs = refs[:importsMost]
	}
	// A relative import is relative to the file it is written in. For a
	// file reached through a link that is where the link leads, since that
	// is where the file's neighbours are.
	from := path.Dir(real)
	if shownDir := path.Dir(w.path); shownDir != from {
		if rd, err := l.k.realDir(shownDir); err == nil && rd == from {
			from = shownDir
		}
	}
	for _, written := range refs {
		if l.ctx.Err() != nil {
			return
		}
		child := want{
			scope:   w.scope,
			kind:    KindImport,
			reason:  ReasonImport,
			late:    w.late,
			trigger: w.trigger,
			parent:  w.path,
			written: written,
			depth:   w.depth + 1,
			unit:    unit,
			free:    w.scope == ScopeUser,
			fence:   w.fence,
		}
		switch {
		case written == "~" || strings.HasPrefix(written, "~/"):
			if s.home == "" {
				l.warn(WarnImportMissing, written, w.path, fmt.Sprintf("%s imports @%s, but no home directory is known, so it was not followed.", plain(osPath(w.path)), plain(written)))
				continue
			}
			child.path = path.Join(s.home, written[1:])
		case strings.HasPrefix(written, "/"):
			child.path = path.Clean(written)
		default:
			child.path = path.Join(from, written)
		}
		if child.depth > s.o.MaxDepth {
			// Say so only if there is something that was left out.
			pr := l.k.probe(child.path)
			if _, held := s.loaded[pr.real]; pr.err == nil && !held && pr.info.Mode().IsRegular() {
				l.warn(WarnImportDepth, child.path, w.path, fmt.Sprintf("%s imports %s, but that is more than %d imports deep, so it was not followed.", plain(osPath(w.path)), plain(osPath(child.path)), s.o.MaxDepth))
			}
			continue
		}
		if got, ok := l.fetch(child); ok {
			l.add(child, got, got.text)
		}
	}
}

// warn records a warning, unless the same one was recorded before.
func (l *loading) warn(kind WarningKind, p, from, text string) {
	w := Warning{Kind: kind, Path: osPath(p), Text: text}
	if from != "" {
		w.From = osPath(from)
	}
	if l.s.warned[w] {
		return
	}
	l.s.warned[w] = true
	l.s.warnings = append(l.s.warnings, w)
	l.warnings = append(l.warnings, w)
}

// excluded reports whether the absolute path p matches an exclude pattern.
func (s *Set) excluded(p string) bool {
	if len(s.exclude) == 0 || p == "/" {
		return false
	}
	rel := strings.TrimPrefix(p, "/")
	for _, pat := range s.exclude {
		if pat.match(rel) {
			return true
		}
	}
	return false
}

// meantAsPath reports whether an import that names nothing there was
// surely meant for a file, and so is worth a warning: one written from a
// folder ("./", "../", "~/", "/") or with an instruction file's ending.
// Any other ("@alice", "@Override", "@types/node", "@pytest.fixture") is
// followed when a file of that name is there and is otherwise taken for
// ordinary text: a package or a decorator in a file's prose was warned
// about at every start (the instruction-files review of 2026-11-20).
func meantAsPath(written string) bool {
	for _, lead := range []string{"./", "../", "~/", "/"} {
		if strings.HasPrefix(written, lead) {
			return true
		}
	}
	switch strings.ToLower(path.Ext(written)) {
	case ".md", ".markdown", ".txt":
		return true
	}
	return false
}

// reason says why a file-system call failed, without the path, which the
// caller names itself.
func reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "it is not there"
	case errors.Is(err, fs.ErrPermission):
		return "permission was denied"
	}
	return err.Error()
}

// sizeText writes a byte count the way a person would say it.
func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
