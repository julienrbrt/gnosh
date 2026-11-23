//go:build unix

package deliver

import (
	"os"
	"syscall"
)

// strangerOwns reports whether a file belongs to an account other than
// this process's and root's.
func strangerOwns(st os.FileInfo) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && s.Uid != 0 && int(s.Uid) != os.Getuid()
}

// mayRead asks the system whether this process may read the file at path,
// without opening it.
func mayRead(path string) error {
	const readAccess = 0x4 // R_OK
	return syscall.Access(path, readAccess)
}

// openNoWait opens the file at path for reading without waiting: a pipe
// opens at once, empty, where an open of one waits for a writer.
func openNoWait(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
