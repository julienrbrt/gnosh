package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/filelock"
)

// State is what the user chose inside gnosh, kept between sessions: the
// models picked with /model, the thinking level picked for each model with
// /think, /no_think or /model, and the options set with /config. It
// lives in the user's state directory, never in a project, and it outranks
// the user's gnosh.json: a choice made inside gnosh sticks. A model given
// on the command line is never written here.
type State struct {
	// Model and SmallModel are the models chosen, as provider/id: the
	// session's model, and the one for the short side jobs. Empty means no
	// choice.
	Model, SmallModel string
	// Thinking is the level chosen for each model, keyed by provider/id.
	Thinking map[string]string
	// AutoCompactWindow is the window chosen, in tokens. Zero means no
	// choice. (It is the option of that name: /config writes it here.)
	AutoCompactWindow int
	// Options are the other options set with /config, each under its key
	// as the JSON value a config file's "options" would hold. They are
	// read as gnosh.json's are.
	Options map[string]json.RawMessage
	// ReviewMaxFindings is how many findings /code-review reports, chosen
	// with --max-findings: a number, or "all". Empty means no choice: as
	// many as the review's level asks.
	ReviewMaxFindings string

	// extra holds keys this gnosh does not know, so that writing the file
	// back does not drop what another version put there.
	extra map[string]json.RawMessage
	// read are the options as the file held them when it was read: one
	// that is written back unchanged is not checked again, so an option
	// another version of gnosh saved, which this one does not know, does
	// not stop every later choice from being saved (the settings-commands
	// review of 2026-11-20).
	read map[string]json.RawMessage
}

// stateVersion is the version written to the state file.
const stateVersion = 1

// stateFile is the state file's shape on disk. The two slots of "models"
// have the names of the config's two keys for the same choices.
type stateFile struct {
	Version int `json:"version"`
	Models  struct {
		Model      string `json:"model,omitempty"`
		SmallModel string `json:"smallModel,omitempty"`
	} `json:"models"`
	Thinking          map[string]string          `json:"thinking,omitempty"`
	AutoCompactWindow int                        `json:"autoCompactWindow,omitempty"`
	Options           map[string]json.RawMessage `json:"options,omitempty"`
	ReviewMaxFindings string                     `json:"reviewMaxFindings,omitempty"`
}

// stateKeys are the keys of stateFile, which extra never holds.
var stateKeys = []string{"version", "models", "thinking", "autoCompactWindow", "options", "reviewMaxFindings"}

// ReadState reads the state file. A file that is not there gives an empty
// State. A file that cannot be read or understood also gives an empty
// State, with a warning that says so: what gnosh wrote itself must never
// stop it from starting.
func ReadState(fsys FS, path string) (State, []Problem) {
	data, err := fsys.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	warn := func(format string, args ...any) (State, []Problem) {
		return State{}, []Problem{{Severity: Warning, File: path, Message: fmt.Sprintf(format, args...)}}
	}
	if err != nil {
		return warn("the state file could not be read (%v), so the model and thinking level chosen earlier are not in force. Choose them again with /model.", err)
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil {
		return warn("the state file is not JSON gnosh can read, so the model and thinking level chosen earlier are not in force. Choosing them again with /model writes a new file.")
	}
	st := State{Model: f.Models.Model, SmallModel: f.Models.SmallModel, Thinking: f.Thinking, AutoCompactWindow: f.AutoCompactWindow, Options: f.Options, read: maps.Clone(f.Options)}
	if ValidReviewMaxFindings(f.ReviewMaxFindings) {
		// (One this gnosh cannot read is left out, not an error: /code-
		// review then reports as its level asks.)
		st.ReviewMaxFindings = f.ReviewMaxFindings
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) == nil {
		for _, k := range stateKeys {
			delete(all, k)
		}
		if len(all) > 0 {
			st.extra = all
		}
	}
	return st, nil
}

// check says what is wrong with a State that is about to be written, or
// nil. A writer is held to what the reader accepts.
func (st State) check() error {
	for _, m := range []string{st.Model, st.SmallModel} {
		if m != "" && !validModelRef(m) {
			return fmt.Errorf("%q is not a model name of the form provider/id", m)
		}
	}
	for m, level := range st.Thinking {
		if !validModelRef(m) {
			return fmt.Errorf("%q is not a model name of the form provider/id", m)
		}
		if !levelPattern.MatchString(level) {
			return fmt.Errorf("%q is not a thinking level", level)
		}
	}
	if w := st.AutoCompactWindow; w != 0 && (w < 100000 || w > 1000000) {
		return fmt.Errorf("the auto-compact window %d is not between 100000 and 1000000", w)
	}
	if !ValidReviewMaxFindings(st.ReviewMaxFindings) {
		return fmt.Errorf("%q is not a number of findings (or all)", st.ReviewMaxFindings)
	}
	for key, raw := range st.Options {
		if key == "autoCompactWindow" {
			return errors.New("autoCompactWindow has a slot of its own")
		}
		if was, ok := st.read[key]; ok && bytes.Equal(was, raw) {
			// As it was on disk: carried through, whether or not this
			// gnosh knows it.
			continue
		}
		if err := CheckOption(key, raw); err != nil {
			return err
		}
	}
	return nil
}

// ValidReviewMaxFindings reports whether s is a number of findings for
// /code-review to report: a whole number from 1 to 1000, "all", or ""
// for no choice.
func ValidReviewMaxFindings(s string) bool {
	if s == "" || s == "all" {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 1000 && strconv.Itoa(n) == s
}

// validModelRef reports whether s has the form provider/id.
func validModelRef(s string) bool {
	prov, id, ok := strings.Cut(s, "/")
	return ok && idPattern.MatchString(prov) && id != "" && !strings.ContainsAny(id, " \t\n")
}

// WriteState writes the state file in one step, making its directory when
// it is not there. It refuses a State the reader would not accept, and
// then nothing is written.
func WriteState(fsys WriteFS, path string, st State) error {
	if err := st.check(); err != nil {
		return fmt.Errorf("the state file %s was not written: %w", path, err)
	}
	var f stateFile
	f.Version = stateVersion
	f.Models.Model, f.Models.SmallModel = st.Model, st.SmallModel
	f.Thinking, f.AutoCompactWindow, f.Options, f.ReviewMaxFindings = st.Thinking, st.AutoCompactWindow, st.Options, st.ReviewMaxFindings
	known, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("the state file %s was not written: %w", path, err)
	}
	all := map[string]json.RawMessage{}
	if err := json.Unmarshal(known, &all); err != nil {
		return fmt.Errorf("the state file %s was not written: %w", path, err)
	}
	for k, v := range st.extra {
		if !slices.Contains(stateKeys, k) {
			all[k] = v
		}
	}
	// A map is written with its keys in order, so the same State always
	// gives the same bytes.
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("the state file %s was not written: %w", path, err)
	}
	if err := fsys.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("the state file %s was not written: its directory could not be made: %w", path, err)
	}
	if err := fsys.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("the state file %s was not written: %w", path, err)
	}
	return nil
}

// UpdateState changes the state file: it reads the file as it is now,
// hands the State to change, and writes the result, one gnosh at a time
// (a lock beside the file, on the machine's own disk). Reading again just
// before writing keeps what another session saved a moment ago. If the
// file could not be understood, change starts from an empty State and the
// unreadable file is replaced; a file that is there and cannot be read is
// left as it is, and nothing is written, since what it holds would be
// lost (the settings-commands review of 2026-11-20: two gnosh saving at
// once lost one choice, and a file gnosh could not read was replaced
// whole by the one new choice).
func UpdateState(fsys WriteFS, path string, change func(*State)) error {
	if _, ok := fsys.(OS); ok {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("the state file %s was not written: its directory could not be made: %w", path, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		release, err := filelock.File(ctx, path+".lock")
		if err != nil {
			return fmt.Errorf("the state file %s is being written by another gnosh: %w", path, err)
		}
		defer release()
	}
	if _, err := fsys.ReadFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the state file %s could not be read (%v), so it was not written: what it holds would be lost", path, err)
	}
	st, _ := ReadState(fsys, path)
	st.Thinking = maps.Clone(st.Thinking)
	st.Options = maps.Clone(st.Options)
	change(&st)
	return WriteState(fsys, path, st)
}

// SetModel records a model choice for a slot, "model" or "smallModel". An
// empty ref clears the choice.
func (st *State) SetModel(slot string, ref ModelRef) error {
	name := ""
	if ref.ID != "" {
		name = ref.String()
		if !validModelRef(name) {
			return fmt.Errorf("%q is not a model name of the form provider/id", name)
		}
	}
	switch slot {
	case "model":
		st.Model = name
	case "smallModel":
		st.SmallModel = name
	default:
		return fmt.Errorf("%q is not a model slot; the slots are model and smallModel", slot)
	}
	return nil
}

// SetThinking records the thinking level chosen for a model. An empty
// level clears the choice.
func (st *State) SetThinking(ref ModelRef, level string) error {
	name := ref.String()
	if !validModelRef(name) {
		return fmt.Errorf("%q is not a model name of the form provider/id", name)
	}
	if level == "" {
		delete(st.Thinking, name)
		return nil
	}
	if !levelPattern.MatchString(level) {
		return fmt.Errorf("%q is not a thinking level", level)
	}
	if st.Thinking == nil {
		st.Thinking = map[string]string{}
	}
	st.Thinking[name] = level
	return nil
}

// SetOption records the value set with /config for an option, as a config
// file's "options" would hold it (ParseOption makes one from what a person
// typed). A nil value takes the choice away, so that the config files
// decide again.
func (st *State) SetOption(key string, raw json.RawMessage) error {
	if raw != nil {
		if err := CheckOption(key, raw); err != nil {
			return err
		}
	}
	if key == "autoCompactWindow" {
		st.AutoCompactWindow = 0
		if raw != nil {
			if err := json.Unmarshal(raw, &st.AutoCompactWindow); err != nil {
				return err
			}
		}
		return nil
	}
	if raw == nil {
		delete(st.Options, key)
		return nil
	}
	if st.Options == nil {
		st.Options = map[string]json.RawMessage{}
	}
	st.Options[key] = raw
	return nil
}
