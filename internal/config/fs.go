package config

import (
	"io/fs"
	"os"
	"path/filepath"
)

// FS is what Load reads files through. Load never writes.
type FS interface {
	// ReadFile returns the whole of a file.
	ReadFile(name string) ([]byte, error)
	// Stat describes a file, following links.
	Stat(name string) (fs.FileInfo, error)
	// Owner returns the id of the user who owns a file. ok is false where
	// that cannot be told.
	Owner(name string) (uid int, ok bool)
}

// WriteFS is an FS that the state file can be written through.
type WriteFS interface {
	FS
	// MkdirAll makes a directory and its parents.
	MkdirAll(name string, perm fs.FileMode) error
	// WriteFile replaces a file in one step: a reader sees the old file or
	// the new one, never a part of either.
	WriteFile(name string, data []byte, perm fs.FileMode) error
}

// OS is the machine's own file system.
type OS struct{}

// ReadFile reads a file.
func (OS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// Stat describes a file, following links.
func (OS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// Lstat describes a file without following a link at its end.
func (OS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

// lstater is an FS that can tell a link from what it leads to.
type lstater interface {
	Lstat(name string) (fs.FileInfo, error)
}

// linkedFile reports whether name is a link, on an FS that can tell.
func linkedFile(fsys FS, name string) bool {
	lf, ok := fsys.(lstater)
	if !ok {
		return false
	}
	info, err := lf.Lstat(name)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

// Owner returns the id of the user who owns a file.
func (OS) Owner(name string) (int, bool) {
	info, err := os.Stat(name)
	if err != nil {
		return 0, false
	}
	return ownerOf(info)
}

// MkdirAll makes a directory and its parents.
func (OS) MkdirAll(name string, perm fs.FileMode) error { return os.MkdirAll(name, perm) }

// WriteFile writes data to a new file beside name and renames it over
// name, so that name is never seen half written.
func (OS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return WriteFileAtomic(name, data, perm)
}

// WriteFileAtomic replaces the file at name with data. It writes a
// temporary file in the same directory, flushes it to disk and renames it
// over name. If anything fails the temporary file is removed and name is
// left as it was.
func WriteFileAtomic(name string, data []byte, perm fs.FileMode) (err error) {
	dir, base := filepath.Split(name)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), name); err != nil {
		return err
	}
	// Flushing the directory makes the rename itself survive a power cut.
	// Not every file system allows it, and the file is already in place.
	if d, derr := os.Open(dir); derr == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
