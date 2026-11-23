package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	// The real file system, in a directory of the test's own: the write
	// must be a rename, and must make its directory.
	path := filepath.Join(t.TempDir(), "state", "gnosh", "state.json")
	want := State{Model: "qwen/qwen3.8-27b", SmallModel: "qwen/qwen3.8-27b", Thinking: map[string]string{"qwen/qwen3.8-27b": "off", "gptoss/gpt-oss-120b": "low"}, AutoCompactWindow: 200000}
	if err := WriteState(OS{}, path, want); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	got, problems := ReadState(OS{}, path)
	if len(problems) != 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v with %v, want %+v", got, problems, want)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the state file is the user's alone: %v %v", info.Mode(), err)
	}
	// Nothing is left beside it: the temporary file was renamed into
	// place.
	names, _ := os.ReadDir(filepath.Dir(path))
	if len(names) != 1 || names[0].Name() != "state.json" {
		t.Errorf("files left in the state directory: %v", names)
	}
	// Writing the same State gives the same bytes.
	first, _ := os.ReadFile(path)
	// The two slots are written under the names of the config's two keys,
	// "model" and "smallModel" (spec/DECISIONS.md, 26).
	if text := string(first); !strings.Contains(text, `"model": "qwen/qwen3.8-27b"`) || !strings.Contains(text, `"smallModel": "qwen/qwen3.8-27b"`) {
		t.Errorf("the state file does not hold its two slots as model and smallModel:\n%s", text)
	}
	if err := WriteState(OS{}, path, want); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("the same State gave different bytes:\n%s\n%s", first, second)
	}

	// A file that is not there is an empty State and no problem.
	got, problems = ReadState(OS{}, filepath.Join(t.TempDir(), "nope.json"))
	if len(problems) != 0 || !reflect.DeepEqual(got, State{}) {
		t.Errorf("no file: %+v %v", got, problems)
	}

	// The slots had other names once. A file that still has them holds no
	// choice: gnosh alone writes this file, and nothing is carried over.
	old := newFS().put(statePath, `{"version": 1, "models": {"large": "p/m", "small": "p/s"}}`)
	if got, problems := ReadState(old, statePath); len(problems) != 0 || got.Model != "" || got.SmallModel != "" {
		t.Errorf("a state file with the earlier slot names: %+v %v", got, problems)
	}
}

// A state file written by another version of gnosh keeps the keys this
// one does not know.
func TestStateKeepsUnknownKeys(t *testing.T) {
	fsys := newFS().put(statePath, `{"version": 1, "models": {"model": "p/m"}, "recentModels": ["p/a", "p/b"], "future": {"x": 1}}`)
	err := UpdateState(fsys, statePath, func(st *State) {
		if st.Model != "p/m" {
			t.Errorf("UpdateState hands over the file as it is: %+v", st)
		}
		if err := st.SetThinking(ModelRef{Provider: "p", ID: "m"}, "off"); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	data, _ := fsys.ReadFile(statePath)
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatalf("the state file is not JSON: %v\n%s", err, data)
	}
	if string(all["recentModels"]) != `["p/a","p/b"]` && !strings.Contains(string(all["recentModels"]), `"p/a"`) || all["future"] == nil {
		t.Errorf("unknown keys were dropped: %s", data)
	}
	st, _ := ReadState(fsys, statePath)
	if st.Model != "p/m" || st.Thinking["p/m"] != "off" {
		t.Errorf("after the update: %+v", st)
	}
}

// An option another version of gnosh saved, which this one does not know,
// is carried through when a choice is saved, as it was on disk, and does
// not stop the choice from being saved; one being changed is checked
// (the settings-commands review of 2026-11-20).
func TestStateCarriesAnOptionItDoesNotKnow(t *testing.T) {
	fsys := newFS().put(statePath, `{"version": 1, "models": {}, "options": {"oldOption": 3, "debug": true}}`)
	if err := UpdateState(fsys, statePath, func(st *State) { _ = st.SetModel("model", ModelRef{Provider: "p", ID: "m"}) }); err != nil {
		t.Fatalf("a choice was not saved beside an option this gnosh does not know: %v", err)
	}
	st, _ := ReadState(fsys, statePath)
	if st.Model != "p/m" || string(st.Options["oldOption"]) != "3" {
		t.Errorf("after the save: %+v", st)
	}
	err := UpdateState(fsys, statePath, func(st *State) { st.Options["oldOption"] = json.RawMessage("4") })
	if err == nil || !strings.Contains(err.Error(), "not an option gnosh knows") {
		t.Errorf("a changed unknown option was written: %v", err)
	}
	if err := UpdateState(fsys, statePath, func(st *State) { _ = st.SetOption("oldOption", nil) }); err != nil {
		t.Errorf("an unknown option could not be taken off: %v", err)
	}
	if st, _ := ReadState(fsys, statePath); st.Options["oldOption"] != nil || string(st.Options["debug"]) != "true" {
		t.Errorf("after taking it off: %+v", st)
	}
}

// What gnosh wrote itself never stops it from starting: a state file that
// cannot be understood, or that names things that are gone, is passed
// over with a word.
func TestStaleStateNeverStopsStartup(t *testing.T) {
	const text = "{" + one + `, "models": {"p/m": {"thinkingLevels": ["off", "on"]}}, "model": "p/m"}`
	cases := []struct {
		name, state string
		pieces      []string
		check       func(*Config)
	}{
		{"not JSON", "{{{", []string{"not JSON gnosh can read", "not in force"}, nil},
		{"the wrong shape", `{"models": "p/m"}`, []string{"not JSON gnosh can read"}, nil},
		{"a provider that is gone", `{"models": {"model": "gone/m"}}`, []string{"gone/m", "provider that is not configured", "Choose a model"},
			func(c *Config) {
				if m := c.Large.Model; m.V.String() != "p/m" || m.Origin.Layer != LayerUser {
					t.Errorf("a stale choice is dropped, and the user's own file counts again: %+v", m)
				}
			}},
		{"a model name of the wrong form", `{"models": {"smallModel": "justaname"}}`, []string{"not of the form provider/id"}, nil},
		{"a window out of range", `{"autoCompactWindow": 7}`, []string{"not between 100000 and 1000000"}, nil},
		{"a level the model does not have", `{"thinking": {"p/m": "extreme"}}`, []string{"extreme", "not one of that model's levels"},
			func(c *Config) {
				if c.Large.Thinking.V != "on" || c.Large.Thinking.Set {
					t.Errorf("a stale level is dropped: %+v", c.Large.Thinking)
				}
			}},
		{"a level that is not a word", `{"thinking": {"p/m": "LOUD!"}}`, []string{"not a level gnosh can read"}, nil},
		// What /config set is read as gnosh.json's options are, and one
		// this gnosh cannot use is left out, the others kept.
		{"an option this gnosh does not have", `{"options": {"fancy": true, "debug": true}}`, []string{"fancy", "set here with /config was not used", "not an option gnosh knows"},
			func(c *Config) {
				if !c.Options.Debug.V || c.Options.Debug.Origin.Layer != LayerState {
					t.Errorf("the option beside it was dropped too: %+v", c.Options.Debug)
				}
			}},
		{"an option's value out of range", `{"options": {"maxBackgroundTasks": 0}}`, []string{"maxBackgroundTasks", "set here with /config was not used", "from 1 to 100000"},
			func(c *Config) {
				if c.Options.MaxBackgroundTasks.V != 50 || c.Options.MaxBackgroundTasks.Set {
					t.Errorf("a stale option is dropped: %+v", c.Options.MaxBackgroundTasks)
				}
			}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg, problems := load(t, inputs(newFS().put(userJSON, text).put(statePath, tt.state)))
			if cfg == nil {
				t.Fatalf("a stale state file stopped startup:\n%s", showProblems(problems))
			}
			wantProblem(t, problems, Warning, statePath, 0, tt.pieces...)
			if tt.check != nil {
				tt.check(cfg)
			}
		})
	}
}

// The thinking level chosen inside gnosh is kept per model, and sits
// between the user's gnosh.json and the project's.
func TestStateThinking(t *testing.T) {
	const text = "{" + one + `, "model": {"id": "p/m", "thinking": true}}`
	fsys := newFS().put(userJSON, text).put(statePath, `{"thinking": {"p/m": "off", "p/other": "low"}}`)
	cfg := mustLoad(t, inputs(fsys))
	if th := cfg.Large.Thinking; th.V != "off" || th.Origin.Layer != LayerState {
		t.Errorf("state over the user's gnosh.json: %+v", th)
	}
	fsys.put(projJSON, `{"model": {"thinking": true}}`)
	cfg = mustLoad(t, inputs(fsys))
	if th := cfg.Large.Thinking; th.V != "on" || th.Origin.Layer != LayerProject {
		t.Errorf("the project's gnosh.json over state: %+v", th)
	}
	in := inputs(fsys)
	in.Flags.Model = "p/other"
	cfg = mustLoad(t, in)
	if th := cfg.Large.Thinking; th.V != "on" {
		// The project's file set the level, which outranks the state.
		t.Errorf("another model, the project's level still holds: %+v", th)
	}
	in = inputs(newFS().put(userJSON, "{"+one+"}").put(statePath, `{"thinking": {"p/m": "off", "p/other": "low"}}`))
	in.Flags.Model = "p/other"
	cfg = mustLoad(t, in)
	if th := cfg.Large.Thinking; th.V != "low" || th.Origin.Layer != LayerState {
		t.Errorf("the level saved for the model in use: %+v", th)
	}
}

// A writer is held to what the reader accepts: a State the loader would
// drop is not written.
func TestWriteStateRefusesWhatTheReaderWould(t *testing.T) {
	fsys := newFS()
	for name, st := range map[string]State{
		"a model with no provider": {Model: "justaname"},
		"a level that is no word":  {Thinking: map[string]string{"p/m": "LOUD!"}},
		"a window out of range":    {AutoCompactWindow: 5},
	} {
		if err := WriteState(fsys, statePath, st); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	if _, err := fsys.Stat(statePath); err == nil {
		t.Errorf("a refused State left a file behind")
	}
	var st State
	if err := st.SetModel("medium", ModelRef{Provider: "p", ID: "m"}); err == nil {
		t.Errorf("a slot other than model or smallModel was taken")
	}
	if err := st.SetModel("model", ModelRef{ID: "m"}); err == nil {
		t.Errorf("a model with no provider was taken")
	}
	if err := st.SetModel("model", ModelRef{Provider: "p", ID: "m"}); err != nil || st.Model != "p/m" {
		t.Errorf("SetModel: %v %+v", err, st)
	}
	if err := st.SetModel("model", ModelRef{}); err != nil || st.Model != "" {
		t.Errorf("an empty model clears the choice: %v %+v", err, st)
	}
	if err := st.SetThinking(ModelRef{Provider: "p", ID: "m"}, "LOUD"); err == nil {
		t.Errorf("a level that is no word was taken")
	}
}

// Several sessions saving at once never leave a file that cannot be read,
// and none loses another's choice: they write one at a time (the
// settings-commands review of 2026-11-20).
func TestStateWritersAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				err := UpdateState(OS{}, path, func(st *State) {
					_ = st.SetThinking(ModelRef{Provider: "p", ID: string(rune('a' + i))}, "off")
				})
				if err != nil {
					t.Error(err)
				}
				if _, problems := ReadState(OS{}, path); len(problems) != 0 {
					t.Errorf("a reader saw a state file it could not read: %v", problems)
				}
			}
		}()
	}
	wg.Wait()
	st, problems := ReadState(OS{}, path)
	if len(problems) != 0 || len(st.Thinking) != 8 {
		t.Errorf("after the writers, a choice was lost: %+v %v", st, problems)
	}
	names, _ := os.ReadDir(filepath.Dir(path))
	var left []string
	for _, n := range names {
		if n.Name() != "state.json" && n.Name() != "state.json.lock" {
			left = append(left, n.Name())
		}
	}
	if len(left) != 0 {
		t.Errorf("temporary files were left behind: %v", left)
	}
}

// A state file that is there and cannot be read is left as it is, and
// nothing is written: replacing it would lose what it holds.
func TestStateNotReadNotReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.Mkdir(path, 0o700); err != nil { // (Cannot be read, even by root.)
		t.Fatal(err)
	}
	err := UpdateState(OS{}, path, func(st *State) { _ = st.SetModel("model", ModelRef{Provider: "p", ID: "m"}) })
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("UpdateState over a file it cannot read: %v", err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Errorf("what was there was replaced: %v %v", info, err)
	}
}

// What /config sets is kept in the state file, read as a config file's
// options are, and sits between the user's gnosh.json and the project's
// files.
func TestStateOptions(t *testing.T) {
	var st State
	for _, set := range []struct{ key, text string }{{"debug", "true"}, {"memoryText", "full"}, {"autoCompactWindow", "200000"}, {"requestTimeout", "'120'"}} {
		raw, err := ParseOption(set.key, set.text)
		if err != nil {
			t.Fatalf("ParseOption(%s, %s): %v", set.key, set.text, err)
		}
		if err := st.SetOption(set.key, raw); err != nil {
			t.Fatalf("SetOption(%s): %v", set.key, err)
		}
	}
	// The window has its slot of old; the others are options.
	if st.AutoCompactWindow != 200000 || len(st.Options) != 3 || string(st.Options["memoryText"]) != `"full"` || string(st.Options["requestTimeout"]) != "120" {
		t.Fatalf("the state: %+v %s", st, st.Options)
	}
	if err := st.SetOption("requestTimeout", nil); err != nil || len(st.Options) != 2 {
		t.Errorf("taking a choice away: %v %s", err, st.Options)
	}
	if err := st.SetOption("memoryText", json.RawMessage(`"fulll"`)); err == nil || !strings.Contains(err.Error(), "lean") || string(st.Options["memoryText"]) != `"full"` {
		t.Errorf("a value the reader would refuse: %v, and the state holds %s", err, st.Options["memoryText"])
	}
	fsys := newFS()
	if err := WriteState(fsys, statePath, st); err != nil {
		t.Fatal(err)
	}
	got, problems := ReadState(fsys, statePath)
	got.read = nil // (what the file held, for the next write's check)
	if len(problems) != 0 || !reflect.DeepEqual(got, st) {
		t.Errorf("read back %+v with %v, want %+v", got, problems, st)
	}
	// The state outranks the user's file, and the project's file outranks
	// the state.
	fsys.put(userJSON, "{"+one+`, "options": {"debug": false, "memoryText": "lean", "loopMaxTurns": 5}}`)
	fsys.put(projJSON, `{"options": {"memoryText": "lean"}}`)
	cfg := mustLoad(t, inputs(fsys))
	o := cfg.Options
	if !o.Debug.V || o.Debug.Origin.Layer != LayerState || o.Debug.Origin.File != statePath || o.Debug.Origin.Line != 0 {
		t.Errorf("debug: %+v", o.Debug)
	}
	if o.MemoryText.V != "lean" || o.MemoryText.Origin.File != projJSON {
		t.Errorf("memoryText: the project's file wins: %+v", o.MemoryText)
	}
	if o.LoopMaxTurns.V != 5 || o.LoopMaxTurns.Origin.Layer != LayerUser {
		t.Errorf("loopMaxTurns: what the state does not hold stays the user's: %+v", o.LoopMaxTurns)
	}
	if o.AutoCompactWindow.V != 200000 || o.AutoCompactWindow.Origin.Layer != LayerState {
		t.Errorf("autoCompactWindow: %+v", o.AutoCompactWindow)
	}
	rows := o.All()
	if len(rows) != len(optionDefs) {
		t.Fatalf("%d rows for %d options", len(rows), len(optionDefs))
	}
	for _, r := range rows {
		switch r.Key {
		case "debug":
			if r.Text != "true" || string(r.JSON) != "true" || !r.Set || r.Takes != "true or false" {
				t.Errorf("the row of debug: %+v", r)
			}
		case "maxBackgroundTasks":
			if r.Text != "50" || r.Set || r.JSON != nil || r.Takes != "a whole number from 1 to 100000" {
				t.Errorf("the row of an option nothing set: %+v", r)
			}
		case "hiddenSkills":
			if !r.List || r.Set || r.Text != "none" {
				t.Errorf("the row of a list: %+v", r)
			}
		}
	}
}

// ParseOption reads what a person types after /config key= as the JSON
// value gnosh.json would hold, and refuses what gnosh.json's reader would,
// in its words.
func TestParseOption(t *testing.T) {
	for _, tt := range []struct{ key, text, want, wrong string }{
		{"debug", "true", "true", ""},
		{"debug", "false", "false", ""},
		{"debug", "yes", "", "true or false"},
		{"maxBackgroundTasks", "12", "12", ""},
		{"maxBackgroundTasks", "0", "", "from 1 to 100000"},
		{"maxBackgroundTasks", "twelve", "", "whole number"},
		{"memoryText", "full", `"full"`, ""},
		{"memoryText", `"full"`, `"full"`, ""},
		{"memoryText", "short", "", `"lean", "full"`},
		{"memEmbeddingsUrl", "http://127.0.0.1:8091/v1", `"http://127.0.0.1:8091/v1"`, ""},
		{"memEmbeddingsUrl", "ftp://x", "", "http:// or https://"},
		{"deliverablesServer", "off", `"off"`, ""},
		{"deliverablesServer", "0.0.0.0:80", "", "this machine alone"},
		{"projectDataDir", "'my data'", `"my data"`, ""},
		{"hiddenSkills", "x", "", "a list is set in gnosh.json"},
		{"fancy", "1", "", "not an option gnosh knows"},
	} {
		raw, err := ParseOption(tt.key, tt.text)
		switch {
		case tt.wrong == "" && (err != nil || string(raw) != tt.want):
			t.Errorf("ParseOption(%s, %s) = %s, %v; want %s", tt.key, tt.text, raw, err, tt.want)
		case tt.wrong != "" && (err == nil || !strings.Contains(err.Error(), tt.wrong)):
			t.Errorf("ParseOption(%s, %s) = %s, %v; want a mistake that says %q", tt.key, tt.text, raw, err, tt.wrong)
		}
	}
	// Only one JSON value is a value: nothing can be written past it.
	if err := CheckOption("debug", json.RawMessage(`true}, "providers": {"x": 1`)); err == nil {
		t.Error("CheckOption took more than one JSON value")
	}
}
