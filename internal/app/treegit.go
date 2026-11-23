package app

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/permission"
)

// This file is what the work-tree checks learn from the disk about git: the
// repository git opens from a folder, which a .git file names and the text
// of a command line does not, and the remotes the work tree's repository
// has. In a work tree of its own, git runs only against the work tree's
// own repository (DECISIONS 76): a .git file that points at the checkout's
// repository, a folder of another repository, or one nested in the work
// tree with settings of its own, would let the allowed commands reach the
// checkout.

// dotGit reports whether the path p names a .git file or folder, or
// something inside one: what says which repository git opens.
func dotGit(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

// dotGitRefusal says why a session with the work tree t may not write p.
func dotGitRefusal(t core.WorkTree, p string) string {
	return fmt.Sprintf("You work in a git work tree of your own, at %s, and %s is a .git file or folder, or in one: it says which repository git opens, and in a work tree of your own it is not to be changed.", t.Path, p)
}

// gitDirIn is the git folder that git run in dir opens, as git finds it:
// the first .git, a folder or a file that names one, in dir or a folder
// above it. ok is false when there is none, or dir is not there yet.
func gitDirIn(dir string) (gitdir string, why string) {
	real, err := resolvePath(dir)
	if err != nil {
		return "", "the folder " + dir + " is not there yet, so which repository git would open in it cannot be told"
	}
	for d := real; ; d = filepath.Dir(d) {
		p := filepath.Join(d, ".git")
		if fi, err := os.Lstat(p); err == nil {
			switch {
			case fi.IsDir():
				return resolved(p), ""
			case fi.Mode().IsRegular():
				data, err := os.ReadFile(p)
				if err != nil {
					return "", "the .git file at " + p + " cannot be read"
				}
				line, _, _ := strings.Cut(string(data), "\n")
				target, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
				if !ok {
					return "", "the .git file at " + p + " names no git folder"
				}
				target = strings.TrimSpace(target)
				if !filepath.IsAbs(target) {
					target = filepath.Join(d, target)
				}
				return resolved(target), ""
			default:
				return "", "the .git at " + p + " is neither a folder nor a file"
			}
		}
		if d == filepath.Dir(d) {
			return "", "git would find no repository in " + dir
		}
	}
}

// treeGit is what SharedGitChange asks of the disk for the work tree t.
func treeGit(t core.WorkTree) permission.TreeGit {
	own, ownWhy := gitDirIn(t.Path)
	return permission.TreeGit{
		Repo: func(dir string) string {
			if ownWhy != "" {
				return "gnosh cannot tell your work tree's own repository: " + ownWhy
			}
			got, why := gitDirIn(dir)
			switch {
			case why != "":
				return why
			case permission.PathKey(got) != permission.PathKey(own):
				return "git run in " + dir + " would open the repository at " + got + ", not your work tree's own"
			}
			return ""
		},
		Remote: func(name string) bool {
			return ownWhy == "" && hasRemote(own, name)
		},
		Stashes: func() bool {
			return ownWhy != "" || hasStashes(own)
		},
	}
}

// commonDir is the folder of a repository a work tree's git folder shares
// with the others (its file commondir names it), or gitdir itself.
func commonDir(gitdir string) string {
	if data, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		c := strings.TrimSpace(string(data))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitdir, c)
		}
		return filepath.Clean(c)
	}
	return gitdir
}

// hasStashes reports whether the repository whose work tree's git folder
// is gitdir keeps any stash: refs/stash, loose or packed. What cannot be
// read counts as one.
func hasStashes(gitdir string) bool {
	common := commonDir(gitdir)
	if _, err := os.Stat(filepath.Join(common, "refs", "stash")); err == nil || !os.IsNotExist(err) {
		return true
	}
	data, err := os.ReadFile(filepath.Join(common, "packed-refs"))
	if err != nil {
		return !os.IsNotExist(err)
	}
	return strings.Contains(string(data), " refs/stash\n")
}

// remoteSection matches the first line of a remote's section in git's
// settings: [remote "origin"].
var remoteSection = regexp.MustCompile(`^\s*\[\s*remote\s+"([^"]*)"\s*\]`)

// hasRemote reports whether the repository whose work tree's git folder is
// gitdir has a remote named name: in the settings of its common folder
// (a work tree's git folder names that folder in its file commondir).
func hasRemote(gitdir, name string) bool {
	f, err := os.Open(filepath.Join(commonDir(gitdir), "config"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := remoteSection.FindStringSubmatch(sc.Text()); m != nil && m[1] == name {
			return true
		}
	}
	return false
}
