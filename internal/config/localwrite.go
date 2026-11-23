package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A project's local settings file (.gnosh/settings.local.json) is where
// gnosh keeps the person's saved approvals. While it holds just what the
// person's gnosh last wrote there it is theirs, and in a folder they have
// not trusted it is read as theirs, as Claude Code reads its own local
// file: held there as the repository's, an approval saved with "Yes, and
// don't ask again" went out of force at the next reading of the settings,
// and a run never had it (the settings-commands review of 2026-11-20). A
// file the repository brought, or one anyone else changed, holds
// something else, and is the project's, held until the folder is trusted.
// What gnosh wrote is noted in the person's state folder, which no
// project can write.

// localWriteNote is where gnosh notes what it last wrote in the local
// settings file at path: a file in the state folder named for the file's
// real path.
func localWriteNote(stateDir, path string) string {
	real := path
	if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		real = filepath.Join(dir, filepath.Base(path))
	}
	sum := sha256.Sum256([]byte(real))
	return filepath.Join(stateDir, "local-writes", hex.EncodeToString(sum[:16]))
}

// NoteLocalWrite notes that gnosh wrote content in the local settings file
// at path.
func NoteLocalWrite(stateDir, path string, content []byte) error {
	if stateDir == "" {
		return nil
	}
	note := localWriteNote(stateDir, path)
	if err := os.MkdirAll(filepath.Dir(note), 0o700); err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	if err := WriteFileAtomic(note, []byte(hex.EncodeToString(sum[:])+"\n"), 0o600); err != nil {
		return fmt.Errorf("what gnosh wrote in %s could not be noted: %w", path, err)
	}
	return nil
}

// ownLocal reports whether the local settings file at path holds just what
// the person's gnosh last wrote there, and is no link.
func (ld *loader) ownLocal(path string) bool {
	if ld.paths.StateDir == "" || linkedFile(ld.in.FS, path) {
		return false
	}
	data, err := ld.in.FS.ReadFile(path)
	if err != nil {
		return false
	}
	note, err := ld.in.FS.ReadFile(localWriteNote(ld.paths.StateDir, path))
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return strings.TrimSpace(string(note)) == hex.EncodeToString(sum[:])
}
