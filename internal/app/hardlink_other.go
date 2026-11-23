//go:build !unix

package app

import "os"

// hardLinked reports whether the file at p has more than one name; where
// that cannot be told, it says no.
func hardLinked(p string) bool { return false }

// writable reports whether the file at p may be written: whether it opens
// for writing.
func writable(p string) bool {
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	return f.Close() == nil
}
