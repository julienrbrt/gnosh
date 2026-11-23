//go:build !unix

package transcript

import "os"

// lockFile is a stand-in for systems without flock: it always succeeds, so
// on them nothing stops two writers.
func lockFile(f *os.File) (bool, error) {
	return true, nil
}

// Hold cannot lock without flock: it reports true.
func Hold(f *os.File) bool { return true }

// InUse cannot be told without flock: it reports false.
func InUse(path string) bool { return false }
