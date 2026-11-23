package config

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The words for the kinds of setting are shared with the trust package,
// which cannot be imported here. Its test pins the same list: change one
// side and its test fails until both agree.
func TestKindsMatchTheTrustPackage(t *testing.T) {
	got := []string{
		KindProvider, KindModel, KindHost, KindHook, KindHookOff,
		KindAllowRule, KindAskRule, KindDenyRule, KindMode, KindAddDir, KindEnv, KindOption, KindExtraPath,
		KindAddress, KindHiddenSkill, KindRemoval, KindStatusLine, KindFileSuggest, KindLSPServer, KindSandboxWider,
	}
	want := []string{
		"provider", "model", "host", "hook", "hook-off",
		"allow-rule", "ask-rule", "deny-rule", "mode", "add-dir", "env", "option", "extra-path",
		"address", "hidden-skill", "removal", "status-line", "file-suggestion", "lsp", "sandbox-wider",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the kinds changed:\n got %q\nwant %q\nChange internal/trust/kinds.go and its test to match.", got, want)
	}
}

// camelCase is what a key of gnosh's own looks like, as Claude Code's
// settings keys do: letters and digits, beginning in lower case, with no
// dash and no underscore.
var camelCase = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

// The table is the one list of what the configuration can say. Every
// entry has a name, says what it is for and where its name comes from,
// and nothing is listed twice.
func TestTable(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Table() {
		if e.Name == "" || seen[e.Name] {
			t.Errorf("an entry with no name, or listed twice: %+v", e)
		}
		seen[e.Name] = true
		if e.What == "" {
			t.Errorf("%s does not say what it is for", e.Name)
		}
		if !strings.HasPrefix(e.From, fromClaudeCode) && !strings.HasPrefix(e.From, fromAPIs) && !strings.HasPrefix(e.From, fromPlain) {
			t.Errorf("%s does not say where its name comes from (Claude Code, the provider APIs, or plain words): %q", e.Name, e.From)
		}
	}
	// Every key the reader takes, the provider types, a hook event and
	// the variables are there.
	for _, name := range []string{
		"providers", "models", "model", "smallModel", "hosts", "options", "permissions", "hooks", "env", "disableAllHooks",
		"providers.<id>.type", "providers.<id>.baseUrl", "providers.<id>.apiKey", "providers.<id>.headers", "providers.<id>.extraBody",
		"providers.<id>.listModels", "providers.<id>.disabled", "providers.<id>.name",
		"models.<provider>/<model id>.contextWindow", "models.<provider>/<model id>.maxOutputTokens", "models.<provider>/<model id>.thinkingLevels",
		"model.id", "model.thinking", "model.temperature", "smallModel.id", "hosts.<name>.ssh", "hosts.<name>.description",
		"options.requestTimeout", "options.autoCompactWindow", "options.memEmbeddingsUrl", "options.hiddenSkills", "options.deliverablesServer",
		"permissions.allow", "permissions.defaultMode",
		"hook event Stop", "provider type llamacpp", "provider type openai-compatible", "$GNOSH_CONFIG_DIR", "$GNOSH_DATA_DIR", "$API_TIMEOUT_MS",
	} {
		if !seen[name] {
			t.Errorf("the table has no entry %q", name)
		}
	}
	// What must stop startup when it is configured and the part it is for
	// is not built names that part.
	for _, e := range Table() {
		guard := strings.HasPrefix(e.Name, "hook event ") || strings.HasPrefix(e.Name, "hosts") || e.Name == "hooks" || e.Name == "env" ||
			e.Name == "permissions" || e.Name == "permissions.deny" || e.Name == "permissions.allow"
		if guard && e.Feature == "" {
			t.Errorf("%s is something that must stop startup when it is not built, and it names no part of gnosh", e.Name)
		}
	}
	// Every key is written the way Claude Code's settings keys are. None
	// has a dash in it, as the names of the line format gnosh had before
	// did.
	for _, defs := range [][]keyDef{topKeys, providerFields, modelFields, slotFields, hostFields, permissionKeys} {
		for _, d := range defs {
			if d.key != "$schema" && !camelCase.MatchString(d.key) {
				t.Errorf("the key %q is not written as Claude Code's keys are", d.key)
			}
		}
	}
	for _, v := range ownEnv {
		if v.use == "reads" && (v.what == "" || v.from == "") {
			t.Errorf("the variable %s lacks its meaning or where its name comes from", v.name)
		}
	}
	// The provider types are the servers gnosh treats in a way of their
	// own and the one format the rest speak, and no others
	// (spec/DECISIONS.md, 26 and 27).
	if got := providerTypeNames(); !slices.Equal(got, []string{"llamacpp", "openai-compatible", "anthropic", "openai"}) {
		t.Errorf("the provider types are %q", got)
	}
	for _, pt := range providerTypes {
		if pt.what == "" || pt.from == "" {
			t.Errorf("the provider type %s lacks what it is or where its name comes from", pt.name)
		}
	}
}

// Every option key sets exactly one field, and resetting it gives the
// default back.
func TestOptionTable(t *testing.T) {
	seen := map[string]bool{}
	for i := range optionDefs {
		d := &optionDefs[i]
		if seen[d.key] {
			t.Errorf("option %s is in the table twice", d.key)
		}
		seen[d.key] = true
		if !camelCase.MatchString(d.key) {
			t.Errorf("the option %q is not written as Claude Code's keys are", d.key)
		}
		if hasKey(topKeys, d.key) || slices.Contains(claudeCodeKeys, d.key) {
			t.Errorf("option %s is also a key of the top of the file, or listed as not built", d.key)
		}
		setters := 0
		for _, set := range []bool{d.truth != nil, d.num != nil, d.text != nil, d.list != nil} {
			if set {
				setters++
			}
		}
		if setters != 1 {
			t.Errorf("option %s sets %d fields, want exactly 1", d.key, setters)
		}
		if d.what == "" || d.def == "" || d.from == "" {
			t.Errorf("option %s lacks its meaning, its default or where its name comes from", d.key)
		}
		// Changed and put back, it is as it was when nothing set it.
		opts, def := defaultOptions(), defaultOptions()
		o := Origin{Layer: LayerUser, File: userJSON, Line: 1}
		switch {
		case d.truth != nil:
			d.truth(&opts).put(!d.truth(&def).V, o)
		case d.num != nil:
			d.num(&opts).put(d.num(&def).V+1, o)
		case d.text != nil:
			d.text(&opts).put("another", o)
		case d.list != nil:
			addItem(d.list(&opts), "another", o)
		}
		if all := opts.All(); !all[i].Set || all[i].Key != d.key {
			t.Errorf("option %s was set and All does not say so: %+v", d.key, all[i])
		}
		d.reset(&opts)
		if all := opts.All(); all[i].Set || all[i].Text != d.def {
			t.Errorf("option %s was put back and All still shows it set: %+v", d.key, all[i])
		}
	}
	// Every hook event has an entry, in the spec's order, and exactly the
	// five with nothing to match take no matcher.
	var none []string
	for _, e := range hookEvents {
		if e.matcher == "" {
			none = append(none, e.name)
		}
	}
	if len(hookEvents) != 25 || !slices.Equal(none, []string{"UserPromptSubmit", "Stop", "SubagentStop", "CwdChanged", "PostToolBatch"}) {
		t.Errorf("hook events: %d, without a matcher %q", len(hookEvents), none)
	}
	if !slices.Equal(HookEventNames()[:6], []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "SessionStart", "Stop"}) {
		t.Errorf("hook event names: %q", HookEventNames())
	}
}

// The keys Claude Code documents are the ones in spec/cc/settings.md. Each
// of them is either a key gnosh reads, one of its options, or on the list
// of settings Claude Code has and gnosh has not built, so that a file
// holding one gets a message that says whose key it is; and the list holds
// nothing the spec does not name.
func TestClaudeCodeKeysMatchTheSpec(t *testing.T) {
	spec, err := os.ReadFile("../../spec/cc/settings.md")
	if err != nil {
		t.Skipf("the spec is not beside the package in this copy of the tree: %v", err)
	}
	text := string(spec)
	_, keys, ok := strings.Cut(text, "\n## 5. Keys")
	if !ok {
		t.Fatal("spec/cc/settings.md has no section 5, Keys")
	}
	keys, _, _ = strings.Cut(keys, "\n## 6.")
	_, notAdopted, ok := strings.Cut(text, "\n## Deliberately not adopted")
	if !ok {
		t.Fatal("spec/cc/settings.md has no section on what is not adopted")
	}
	notAdopted, _, _ = strings.Cut(notAdopted, "\n## ")

	documented := map[string]bool{}
	// The first column of each row of the tables of section 5, and the
	// names listed in 5.3 and under what is not adopted.
	for _, m := range regexp.MustCompile("(?m)^\\| `([A-Za-z]+)` \\|").FindAllStringSubmatch(keys, -1) {
		documented[m[1]] = true
	}
	// What section 5.3 says in brackets about a key is not a key.
	_, later, _ := strings.Cut(keys, "\n### 5.3")
	later = regexp.MustCompile(`\([^)]*\)`).ReplaceAllString(later, "")
	for _, m := range regexp.MustCompile("`([a-z][A-Za-z]+)(?:\\.\\*)?`").FindAllStringSubmatch(later+notAdopted, -1) {
		documented[m[1]] = true
	}
	if len(documented) < 50 {
		t.Fatalf("only %d keys were found in the spec; its layout has changed and this test with it", len(documented))
	}
	for key := range documented {
		if !hasKey(topKeys, key) && findOption(key) == nil && !slices.Contains(claudeCodeKeys, key) {
			t.Errorf("Claude Code documents the settings key %q, and gnosh neither reads it nor lists it as not built", key)
		}
	}
	for _, key := range claudeCodeKeys {
		if !documented[key] {
			t.Errorf("%q is listed as a setting Claude Code has, and spec/cc/settings.md does not name it", key)
		}
		if hasKey(topKeys, key) {
			t.Errorf("%q is listed as not built, and it is a key gnosh reads", key)
		}
	}
	// A file that holds one of them is refused with a message that says
	// whose key it is.
	for _, key := range claudeCodeKeys {
		_, problems := load(t, withConfig(`{"`+key+`": true}`))
		wantProblem(t, problems, Error, userJSON, 1, `"`+key+`" is a setting Claude Code has and gnosh has not built`)
	}
}
