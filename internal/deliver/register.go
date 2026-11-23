package deliver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/filelock"
)

// The names of what the feature keeps: its folder in the project's data
// folder, the register and the copies inside it, and the folder and the
// key in gnosh's own data directory.
const (
	folderName   = "deliverables"
	registerName = "register.json"
	copiesName   = "files"
	stateName    = "deliver"
	keyName      = "key"
)

// tokenBytes is how many random bytes a token is made of.
const tokenBytes = 32

// record is one entry of the register: a token, and the file it stands
// for.
type record struct {
	// Token is what an address holds in place of a path.
	Token string `json:"token"`
	// Name is the file's own name, which an address ends with.
	Name string `json:"name"`
	// Host and Path say which file was delivered: the path as the list
	// shows it, and the declared machine it is on, or none for this one.
	// Together they are what an entry is found by when the same file is
	// delivered again.
	Host string `json:"host,omitempty"`
	Path string `json:"path"`
	// File is the file on this machine that is served: the delivered file
	// itself with its links followed, or the copy of a file from a host.
	File string `json:"file"`
	// Label is the label it was last delivered under.
	Label string `json:"label,omitempty"`
	// Added and Updated say when it was first delivered and last.
	Added   time.Time `json:"added"`
	Updated time.Time `json:"updated"`
	// Seal is the HMAC of the fields above it that say what is served,
	// under the account's key.
	Seal string `json:"seal"`
}

// registerFile is the register as it is written.
type registerFile struct {
	Version int      `json:"version"`
	Files   []record `json:"files"`
}

// register is which token stands for which file, for one project. It is a
// file in the project's data folder, read again whenever it has changed:
// another session of the same project may be adding to it.
type register struct {
	dir  string // the feature's folder in the project's data folder
	key  []byte // the account's key
	rand io.Reader
	now  func() time.Time

	mu sync.Mutex
	// read says the file has been looked at; info is the file as it then
	// was, and gone that there was none.
	read bool
	gone bool
	info os.FileInfo
	// all is every entry of the file, and sealed, by token, the ones this
	// account's gnosh wrote: only those are served.
	all    []record
	sealed map[string]record
}

// newRegister makes the register of the folder dir, whose entries are
// sealed with key. Nothing is read or written until it is used.
func newRegister(dir string, key []byte, random io.Reader, now func() time.Time) *register {
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &register{dir: dir, key: key, rand: random, now: now}
}

// path is the register's file.
func (r *register) path() string { return filepath.Join(r.dir, registerName) }

// seal is the HMAC that says an entry was written by whoever holds the
// key: over the token and over everything that decides what the token
// serves, each with its length before it.
func (r *register) seal(rec record) string {
	mac := hmac.New(sha256.New, r.key)
	for _, field := range []string{rec.Token, rec.Name, rec.Host, rec.Path, rec.File} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(field)))
		mac.Write(n[:])
		mac.Write([]byte(field))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// load reads the register's file when it is not as it was last read. It
// must be called with mu held. A file that cannot be read or is not the
// register's leaves the register empty: nothing is served from a file
// gnosh cannot make sense of.
func (r *register) load() error {
	st, err := os.Lstat(r.path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.read, r.gone, r.all, r.sealed = true, true, nil, nil
		return nil
	case err != nil:
		return fmt.Errorf("the register of delivered files, %s, cannot be looked at: %w", r.path(), err)
	case !st.Mode().IsRegular() || st.Size() > maxRegister:
		// A link (to /dev/zero, to a terminal), a pipe, or a file far
		// bigger than one gnosh writes: a cloned repository may hold
		// any of them there, and reading one would never end, or fill
		// the memory, as every session in the folder starts (the
		// deliverables review of 2026-11-19).
		r.read, r.gone, r.all, r.sealed = false, false, nil, nil
		return fmt.Errorf("the register of delivered files, %s, is not a file gnosh wrote (it is %s), so nothing in it is served. Move it away and deliver the files again", r.path(), registerKind(st))
	}
	if r.read && !r.gone && os.SameFile(st, r.info) && st.Size() == r.info.Size() && st.ModTime().Equal(r.info.ModTime()) {
		return nil
	}
	data, err := readRegister(r.path(), st)
	if err != nil {
		return fmt.Errorf("the register of delivered files, %s, cannot be read: %w", r.path(), err)
	}
	var file registerFile
	if err := json.Unmarshal(data, &file); err != nil {
		r.read, r.gone, r.all, r.sealed = false, false, nil, nil
		return fmt.Errorf("the register of delivered files, %s, is not the JSON gnosh wrote (%v), so nothing in it is served. Move it away and deliver the files again", r.path(), err)
	}
	r.read, r.gone, r.info = true, false, st
	r.all, r.sealed = file.Files, make(map[string]record, len(file.Files))
	for _, rec := range file.Files {
		if rec.Token == "" || rec.File == "" || !hmac.Equal([]byte(rec.Seal), []byte(r.seal(rec))) {
			continue
		}
		r.sealed[rec.Token] = rec
	}
	return nil
}

// byToken returns the entry a token stands for, when there is one that
// this account's gnosh wrote.
func (r *register) byToken(token string) (record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.load() != nil {
		return record{}, false
	}
	rec, ok := r.sealed[token]
	return rec, ok
}

// byTarget returns the entry of a delivered file: the one with that path
// on that machine.
func (r *register) byTarget(host, path string) (record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.load() != nil {
		return record{}, false
	}
	for _, rec := range r.sealed {
		if rec.Host == host && rec.Path == path {
			return rec, true
		}
	}
	return record{}, false
}

// count says how many entries can be served, and how many more the file
// holds that this account's gnosh did not write.
func (r *register) count() (served, foreign int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return 0, 0, err
	}
	return len(r.sealed), len(r.all) - len(r.sealed), nil
}

// add records that the file at path on host, which may be empty for this
// machine, was delivered under label and is served from file. The same
// path on the same machine keeps the token it has, so that its address
// stays the same; a new one gets a new token. The register's file is
// written whole and renamed into place, under a lock that keeps two
// sessions of the project from writing over each other.
func (r *register) add(ctx context.Context, host, path, file, label string) (record, error) {
	if err := r.makeFolder(); err != nil {
		return record{}, err
	}
	release, err := filelock.File(ctx, r.path()+".lock")
	if err != nil {
		return record{}, fmt.Errorf("the register of delivered files is being written by another gnosh: %w", err)
	}
	defer release()

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return record{}, err
	}
	now := r.now().UTC()
	rec := record{Host: host, Path: path, Added: now}
	at := -1
	for i, old := range r.all {
		if _, ours := r.sealed[old.Token]; ours && old.Host == host && old.Path == path {
			rec, at = old, i
			break
		}
	}
	if rec.Token == "" {
		raw := make([]byte, tokenBytes)
		if _, err := io.ReadFull(r.rand, raw); err != nil {
			return record{}, fmt.Errorf("no token could be drawn for the file's address: %w", err)
		}
		rec.Token = base64.RawURLEncoding.EncodeToString(raw)
	}
	rec.Name, rec.File, rec.Label, rec.Updated = fileName(path), file, label, now
	rec.Seal = r.seal(rec)

	files := append([]record(nil), r.all...)
	if at >= 0 {
		files[at] = rec
	} else {
		files = append(files, rec)
	}
	return rec, r.write(files)
}

// drop takes the entry of a delivered file out of the register, and
// reports whether it had one: the file is not served any more. It is for
// a file whose copy on this machine is no longer the file, such as one
// that has grown past what is copied back from a host.
func (r *register) drop(ctx context.Context, host, path string) (bool, error) {
	if !exists(r.path()) {
		return false, nil
	}
	release, err := filelock.File(ctx, r.path()+".lock")
	if err != nil {
		return false, fmt.Errorf("the register of delivered files is being written by another gnosh: %w", err)
	}
	defer release()

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return false, err
	}
	var files []record
	for _, old := range r.all {
		if _, ours := r.sealed[old.Token]; ours && old.Host == host && old.Path == path {
			continue
		}
		files = append(files, old)
	}
	if len(files) == len(r.all) {
		return false, nil
	}
	return true, r.write(files)
}

// write writes the register's file whole, with these entries, and reads it
// back. It must be called with mu and the file's lock held.
func (r *register) write(files []record) error {
	if files == nil {
		files = []record{}
	}
	data, err := json.MarshalIndent(registerFile{Version: 1, Files: files}, "", "  ")
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(r.path(), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("the register of delivered files, %s, could not be written: %w", r.path(), err)
	}
	// What was just written is what the file holds: it is read again by
	// its new size and time.
	r.read = false
	return r.load()
}

// makeFolder makes the feature's folder. A project's data folder is never
// committed: when it is made here it is given the line that says so, as
// it is wherever gnosh makes it, and the feature's own folder gets one
// too, since it holds tokens.
func (r *register) makeFolder() error {
	project := filepath.Dir(r.dir)
	_, err := os.Stat(project)
	fresh := errors.Is(err, os.ErrNotExist)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return fmt.Errorf("the folder for delivered files, %s, could not be made: %w", r.dir, err)
	}
	if err := noLinks(filepath.Dir(r.dir), r.dir); err != nil {
		return err
	}
	if fresh {
		_ = os.WriteFile(filepath.Join(project, ".gitignore"), []byte("*\n"), 0o600)
	}
	if ignore := filepath.Join(r.dir, ".gitignore"); !exists(ignore) {
		_ = os.WriteFile(ignore, []byte("*\n"), 0o600)
	}
	return nil
}

// noLinks says why gnosh writes nothing in dir when a folder on the way
// from base down to it is a link: a repository can carry one in the data
// folder, and the register, its ignore file and the copies of delivered
// files would be written wherever it leads (the deliverables review of
// 2026-11-19). dir lies under base.
func noLinks(base, dir string) error {
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is not inside %s", dir, base)
	}
	at := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		at = filepath.Join(at, part)
		if st, err := os.Lstat(at); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("the folder %s is a link, and gnosh writes nothing for delivered files through one: remove the link", at)
		}
	}
	return nil
}

// copyPath is where the copy of a file from a host is kept: a folder of
// its own for each file, named after the machine and the path, so that
// the same file delivered again takes the place of its earlier copy and
// two files of one name do not meet.
func (r *register) copyPath(host, path string) string {
	sum := sha256.Sum256([]byte(host + "\x00" + path))
	return filepath.Join(r.dir, copiesName, hex.EncodeToString(sum[:12]), fileName(path))
}

// fileName is the name an address ends with: the last part of the path. A
// path on another machine is written with slashes, wherever gnosh runs.
func fileName(p string) string {
	name := path.Base(filepath.ToSlash(strings.TrimRight(p, "/")))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "file"
	}
	return name
}

// maxRegister is the most of a register's file that is read: one gnosh
// writes holds a few lines for each file delivered.
const maxRegister = 16 << 20

// registerKind says what is at the register's place instead of a file.
func registerKind(st os.FileInfo) string {
	switch m := st.Mode(); {
	case m&os.ModeSymlink != 0:
		return "a link"
	case m.IsDir():
		return "a folder"
	case !m.IsRegular():
		return "not a plain file"
	}
	return fmt.Sprintf("%d bytes, more than one gnosh writes", st.Size())
}

// readRegister reads the register's file, which st describes, up to
// maxRegister bytes, and only while it is still the file st describes.
func readRegister(path string, st os.FileInfo) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if now, err := f.Stat(); err != nil || !os.SameFile(st, now) {
		return nil, errors.New("it was changed while it was read")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRegister+1))
	if err == nil && len(data) > maxRegister {
		err = errors.New("it grew past what a register holds while it was read")
	}
	return data, err
}

// exists reports whether something is at path.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// loadKey returns the account's key: the secret that seals the entries of
// every project's register. It is kept in gnosh's own data directory,
// outside every project, in a file only the account can read, and is made
// the first time it is needed.
func loadKey(stateDir string, random io.Reader) ([]byte, error) {
	if random == nil {
		random = rand.Reader
	}
	dir := filepath.Join(stateDir, stateName)
	file := filepath.Join(dir, keyName)
	read := func() ([]byte, error) {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) < 32 {
			return nil, fmt.Errorf("%s does not hold the key gnosh wrote there. Remove it, and a new one is made; addresses handed out before then stop working", file)
		}
		return key, nil
	}
	key, err := read()
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return key, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the folder for the key that seals delivered files, %s, could not be made: %w", dir, err)
	}
	key = make([]byte, 32)
	if _, err := io.ReadFull(random, key); err != nil {
		return nil, fmt.Errorf("no key could be drawn to seal delivered files with: %w", err)
	}
	// The file is made only if it is not there: of two sessions that start
	// at once, one writes the key and the other reads it.
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return awaitKey(read)
	}
	if err != nil {
		return nil, fmt.Errorf("the key that seals delivered files could not be kept in %s: %w", file, err)
	}
	_, werr := f.WriteString(hex.EncodeToString(key) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(file)
		return nil, fmt.Errorf("the key that seals delivered files could not be kept in %s: %w", file, werr)
	}
	return key, nil
}

// awaitKey reads the key another session is writing at this moment. The
// other session has the file open and writes one short line into it, so
// the wait is a moment; should the line never come, the last reading's
// error says what is in the file.
func awaitKey(read func() ([]byte, error)) ([]byte, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		key, err := read()
		if err == nil || time.Now().After(deadline) {
			return key, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
