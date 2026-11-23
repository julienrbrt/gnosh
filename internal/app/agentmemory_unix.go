//go:build unix

package app

import (
	"os"
	"syscall"
)

// openNoLink opens a file to read, refusing a link (ELOOP) and not
// waiting on a pipe put in a file's place.
func openNoLink(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
