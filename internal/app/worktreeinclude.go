package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A new work tree is a checkout of a commit, and what git ignores is not
// in one: a .env, a local settings file. As Claude Code does, the files a
// repository's .worktreeinclude names (in the syntax of .gitignore) that
// git ignores are copied into each new work tree, a session's (--worktree)
// or a sub-agent's, each to the same place in it. A copy the work tree
// still holds as it was copied is no work of the tree's: one whose only
// difference from the repository is such copies is removed at its end as
// one that holds nothing.

// includeFile is the file, in a repository's top folder, that names them.
const includeFile = ".worktreeinclude"

// copyIncluded copies into the work tree wt, which was just made, the
// files of its repository that .worktreeinclude names and git ignores,
// and returns those it copied. A file the work tree has already is left as
// it is, and so is one whose place in the work tree leads out of it (by a
// link the checkout holds). The error says what could not be copied, and
// that the copying stopped when ctx ended first.
func copyIncluded(ctx context.Context, environ []string, wt *worktree) ([]string, error) {
	list := filepath.Join(wt.repo, includeFile)
	if info, err := os.Stat(list); err != nil || !info.Mode().IsRegular() {
		return nil, nil
	}
	// The files it names that git does not track ...
	named, err := gitWith(ctx, environ, wt.repo, "", "ls-files", "-z", "--others", "--ignored", "--exclude-from="+list)
	if err != nil {
		return nil, fmt.Errorf("the files %s names could not be listed (%v)", includeFile, err)
	}
	if strings.Trim(named, "\x00") == "" {
		return nil, nil
	}
	// ... and of those, the ones git ignores.
	ignored, err := gitWith(ctx, environ, wt.repo, named, "check-ignore", "-z", "--stdin")
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return nil, nil // none of them
	case err != nil:
		return nil, fmt.Errorf("which of the files %s names git ignores could not be told (%v)", includeFile, err)
	}
	tree, err := filepath.EvalSymlinks(wt.path)
	if err != nil {
		return nil, err
	}
	var copied, failed []string
	for _, rel := range strings.Split(ignored, "\x00") {
		if ctx.Err() != nil {
			failed = append(failed, "the rest (the copying was stopped)")
			break
		}
		if rel == "" || !filepath.IsLocal(rel) {
			continue
		}
		dst := filepath.Join(wt.path, rel)
		if fileExists(dst) {
			continue
		}
		if err := copyInto(filepath.Join(wt.repo, rel), wt.path, rel, tree); err != nil {
			failed = append(failed, rel+" ("+err.Error()+")")
			continue
		}
		copied = append(copied, rel)
	}
	if len(failed) > 0 {
		return copied, fmt.Errorf("of the files %s names, these could not be copied into the work tree: %s", includeFile, fewOf(failed, 8))
	}
	return copied, nil
}

// fewOf joins names for a line: the first n, and how many more there are.
func fewOf(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:n], ", "), len(names)-n)
}

// copyInto copies the file or link src to rel in the work tree at root,
// whose real path is tree: its folders are made as needed, one at a time,
// each only where it stays inside the tree. A file that is not what it was
// as it was looked at (a link put in its place since) is not copied, and
// a copy that fails part way is taken away again.
func copyInto(src, root, rel, tree string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, rel)
	dir, err := makeDirsIn(root, filepath.Dir(rel), tree)
	if err != nil {
		return err
	}
	if !inTree(dir, tree) {
		return errLeadsOut
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case !info.Mode().IsRegular():
		return errors.New("it is no file")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if now, err := in.Stat(); err != nil || !now.Mode().IsRegular() || !os.SameFile(info, now) {
		return errors.New("it changed as it was being copied")
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

// errLeadsOut is the refusal of a copy whose folder in the work tree is,
// by a link, somewhere else.
var errLeadsOut = errors.New("its folder in the work tree leads out of it")

// makeDirsIn makes the folder rel (a local path, "." for root itself)
// under root, one folder at a time from root down. Each that is there
// already must be a folder whose real path is inside tree, the real path
// of root, before anything is made in it: a link the checkout holds, which
// leads out of the tree, is never followed to make a folder. It returns
// the folder.
func makeDirsIn(root, rel, tree string) (string, error) {
	cur := root
	if rel == "." {
		return cur, nil
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(cur, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(cur)
		}
		switch {
		case err != nil:
			return "", err
		case info.Mode()&os.ModeSymlink != 0:
			if !inTree(cur, tree) {
				return "", errLeadsOut
			}
			if real, err := os.Stat(cur); err != nil || !real.IsDir() {
				return "", errors.New("a link to no folder stands where its folder would be")
			}
		case !info.IsDir():
			return "", errors.New("a file stands where its folder would be")
		}
	}
	return cur, nil
}

// inTree reports whether p's real path is tree, a real path, or inside it.
func inTree(p, tree string) bool {
	real, err := filepath.EvalSymlinks(p)
	return err == nil && (real == tree || strings.HasPrefix(real, tree+string(filepath.Separator)))
}

// copiedAsIs reports whether what stands at rel in the work tree wt, a
// path git status gives as ignored, is a copy of what stands there in the
// repository's own folder: a file of the same bytes, a link to the same
// place, a folder of nothing else. Taking it away with the work tree loses
// nothing. When ctx ends first it says no, which keeps the tree.
func copiedAsIs(ctx context.Context, wt *worktree, rel string) bool {
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" || !filepath.IsLocal(rel) {
		return false
	}
	return sameFiles(ctx, filepath.Join(wt.path, rel), filepath.Join(wt.repo, rel))
}

// sameFiles reports whether a is what b is: the same bytes, the same link,
// or a folder each of whose entries is the same as b's of that name. Once
// ctx has ended it says no.
func sameFiles(ctx context.Context, a, b string) bool {
	if ctx.Err() != nil {
		return false
	}
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	if err != nil || ai.Mode().Type() != bi.Mode().Type() {
		return false
	}
	switch {
	case ai.Mode()&os.ModeSymlink != 0:
		ta, err1 := os.Readlink(a)
		tb, err2 := os.Readlink(b)
		return err1 == nil && err2 == nil && ta == tb
	case ai.IsDir():
		entries, err := os.ReadDir(a)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !sameFiles(ctx, filepath.Join(a, e.Name()), filepath.Join(b, e.Name())) {
				return false
			}
		}
		return true
	case ai.Mode().IsRegular():
		return ai.Size() == bi.Size() && sameBytes(a, b)
	}
	return false
}

// sameBytes reports whether two files hold the same bytes.
func sameBytes(a, b string) bool {
	fa, err := os.Open(a)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()
	// (A read that ends the file early ends it, on both sides at once.)
	ended := func(err error) bool { return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) }
	ba, bb := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false
		}
		if ea != nil || eb != nil {
			return ended(ea) && ended(eb)
		}
	}
}
