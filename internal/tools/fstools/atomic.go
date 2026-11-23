package fstools

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// fileOps is the file-system calls Write and Edit make that a test needs
// to be able to make fail.
type fileOps struct {
	// rename puts a finished temporary file in the place of the real one.
	rename func(oldpath, newpath string) error
	// writeAt writes into a file written in place.
	writeAt func(f *os.File, b []byte, off int64) (int, error)
	// judged are the real paths of Write and Edit calls as they were
	// described, by call id: what the permission layer judged, which a run
	// is held to.
	judged sync.Map
}

// judge notes the real path a call was described with.
func (s *fileOps) judge(id, real string) {
	if id != "" {
		s.judged.Store(id, real)
	}
}

// judgedElsewhere reports the real path a call was described with when it
// is not real: between the question and the run, a link or a folder on the
// way changed, and the call would write what nobody was asked about.
func (s *fileOps) judgedElsewhere(id, real string) (string, bool) {
	v, ok := s.judged.LoadAndDelete(id)
	if !ok {
		return "", false
	}
	was, _ := v.(string)
	return was, was != real
}

func newFileOps() *fileOps {
	return &fileOps{rename: os.Rename, writeAt: (*os.File).WriteAt}
}

// inPlaceHelps reports whether writing a file where it is can do what
// making a temporary file beside it or the rename could not: the folder
// may not be written in, or the file is a mount point or on another
// device than its folder. Not a full disk or quota, where writing in
// place fails too, and after cutting the file short (the Edit/Write review
// of 2026-11-19).
func inPlaceHelps(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.ETXTBSY)
}

// keptModeBits are the parts of a file's mode a rewrite keeps.
const keptModeBits = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

// replaceFile makes data the whole content of the file at path. path must
// have no symbolic link in it: the caller has followed them.
//
// The new content is written to a temporary file in the same directory,
// flushed to disk, and then renamed over the old file, so that at every
// moment the path holds either all of the old content or all of the new.
// old is the info of the file being replaced, or nil when the file is new;
// a replaced file keeps its permission bits and, where the system allows
// it, its owner and group. A new file gets the permissions the system
// gives any new file.
//
// When the directory will not take a temporary file, or the rename is
// refused, for a reason writing in place gets around (inPlaceHelps), and
// the file itself exists and can be written, the content is written into
// the file in place. That is not atomic: inPlace says why it was done. No
// temporary file is left behind either way.
//
// The returned info is that of the new content's file.
func (ops *fileOps) replaceFile(path string, data []byte, old fs.FileInfo) (info fs.FileInfo, inPlace string, err error) {
	if old != nil {
		// A file with other names, or of an owner or group a new file
		// could not have, is written where it is: its other names see the
		// new content, and it stays whose it was (the Edit/Write review of
		// 2026-11-19). One that may not be written so is replaced, as any
		// other.
		if why := replaceLoses(old); why != "" {
			if changed := changedSince(path, old); changed != "" {
				return nil, "", errors.New(changed + ": nothing was written; read it again")
			}
			info, err := ops.writeInPlace(path, data)
			if err == nil {
				return info, why, nil
			}
			if !errors.Is(err, fs.ErrPermission) {
				return nil, "", fmt.Errorf("write it where it is, since %s: %w", why, err)
			}
		}
	}
	dir := filepath.Dir(path)
	perm := fs.FileMode(0o666)
	if old != nil {
		perm = old.Mode().Perm()
	}
	tmp, f, err := createTemp(dir, perm)
	if err != nil {
		if old == nil || !inPlaceHelps(err) {
			return nil, "", fmt.Errorf("make a temporary file in %s: %w", dir, underlying(err))
		}
		info, err2 := ops.writeInPlace(path, data)
		if err2 != nil {
			return nil, "", fmt.Errorf("make a temporary file in %s: %w; writing the file in place failed too: %w", dir, underlying(err), err2)
		}
		return info, "a temporary file could not be made beside it: " + underlying(err).Error(), nil
	}
	info, err = fillTemp(f, data, old)
	if err != nil {
		os.Remove(tmp)
		return nil, "", err
	}
	// Another program may have written the file while the new content was
	// made and flushed: the rename would undo what it wrote. It is looked
	// at once more, as near the rename as can be (the Edit/Write review of
	// 2026-11-19).
	if changed := changedSince(path, old); changed != "" {
		os.Remove(tmp)
		return nil, "", errors.New(changed + ": nothing was written; read it again")
	}
	if err := ops.rename(tmp, path); err != nil {
		os.Remove(tmp)
		if old == nil || !inPlaceHelps(err) {
			return nil, "", fmt.Errorf("put the new content in place: %w", underlying(err))
		}
		info, err2 := ops.writeInPlace(path, data)
		if err2 != nil {
			return nil, "", fmt.Errorf("put the new content in place: %w; writing the file in place failed too: %w", underlying(err), err2)
		}
		return info, "it could not be replaced in one step: " + underlying(err).Error(), nil
	}
	return info, "", nil
}

// changedSince says how the file at path is no longer the one old
// describes (nil: no file was there), or "" when it is still that file.
func changedSince(path string, old fs.FileInfo) string {
	now, err := os.Lstat(path)
	switch {
	case old == nil && err == nil:
		return "a file was made there by another program meanwhile"
	case old == nil:
		return ""
	case err != nil:
		return "the file was taken away by another program meanwhile"
	case !os.SameFile(old, now) || now.Size() != old.Size() || !now.ModTime().Equal(old.ModTime()):
		return "the file was changed by another program meanwhile"
	}
	return ""
}

// createTemp makes a new, empty file in dir under a name no other file
// has, and returns the name and the open file.
func createTemp(dir string, perm fs.FileMode) (string, *os.File, error) {
	for {
		var b [8]byte
		rand.Read(b[:])
		name := filepath.Join(dir, ".gnosh-"+hex.EncodeToString(b[:])+".tmp")
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			// Another file has this name: draw another.
			continue
		}
		return name, f, err
	}
}

// fillTemp gives the temporary file the owner of the file it replaces,
// writes data to it, gives it that file's mode, flushes it and closes it.
func fillTemp(f *os.File, data []byte, old fs.FileInfo) (fs.FileInfo, error) {
	fail := func(doing string, err error) (fs.FileInfo, error) {
		f.Close()
		return nil, fmt.Errorf("%s: %w", doing, underlying(err))
	}
	if old != nil {
		keepOwner(f, old)
	}
	if _, err := f.Write(data); err != nil {
		return fail("write the new content", err)
	}
	if old != nil {
		// The mode last: giving the file to its owner, and writing to it,
		// can each clear the set-id bits that this puts back.
		if err := f.Chmod(old.Mode() & keptModeBits); err != nil {
			return fail("give the new content the file's permissions", err)
		}
	}
	if err := f.Sync(); err != nil {
		return fail("flush the new content to disk", err)
	}
	info, err := f.Stat()
	if err != nil {
		return fail("examine the new content", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("finish writing the new content: %w", underlying(err))
	}
	return info, nil
}

// writeInPlace overwrites the content of an existing file where it is: the
// new content over the old from the start, and then the file cut to its
// length. A write that fails puts the old content back: the file was
// opened cut to nothing, and a full disk then left it empty or holding the
// start of the new text (the Edit/Write review of 2026-11-19).
func (ops *fileOps) writeInPlace(path string, data []byte) (fs.FileInfo, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return nil, underlying(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, underlying(err)
	}
	put := func(b []byte) error {
		if _, err := ops.writeAt(f, b, 0); err != nil {
			return err
		}
		return f.Truncate(int64(len(b)))
	}
	if err := put(data); err != nil {
		if back := put(before); back != nil {
			f.Close()
			return nil, fmt.Errorf("%w; and the old content could not be put back (%v): the file may be damaged", underlying(err), underlying(back))
		}
		f.Sync()
		f.Close()
		return nil, fmt.Errorf("%w; the file holds its old content", underlying(err))
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, underlying(err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, underlying(err)
	}
	if err := f.Close(); err != nil {
		return nil, underlying(err)
	}
	return info, nil
}

// makeParents makes the directory dir and any directories above it that
// are missing. It returns the ones it made, outermost first, so that a
// caller whose next step fails can take them away again.
func makeParents(dir string) (made []string, err error) {
	var missing []string
	for d := dir; ; {
		info, err := os.Stat(d)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%s is %s, not a directory", d, kindOf(info.Mode()))
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("examine %s: %w", d, underlying(err))
		}
		missing = append(missing, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o777); err != nil {
			if errors.Is(err, fs.ErrExist) {
				// Someone else made it meanwhile; it is not ours to remove.
				continue
			}
			removeDirs(made)
			return nil, fmt.Errorf("make the directory %s: %w", missing[i], underlying(err))
		}
		made = append(made, missing[i])
	}
	return made, nil
}

// removeDirs takes away directories that makeParents made, innermost
// first. A directory that has gained something since is left alone.
func removeDirs(made []string) {
	for i := len(made) - 1; i >= 0; i-- {
		os.Remove(made[i])
	}
}
