//go:build !unix

package mcp

// lockFile takes no lock where the system has no flock: the store's own
// mutex still keeps one process's changes apart.
func lockFile(path string) (func(), error) {
	return func() {}, nil
}
