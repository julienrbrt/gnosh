package fstools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

func TestNewReturnsTheSixTools(t *testing.T) {
	tools := New(Options{})
	wantNames := []string{"Read", "Write", "Edit", "Glob", "Grep", "NotebookEdit"}
	wantArgs := map[string][]string{
		"Read":         {"file_path", "offset", "limit", "pages", "allow_large"},
		"Write":        {"file_path", "content"},
		"Edit":         {"file_path", "old_string", "new_string", "replace_all"},
		"Glob":         {"pattern", "path"},
		"Grep":         {"pattern", "path", "glob", "type", "output_mode", "-i", "-n", "-o", "-A", "-B", "-C", "context", "multiline", "head_limit", "offset"},
		"NotebookEdit": {"notebook_path", "cell_id", "new_source", "cell_type", "edit_mode"},
	}
	wantRequired := map[string][]string{
		"Read":         {"file_path"},
		"Write":        {"file_path", "content"},
		"Edit":         {"file_path", "old_string", "new_string"},
		"Glob":         {"pattern"},
		"Grep":         {"pattern"},
		"NotebookEdit": {"notebook_path", "new_source"},
	}
	if len(tools) != len(wantNames) {
		t.Fatalf("New returned %d tools, want %d", len(tools), len(wantNames))
	}
	for i, tool := range tools {
		spec := tool.Spec()
		if spec.Name != wantNames[i] {
			t.Errorf("tool %d is %q, want %q", i, spec.Name, wantNames[i])
		}
		if _, ok := tool.(core.Describer); !ok {
			t.Errorf("%s is not a core.Describer", spec.Name)
		}
		if len(spec.Description) < 200 {
			t.Errorf("%s has a description of %d bytes; the model needs more to go on", spec.Name, len(spec.Description))
		}
		if strings.Contains(spec.Description, "%!") {
			t.Errorf("%s: the description has a formatting mistake in it:\n%s", spec.Name, spec.Description)
		}
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
			Additional *bool                      `json:"additionalProperties"`
		}
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Errorf("%s: the schema is not JSON: %v\n%s", spec.Name, err, spec.InputSchema)
			continue
		}
		if schema.Type != "object" || schema.Additional == nil || *schema.Additional {
			t.Errorf("%s: the schema must be an object that takes no other properties: %s", spec.Name, spec.InputSchema)
		}
		if !reflect.DeepEqual(schema.Required, wantRequired[spec.Name]) {
			t.Errorf("%s: required = %v, want %v", spec.Name, schema.Required, wantRequired[spec.Name])
		}
		if len(schema.Properties) != len(wantArgs[spec.Name]) {
			t.Errorf("%s: %d properties, want %d", spec.Name, len(schema.Properties), len(wantArgs[spec.Name]))
		}
		// The arguments come in the order the model should read them.
		at := 0
		for _, name := range wantArgs[spec.Name] {
			raw, ok := schema.Properties[name]
			if !ok {
				t.Errorf("%s: no property %q", spec.Name, name)
				continue
			}
			var p struct {
				Type        string   `json:"type"`
				Description string   `json:"description"`
				Enum        []string `json:"enum"`
			}
			if err := json.Unmarshal(raw, &p); err != nil || p.Description == "" {
				t.Errorf("%s.%s: %v, description %q", spec.Name, name, err, p.Description)
			}
			if p.Type != "string" && p.Type != "integer" && p.Type != "boolean" {
				t.Errorf("%s.%s: type %q", spec.Name, name, p.Type)
			}
			if name == "output_mode" && !reflect.DeepEqual(p.Enum, []string{"files_with_matches", "content", "count"}) {
				t.Errorf("output_mode enum = %v", p.Enum)
			}
			i := strings.Index(string(spec.InputSchema[at:]), `"`+name+`":`)
			if i < 0 {
				t.Errorf("%s: property %q is out of order in %s", spec.Name, name, spec.InputSchema)
				continue
			}
			at += i
		}
	}
}

func TestDescriptionsStateTheLimits(t *testing.T) {
	o := Options{
		ReadDefaultLines: 1234, ReadMaxBytes: 56789, ReadMaxLineChars: 321,
		GlobMaxResults: 77, GrepHeadLimit: 55, GrepMaxOutputBytes: 3 << 10,
		ReadName: "View", WriteName: "Create", EditName: "Change", GlobName: "Find", GrepName: "Search",
	}
	tools := New(o)
	byName := make(map[string]core.ToolSpec)
	for _, tool := range tools {
		byName[tool.Spec().Name] = tool.Spec()
	}
	for name, wants := range map[string][]string{
		"View":   {"1234 lines", "56789 bytes", "321 characters", "Change", "Create", "Search"},
		"Create": {"View", "Change"},
		"Change": {"View", "Create", "replace_all"},
		"Find":   {"77 at most", "Search", ".gitignore", ".git directory"},
		"Search": {"55 lines", "3 KiB", "Find", "RE2", "no look-ahead or look-behind and no backreferences", "files_with_matches"},
	} {
		spec, ok := byName[name]
		if !ok {
			t.Errorf("no tool called %q", name)
			continue
		}
		mustContain(t, spec.Description, wants...)
		for _, old := range []string{"Read", "Write", "Edit", "Glob", "Grep"} {
			if strings.Contains(spec.Description, " "+old+" ") || strings.Contains(spec.Description, " "+old+".") {
				t.Errorf("%s: the description still calls a tool %q:\n%s", name, old, spec.Description)
			}
		}
	}
	// The messages use the names too.
	f := newFixture(t, o)
	p := f.file("a.txt", "x\n")
	f.fails(f.edit, in{"file_path": p, "old_string": "x", "new_string": "y"}, "Read it with View first")
	f.fails(f.write, in{"file_path": p, "content": "y"}, "Read it with View first", "use Change after that")
	f.fails(f.read, in{"file_path": f.dir}, "use Find with a pattern")
	f.fails(f.edit, in{"file_path": p, "old_string": "", "new_string": "y"}, "Change: old_string is empty", "use Create")
	// What the options turn off, the descriptions stop promising.
	loose := New(Options{GlobNoIgnore: true, GlobSkipHidden: true, GrepNoIgnore: true, GrepHidden: true, GrepBinary: true})
	mustContain(t, loose[3].Spec().Description, "are listed too", "are passed over")
	mustNotContain(t, loose[4].Spec().Description, "files that .gitignore rules leave out", "start with a dot", "look binary")
	mustContain(t, loose[4].Spec().Description, "anything in a .git directory")
}

func TestDefaults(t *testing.T) {
	d := Defaults()
	want := Options{
		ReadName: "Read", WriteName: "Write", EditName: "Edit", GlobName: "Glob", GrepName: "Grep", NotebookEditName: "NotebookEdit",
		ReadDefaultLines: 2000, ReadMaxBytes: 100000, ReadMaxLineChars: 2000, ReadCountBytes: 64 << 20, ReadMaxImageBytes: 5 << 20,
		EditMaxBytes: 32 << 20, DiffMaxBytes: 10 << 20, EditContextLines: 3,
		GlobMaxResults: 100,
		GrepHeadLimit:  250, GrepMaxContext: 1000, GrepMaxOutputBytes: 40 << 10, GrepMaxColumns: 500, GrepMaxLineBytes: 1 << 20, GrepMaxMultilineBytes: 32 << 20,
		SearchTimeout: 20 * time.Second, SearchWorkers: d.SearchWorkers, IgnoreFiles: []string{".gitignore", ".ignore", ".rgignore"},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("Defaults() =\n%+v\nwant\n%+v", d, want)
	}
	if d.SearchWorkers < 1 || d.SearchWorkers > 8 {
		t.Errorf("SearchWorkers = %d, want between 1 and 8", d.SearchWorkers)
	}
	// What is set stays set.
	o := Options{ReadDefaultLines: 5, SearchTimeout: -1, IgnoreFiles: []string{}, ReadCountBytes: -1, EditContextLines: -1, SearchWorkers: 3}.withDefaults()
	if o.ReadDefaultLines != 5 || o.SearchTimeout != -1 || o.IgnoreFiles == nil || len(o.IgnoreFiles) != 0 || o.ReadCountBytes != -1 || o.EditContextLines != -1 || o.SearchWorkers != 3 {
		t.Errorf("withDefaults overrode what was set: %+v", o)
	}
	// A limit below one is no limit anyone means; it takes the default.
	o = Options{ReadDefaultLines: -5, ReadMaxBytes: -1, ReadMaxLineChars: -1, GrepMaxLineBytes: -1, GrepMaxColumns: -9, GlobMaxResults: -2, EditMaxBytes: -1, GrepHeadLimit: -1}.withDefaults()
	if o.ReadDefaultLines != 2000 || o.ReadMaxBytes != 100000 || o.ReadMaxLineChars != 2000 || o.GrepMaxLineBytes != 1<<20 || o.GrepMaxColumns != 500 || o.GlobMaxResults != 100 || o.EditMaxBytes != 32<<20 {
		t.Errorf("limits below one were kept: %+v", o)
	}
	// Except where a negative value means something.
	if o.GrepHeadLimit != 0 {
		t.Errorf("GrepHeadLimit = %d, want 0 for no limit", o.GrepHeadLimit)
	}
	// One set of tools serves any number of sessions, and New does not
	// keep hold of the options it was given.
	opts := Options{ReadDefaultLines: 2}
	tools := New(opts)
	opts.ReadDefaultLines = 99
	mustContain(t, tools[0].Spec().Description, "at most 2 lines")
}
