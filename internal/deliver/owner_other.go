//go:build !unix

package deliver

import "os"

// strangerOwns reports whether a file belongs to another account: not
// known here.
func strangerOwns(st os.FileInfo) bool { return false }

// mayRead finds out whether this process may read the file at path by
// opening it and closing it again.
func mayRead(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// openNoWait opens the file at path for reading.
func openNoWait(path string) (*os.File, error) { return os.Open(path) }
