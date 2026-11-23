package agentdef

import (
	"strings"
	"testing"
)

// --agents gives definitions as JSON: the settings of a definition file,
// prompt for its body, and the scope "command line".
func TestFromJSON(t *testing.T) {
	defs, err := FromJSON(`{
		"reviewer": {"description": "Reviews a change.", "prompt": "You review code.", "tools": "Read, Grep(*.go), Agent(helper, x)", "model": "inherit", "maxTurns": 5, "color": "red"},
		"writer": {"description": "Writes the docs.", "prompt": "", "tools": "Read, Edit", "disallowedTools": "Bash", "background": true, "hooks": {}, "skills": ["style", "terms"], "effort": "max", "initialPrompt": "Begin."}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 || defs[0].Name != "reviewer" || defs[1].Name != "writer" {
		t.Fatalf("defs = %+v", defs)
	}
	r, w := defs[0], defs[1]
	if r.Description != "Reviews a change." || r.Prompt != "You review code." || !r.ToolsSet || strings.Join(r.Tools, ",") != "Read,Grep,Agent" ||
		strings.Join(r.AgentTypes, ",") != "helper,x" || r.Model != "" || !r.InheritModel || r.MaxTurns != 5 || r.Scope != ScopeCommandLine || r.Source != "--agents" {
		t.Errorf("reviewer = %+v", r)
	}
	if strings.Join(w.Tools, ",") != "Read,Edit" || strings.Join(w.Disallowed, ",") != "Bash" || !w.Background || w.Hooks != nil ||
		strings.Join(w.Skills, ",") != "style,terms" || w.Effort != "max" || w.InitialPrompt != "Begin." {
		t.Errorf("writer = %+v", w)
	}
	for text, want := range map[string]string{
		`[1, 2]`:                       "not a JSON object of agents by name",
		`{"a": 3}`:                     `the agent "a" is not a JSON object`,
		`{"a": {"prompt": "x"}}`:       `the agent "a" has no description`,
		`{"-a": {"description": "x"}}`: "cannot be an agent's",
		`{"a": {"description": "x", "maxTurns": 0}}`:    "a number of turns",
		`{"a": {"description": "x", "tools": 7}}`:       "a list of tool names",
		`{"a": {"description": "x", "skills": 7}}`:      "a list of skill names",
		`{"a": {"description": "x", "hooks": [1]}}`:     "a map of events to their hooks",
		`{"a": {"description": "x", "effort": 7}}`:      "a string",
		`{"a": {"description": "x", "temperature": 1}}`: `"temperature", which is not a setting of an agent`,
	} {
		if _, err := FromJSON(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("FromJSON(%s) = %v, want an error with %q", text, err, want)
		}
	}
}

// A definition given with --agents is used in place of one of the same
// name from a file or gnosh's own, and listed after them.
func TestOver(t *testing.T) {
	base := LoadFrom(nil, nil, nil)
	defs, err := FromJSON(`{"Explore": {"description": "My own explorer."}, "extra": {"description": "One more."}}`)
	if err != nil {
		t.Fatal(err)
	}
	s := Over(base, defs)
	if d, _ := s.Get("Explore"); d.Description != "My own explorer." || d.Scope != ScopeCommandLine {
		t.Errorf("Explore = %+v", d)
	}
	if _, ok := s.Get(DefaultType); !ok {
		t.Errorf("the default kind is gone")
	}
	names := s.Names()
	if names[len(names)-1] != "extra" && names[len(names)-2] != "extra" {
		t.Errorf("names = %v", names)
	}
}
