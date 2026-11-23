package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LoginStore keeps the sign-ins to servers reached at a URL (oauth.go), by
// the server's URL, in one JSON file that only its owner can read or
// write. It is written whole, to a new file that is synced and then
// renamed over the old, so that a reader never sees half of it. Changes
// that read the file first (Update) hold a lock on it beside it
// (lockFile), which other gnosh processes on the machine take too. A file
// that cannot be read is set aside, under a name that says when, and the
// store starts empty: one damaged file does not stop every sign-in.
type LoginStore struct {
	// Path is the file.
	Path string

	mu sync.Mutex
}

// loginFile is the file's form.
type loginFile struct {
	Logins map[string]Login `json:"logins"`
}

// read reads the file; a file not there yet holds nothing, and one that
// cannot be read is set aside.
func (s *LoginStore) read() (loginFile, error) {
	f := loginFile{Logins: map[string]Login{}}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		aside := s.Path + ".unreadable-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(s.Path, aside); rerr != nil {
			return loginFile{Logins: map[string]Login{}}, fmt.Errorf("the sign-ins in %s cannot be read (%v), nor set aside: %w", s.Path, err, rerr)
		}
		return loginFile{Logins: map[string]Login{}}, nil
	}
	if f.Logins == nil {
		f.Logins = map[string]Login{}
	}
	return f, nil
}

// write writes the file whole, readable by its owner alone, synced before
// it takes the old one's place.
func (s *LoginStore) write(f loginFile) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".mcp-logins-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// Get returns the sign-in kept for the server at the URL, when it holds a
// token.
func (s *LoginStore) Get(server string) (Login, bool) {
	if s == nil {
		return Login{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return Login{}, false
	}
	l, ok := f.Logins[server]
	return l, ok && l.AccessToken != ""
}

// Put keeps a sign-in, in place of the one kept for its server.
func (s *LoginStore) Put(l Login) error {
	return s.Update(l.Server, func(Login, bool) (Login, bool) { return l, true })
}

// Delete forgets the sign-in kept for the server at the URL, and reports
// whether there was one.
func (s *LoginStore) Delete(server string) (bool, error) {
	had := false
	err := s.Update(server, func(_ Login, ok bool) (Login, bool) {
		had = ok
		return Login{}, false
	})
	return had, err
}

// Update changes the sign-in kept for the server at the URL, under the
// lock other gnosh processes take too: change is given what is kept (and
// whether anything is), and returns what to keep, or false to forget it.
// What it is given is read under the lock, so a change made meanwhile by
// another process is what it sees.
func (s *LoginStore) Update(server string, change func(Login, bool) (Login, bool)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockFile(s.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	f, err := s.read()
	if err != nil {
		return err
	}
	old, had := f.Logins[server]
	if l, keep := change(old, had); keep {
		f.Logins[server] = l
	} else if had {
		delete(f.Logins, server)
	} else {
		return nil
	}
	return s.write(f)
}
