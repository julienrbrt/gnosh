//go:build !unix

package fstools

import (
	"io/fs"
	"os"
)

// replaceLoses says what a new file in the place of the one old describes
// would lose: nothing known here.
func replaceLoses(old fs.FileInfo) string { return "" }

// keepOwner does nothing on systems without Unix file ownership.
func keepOwner(f *os.File, old fs.FileInfo) {}

// otherNames is how many other names (hard links) the file old describes
// has: none known here.
func otherNames(old fs.FileInfo) int { return 0 }

// mayWrite finds out whether this process may write the file at path by
// opening it for writing and closing it again.
func mayWrite(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	return f.Close()
}
