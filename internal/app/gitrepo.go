package app

import (
	"bufio"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// gitFileMost is the most of a repository's .git file, commondir or
// config that is read.
const gitFileMost = 1 << 20

// openGitFile opens one of a repository's small files for reading, only
// when it is a regular file: a named pipe at .git, in an archive unpacked
// with it, blocked every run of the status line, /statusline and gnosh's
// exit for good (the status line review of 2026-11-19). Read it through
// io.LimitReader(f, gitFileMost).
func openGitFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New(path + " is not a file")
	}
	return os.Open(path)
}

// readGitFile reads at most gitFileMost of one of a repository's small
// files (openGitFile).
func readGitFile(path string) ([]byte, error) {
	f, err := openGitFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, gitFileMost))
}

// originRepo reads the address of the origin remote from the git
// repository at root, from its own config file (no git is run, so it may
// be read in a folder that is not trusted), and takes the host, the owner
// and the repository's name from it, as Claude Code's status line gives
// them. ok is false outside a repository, without an origin, or for an
// address that is not of that shape.
func originRepo(root string) (host, owner, name string, ok bool) {
	if root == "" {
		return "", "", "", false
	}
	cfg := gitConfigPath(filepath.Join(root, ".git"))
	if cfg == "" {
		return "", "", "", false
	}
	return parseRepoURL(originURL(cfg))
}

// gitConfigPath is the config file of the repository whose .git is dot: a
// folder holds it; a file names the folder (gitdir: ...), whose commondir,
// for a work tree, names the folder that holds it.
func gitConfigPath(dot string) string {
	info, err := os.Stat(dot)
	if err != nil {
		return ""
	}
	dir := dot
	if !info.IsDir() {
		b, err := readGitFile(dot)
		if err != nil {
			return ""
		}
		gitdir, found := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
		if !found {
			return ""
		}
		dir = strings.TrimSpace(gitdir)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(dot), dir)
		}
		if common, err := readGitFile(filepath.Join(dir, "commondir")); err == nil {
			c := strings.TrimSpace(string(common))
			if !filepath.IsAbs(c) {
				c = filepath.Join(dir, c)
			}
			dir = c
		}
	}
	return filepath.Join(dir, "config")
}

// originURL is the url of [remote "origin"] in a git config file, or "".
func originURL(path string) string {
	f, err := openGitFile(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(io.LimitReader(f, gitFileMost))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = strings.EqualFold(strings.Join(strings.Fields(line), " "), `[remote "origin"]`)
			continue
		}
		if !in {
			continue
		}
		if key, value, found := strings.Cut(line, "="); found && strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// parseRepoURL takes host, owner and name from a remote's address:
// https://host/owner/name(.git), ssh://user@host[:port]/owner/name, or
// user@host:owner/name. The owner is all of the path before the name.
func parseRepoURL(raw string) (host, owner, name string, ok bool) {
	var p string
	switch {
	case raw == "":
		return "", "", "", false
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", "", "", false
		}
		host, p = u.Hostname(), u.Path
	default:
		at := strings.LastIndex(raw, "@")
		h, path, found := strings.Cut(raw[at+1:], ":")
		if !found || h == "" || strings.Contains(h, "/") {
			return "", "", "", false
		}
		host, p = h, path
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	i := strings.LastIndex(p, "/")
	if i <= 0 || i == len(p)-1 {
		return "", "", "", false
	}
	return strings.ToLower(host), p[:i], p[i+1:], true
}
