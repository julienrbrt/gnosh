//go:build unix

package fstools

import (
	"io/fs"
	"os"
	"slices"
	"syscall"
)

// otherNames is how many other names (hard links) the file old describes
// has.
func otherNames(old fs.FileInfo) int {
	if old == nil {
		return 0
	}
	if st, ok := old.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return int(st.Nlink) - 1
	}
	return 0
}

// replaceLoses says what putting a new file in the place of the one old
// describes would lose, and writing into it where it is keeps, or "": its
// other names (hard links), which would go on holding the old content;
// its owner, or its group, which this process may not give a new file.
// Editors write such a file where it is for the same reasons.
func replaceLoses(old fs.FileInfo) string {
	groups, _ := os.Getgroups()
	return replaceLosesFor(old, os.Geteuid(), os.Getegid(), groups)
}

// replaceLosesFor is replaceLoses for a process of that user, group and
// further groups.
func replaceLosesFor(old fs.FileInfo, euid, egid int, groups []int) string {
	st, ok := old.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	switch {
	case st.Nlink > 1:
		return "it has " + countOf(int(st.Nlink)-1, "other name", "other names") + " by hard links, which a new file in its place would leave holding the old content"
	case euid == 0:
		// (Root gives a new file any owner and group.)
	case int(st.Uid) != euid:
		return "it belongs to another user, and a new file in its place would be yours"
	case int(st.Gid) != egid && !slices.Contains(groups, int(st.Gid)):
		return "its group is not one of yours, and a new file in its place would have yours"
	}
	return ""
}

// keepOwner gives the open file f the owner and group of the file old
// describes, as far as the system lets this process do so. A process that
// may not hand a file to another user can usually still give it the group;
// when it can do neither, the new file stays its own, and the write goes
// ahead.
func keepOwner(f *os.File, old fs.FileInfo) {
	want, ok := old.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	info, err := f.Stat()
	if err != nil {
		return
	}
	have, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (have.Uid == want.Uid && have.Gid == want.Gid) {
		return
	}
	if f.Chown(int(want.Uid), int(want.Gid)) != nil && have.Gid != want.Gid {
		f.Chown(-1, int(want.Gid))
	}
}

// mayWrite asks the system whether this process may write the file at
// path, without opening it: opening a file for writing, even to write
// nothing, wakes whatever is watching the file.
func mayWrite(path string) error {
	const writeAccess = 0x2 // W_OK
	return syscall.Access(path, writeAccess)
}
