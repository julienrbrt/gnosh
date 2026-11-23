//go:build !unix

package app

import (
	"errors"
	"os"
)

// openNoLink opens a file to read, refusing one that is a link.
func openNoLink(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, errors.New("not a plain file")
	}
	return os.Open(path)
}
