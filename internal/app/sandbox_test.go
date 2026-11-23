package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/sandbox"
)

// With the sandbox on and autoAllowBashIfSandboxed, a gnosh -p run lets a
// command that writes in the project run without anybody's approval (it
// runs in the sandbox), and a command that writes outside fails there with
// a note; the model is told of the sandbox. Skipped where the sandbox
// cannot run.
func TestSandboxedCommandsRunWithoutAsking(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	outside, err := os.MkdirTemp("/var/tmp", "gnosh-app-sandbox-")
	if err != nil {
		t.Skip("no /var/tmp")
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	w := apptest.NewWorld(t)
	w.Settings(`{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true}}`)
	w.Write(".claude/settings.json", "{}\n")
	w.Write("secret.txt", "hunter2\n")
	w.Deny("Read(./secret.txt)")
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if n == 0 {
			return &apptest.Reply{Calls: []apptest.Call{
				{ID: "c1", Name: "Bash", Args: `{"command":"echo made > made.txt && echo ok"}`},
				{ID: "c2", Name: "Bash", Args: `{"command":"echo out > ` + outside + `/out.txt"}`},
				// (A write gnosh cannot see in the command: the sandbox
				// keeps the settings file read-only all the same.)
				{ID: "c3", Name: "Bash", Args: `{"command":"python3 -c \"open('.claude/settings.json', 'a').write('x')\""}`},
				// (A read gnosh cannot see: the Read deny rule's path is
				// kept from the sandbox all the same.)
				{ID: "c4", Name: "Bash", Args: `{"command":"python3 -c \"print('[' + open('secret.txt').read() + ']')\""}`},
			}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-p", "make it").wantCode(t, 0)
	if !w.Exists("made.txt") {
		t.Errorf("the sandboxed command did not run: %s", toolResult(t, w, 1, "c1"))
	}
	has(t, "the write outside", toolResult(t, w, 1, "c2"), "Read-only file system", "it ran in the sandbox", "dangerouslyDisableSandbox set to true")
	if _, err := os.Stat(filepath.Join(outside, "out.txt")); err == nil {
		t.Error("a sandboxed command wrote outside the project")
	}
	has(t, "a write to a guarded file", toolResult(t, w, 1, "c3"), "Read-only file system")
	if got := w.Read(".claude/settings.json"); got != "{}\n" {
		t.Errorf("a sandboxed command changed a settings file: %q", got)
	}
	if got := toolResult(t, w, 1, "c4"); strings.Contains(got, "hunter2") || !strings.Contains(got, "Permission denied") {
		t.Errorf("a sandboxed command read what a Read rule denies: %s", got)
	}
	has(t, "what the model is told of Bash", string(w.Model.Requests()[0].Body), "In the sandbox:", "dangerouslyDisableSandbox")
}

// excludedCommands takes a plain command out of the sandbox, never a
// list, a pipe, a redirection, a substitution or a cd, and a git clone
// whose path leads elsewhere stays in.
func TestExcludedCall(t *testing.T) {
	entries := []config.ListItem{{V: "docker *"}, {V: "make"}, {V: "git *"}, {V: "npm run *:dev"}}
	for cmd, want := range map[string]bool{
		"docker ps": true, "docker": true, "  docker   build . ": true, "dockerd": false, "make": true, "make all": false,
		"docker ps; rm x": false, "docker ps | cat": false, "docker build . > log": false, "docker $(x)": false, "cd x && docker ps": false,
		"git clone https://example.com/r vendor/lib": true, "git clone https://example.com/r ~/tools": false, "git worktree add ../x": false,
		"npm run web:dev": true, "npm run web:prod": false, "sudo docker ps": false, "": false,
		"docker ps 2>&1": true, "docker\tps": false, "docker\u00a0ps": false,
		"git --git-dir=x status": false, "git clone --template=./t https://example.com/r x": false, "git -c core.fsmonitor=x status": false, "git -Cx status": false,
	} {
		if got := excludedCall(entries, cmd); got != want {
			t.Errorf("excludedCall(%q) = %v", cmd, got)
		}
	}
	if excludedCall(nil, "docker ps") {
		t.Error("no entries took a command out")
	}
	for _, want := range []string{".bashrc", ".claude", ".gnosh", ".envrc", ".mcp.json"} {
		if !slices.Contains(sandboxGuarded, want) {
			t.Errorf("%s is not guarded", want)
		}
	}
}

// /sandbox says how the sandbox stands, and saves a mode chosen to the
// project's local settings, in force from the next start.
func TestSlashSandbox(t *testing.T) {
	w := apptest.NewWorld(t)
	r := run(t, w, "-p", "/sandbox").wantCode(t, 0)
	has(t, "/sandbox with it off", r.out, "The sandbox is off", "/sandbox auto")
	r = run(t, w, "-p", "/sandbox auto").wantCode(t, 0)
	has(t, "/sandbox auto", r.out, "Saved to", "settings.local.json", "/restart")
	local := w.Read(".gnosh/settings.local.json")
	if !strings.Contains(local, `"enabled": true`) || !strings.Contains(local, `"autoAllowBashIfSandboxed": true`) {
		t.Errorf("the local settings: %s", local)
	}
	if st := sandbox.Check(); st.OK {
		r = run(t, w, "-p", "/sandbox").wantCode(t, 0)
		has(t, "/sandbox with it on", r.out, "The sandbox is on", "Mode: auto-allow", "Writes: ", "Network: through gnosh's proxy")
		hasNot(t, "/sandbox with no link on the way", r.out, "Not kept")
		// A guarded file kept by a link (direnv's .envrc in a shared
		// place): on Linux the link is not kept, and /sandbox says so.
		w.Write(w.Home+"/shared/envrc", "export A=1\n")
		if err := os.Symlink(w.Home+"/shared/envrc", w.Path(".envrc")); err != nil {
			t.Fatal(err)
		}
		r = run(t, w, "-p", "/sandbox").wantCode(t, 0)
		if runtime.GOOS == "linux" {
			has(t, "/sandbox with a link on the way", r.out, "Not kept, being links a command could replace", ".envrc")
		} else {
			hasNot(t, "/sandbox with a link on the way, kept", r.out, "Not kept")
		}
	}
	run(t, w, "-p", "/sandbox strict").wantCode(t, 0)
	if local := w.Read(".gnosh/settings.local.json"); !strings.Contains(local, `"allowUnsandboxedCommands": false`) || !strings.Contains(local, `"enabled": true`) {
		t.Errorf("strict lost the mode: %s", local)
	}
	r = run(t, w, "-p", "/sandbox maybe")
	has(t, "a word it does not take", r.out+r.err, "/sandbox takes auto, regular, off, strict or loose")
}

// With network.allowedDomains, a sandboxed command reaches the hosts
// named through gnosh's proxy, and no other; the model is told which.
// Skipped where the sandbox or curl cannot run.
func TestSandboxedCommandsReachAllowedHosts(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "reached") }))
	defer srv.Close()
	w := apptest.NewWorld(t)
	w.Settings(`{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true, "network": {"allowedDomains": ["127.0.0.1"]}}}`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if n == 0 {
			return &apptest.Reply{Calls: []apptest.Call{
				{ID: "c1", Name: "Bash", Args: `{"command":"curl -s -m 10 ` + srv.URL + `/x"}`},
				{ID: "c2", Name: "Bash", Args: `{"command":"curl -s -m 10 http://elsewhere.example/"}`},
			}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-p", "fetch").wantCode(t, 0)
	has(t, "an allowed host", toolResult(t, w, 1, "c1"), "reached")
	has(t, "a host not allowed", toolResult(t, w, 1, "c2"), "not in sandbox.network.allowedDomains")
	has(t, "what the model is told", string(w.Model.Requests()[0].Body), "which lets through 127.0.0.1 and the hosts WebFetch", "any other host only as the permission mode says, as nobody is here to be asked")
}

// What the sandbox cannot keep from being made, where it was not there to
// mount read-only, is taken away after the command, and said: Claude
// Code's folder (an agent there would run hooks outside), a top-level
// HEAD (a bare repository whose config git would obey). A project's
// settings file written while the session runs is not taken in until the
// next start. Skipped where the sandbox cannot run.
func TestSandboxTakesAwayWhatItCouldNotKeep(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	w := apptest.NewWorld(t)
	w.Settings(`{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true}}`)
	py := func(id, code string) apptest.Call {
		return apptest.Call{ID: id, Name: "Bash", Args: `{"command":"python3 -c \"` + code + `\""}`}
	}
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		switch n {
		case 0:
			return &apptest.Reply{Calls: []apptest.Call{
				py("c1", "import os; os.makedirs('.claude/agents'); open('.claude/agents/x.md', 'w').write('x')"),
				py("c2", "open('HEAD', 'w').write('ref: refs/heads/main')"),
				py("c3", "import json, os; os.makedirs('.gnosh', exist_ok=True); json.dump(dict(permissions=dict(allow=['Bash(touch *)'])), open('.gnosh/settings.local.json', 'w'))"),
			}}
		case 1:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c4", Name: "Bash", Args: `{"command":"touch outside-ok","dangerouslyDisableSandbox":true}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	r := run(t, w, "-p", "go")
	if w.Exists(".claude") || w.Exists("HEAD") {
		t.Errorf("what the sandbox could not keep was left: .claude %v, HEAD %v", w.Exists(".claude"), w.Exists("HEAD"))
	}
	has(t, "the note", toolResult(t, w, 1, "c1"), "the sandbox took away", ".claude")
	has(t, "the note", toolResult(t, w, 1, "c2"), "the sandbox took away", "HEAD")
	if got := toolResult(t, w, 2, "c4"); w.Exists("outside-ok") {
		t.Errorf("a rule a sandboxed command wrote was taken in: %s", got)
	}
	if w.Exists(".gnosh/settings.local.json") {
		has(t, "the person told", r.out+r.err, "changed during the session", "/restart")
	}
	// gnosh's own folder is made as the session starts, to be kept
	// read-only: a command cannot put a kind of agent in it.
	if !w.Exists(".gnosh") {
		t.Error("gnosh's own folder was not made, to be kept read-only")
	}
	has(t, "a write in gnosh's own folder", toolResult(t, w, 1, "c3"), "Read-only file system")
}

// In a git work tree, git's own folder (the repository's) is the
// command's to write in, so that a commit works in the sandbox, with its
// hooks and settings, and the work tree's way to them, kept read-only.
func TestGitGuardsOfAWorkTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	gitRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun(repo, "init", "-q")
	gitRun(repo, "commit", "-q", "--allow-empty", "-m", "one")
	tree := filepath.Join(t.TempDir(), "tree")
	gitRun(repo, "worktree", "add", "-q", tree)
	writes, guards := gitGuards(tree)
	common := filepath.Join(repo, ".git")
	if !slices.Contains(writes, common) || !slices.Contains(guards, filepath.Join(common, "hooks")) || !slices.Contains(guards, filepath.Join(common, "config")) || !slices.Contains(guards, filepath.Join(tree, ".git")) {
		t.Errorf("a work tree: writes %v, guards %v", writes, guards)
	}
	_, guards = gitGuards(repo)
	for _, want := range []string{"hooks", "config", "commondir", "config.worktree", "modules"} {
		if !slices.Contains(guards, filepath.Join(common, want)) {
			t.Errorf("a repository's %s is not guarded: %v", want, guards)
		}
	}
}

// A WebFetch(domain:...) allow rule lets a sandboxed command reach its
// host too, as Claude Code takes those rules into its sandbox's allowed
// domains. Skipped where the sandbox or curl cannot run.
func TestSandboxedCommandsReachWebFetchHosts(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "reached") }))
	defer srv.Close()
	w := apptest.NewWorld(t)
	w.Settings(`{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true}}`)
	w.Allow("WebFetch(domain:127.0.0.1)")
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if n == 0 {
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"curl -s -m 10 ` + srv.URL + `/x"}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-p", "fetch").wantCode(t, 0)
	has(t, "a host a WebFetch rule allows", toolResult(t, w, 1, "c1"), "reached")
}

// In a session somebody attends, a sandboxed command that reaches for a
// host nothing lets through waits while the person is asked; Yes lets it
// through for the session, and the next command reaches it without a
// question. Skipped where the sandbox or curl cannot run.
func TestSandboxAsksAboutANewHost(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "reached") }))
	defer srv.Close()
	curl := func(id string) apptest.Reply {
		return apptest.Reply{Calls: []apptest.Call{{ID: id, Name: "Bash", Args: `{"command":"curl -s -m 30 ` + srv.URL + `/x"}`}}}
	}
	w := apptest.NewWorld(t, curl("c1"), apptest.Reply{Text: "First."}, curl("c2"), apptest.Reply{Text: "Second."})
	w.Settings(`{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true}}`)
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("fetch it")
	s.see("The answer is for the host on every port.")
	s.see("Let a command in the sandbox reach 127.0.0.1?")
	s.see("  1. Yes: for this session")
	s.typeLine("1")
	s.see("First.")
	has(t, "the first fetch", toolResult(t, w, 1, "c1"), "reached")
	s.typeLine("again")
	s.see("Second.")
	has(t, "the second fetch, asked nothing", toolResult(t, w, 3, "c2"), "reached")
	s.typeLine("/exit")
	s.exit()
}

// credentials: a variable listed is not given to a sandboxed command, and
// a file listed reads as nothing it may have. Skipped where the sandbox
// cannot run.
func TestSandboxHidesCredentials(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	w := apptest.NewWorld(t)
	keys := w.Write(w.Home+"/.aws/credentials", "aws_secret=hunter2\n")
	w.Write(".env", "TOKEN=hunter5\n")
	// (A pattern, as Claude Code's docs write one; an allowRead of the
	// hidden file, which must not bring it back: the sandbox review.)
	w.Settings(`{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true, "filesystem": {"allowRead": ["~/.aws/credentials"]},
		"credentials": {"files": [{"path": "~/.aws/credentials", "mode": "deny"}, {"path": "` + w.Dir + `/**/.env", "mode": "deny"}], "envVars": [{"name": "GITHUB_TOKEN", "mode": "deny"}]}}}`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if n == 0 {
			return &apptest.Reply{Calls: []apptest.Call{
				{ID: "c1", Name: "Bash", Args: `{"command":"echo token=[$GITHUB_TOKEN] other=[$OTHER_TOKEN]"}`},
				{ID: "c2", Name: "Bash", Args: `{"command":"python3 -c \"print(open('` + keys + `').read())\""}`},
				{ID: "c3", Name: "Bash", Args: `{"command":"cat .env; echo read"}`},
			}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	o := testOptions(w, "-p", "look")
	o.Environ = append(o.Environ, "GITHUB_TOKEN=ghp_secret", "OTHER_TOKEN=kept")
	runWith(t, context.Background(), o).wantCode(t, 0)
	has(t, "the variables", toolResult(t, w, 1, "c1"), "token=[] other=[kept]")
	if got := toolResult(t, w, 1, "c2"); strings.Contains(got, "hunter2") {
		t.Errorf("a hidden credentials file was read, allowRead naming it: %s", got)
	}
	if got := toolResult(t, w, 1, "c3"); strings.Contains(got, "hunter5") || !strings.Contains(got, "read") {
		t.Errorf("a file a hidden pattern matches was read: %s", got)
	}
	r := run(t, w, "-p", "/sandbox").wantCode(t, 0)
	has(t, "/sandbox", r.out, "Credentials hidden, whatever else opens them:", "/proj/**/.env (1 path found when the session started")
}

// With filesystem.disabled the sandbox leaves the files open: a command
// writes outside the project, its network stays confined, and it asks as
// any command does though autoAllowBashIfSandboxed is on (a command with
// the files open could change what gnosh obeys later). /sandbox says so,
// and lists the other loosenings and the reports not made. Skipped where
// the sandbox cannot run.
func TestSandboxFilesLeftOpen(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	outside, err := os.MkdirTemp("/var/tmp", "gnosh-app-open-")
	if err != nil {
		t.Skip("no /var/tmp")
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	w := apptest.NewWorld(t)
	w.Settings(`{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true, "filesystem": {"disabled": true},
		"network": {"allowLocalBinding": true, "allowUnixSockets": ["/run/user/1000/agent.sock"]}, "ignoreViolations": {"*": ["/etc/hosts"]}}}`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		// (Each run's first request: the call; after its result, done.)
		if !strings.Contains(string(r.Body), `"role":"tool"`) {
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"echo out > ` + outside + `/out.txt && echo wrote"}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-p", "write it").wantCode(t, 0)
	if _, err := os.Stat(filepath.Join(outside, "out.txt")); err == nil {
		t.Error("with the files left open, a sandboxed command ran without asking")
	}
	has(t, "the call that asked", toolResult(t, w, 1, "c1"), "approval")
	run(t, w, "-y", "-p", "write it").wantCode(t, 0)
	if data, err := os.ReadFile(filepath.Join(outside, "out.txt")); err != nil || string(data) != "out\n" {
		t.Errorf("with the files left open, let run, the write outside: %q, %v", data, err)
	}
	r := run(t, w, "-p", "/sandbox").wantCode(t, 0)
	has(t, "/sandbox", r.out, "Files: left open (filesystem.disabled", "they ask as any command does",
		"Loosened: network.allowLocalBinding", "Not reported when stopped (ignoreViolations): /etc/hosts for every command", "Network: through gnosh's proxy")
	if runtime.GOOS == "linux" {
		// (As Claude Code's /sandbox says since 2.1.329.)
		has(t, "/sandbox on Linux", r.out, "network.allowUnixSockets: /run/user/1000/agent.sock, which open nothing here")
	}
}

// A WebFetch(domain:...) deny rule refuses its host to a sandboxed
// command in every mode, bypassPermissions too, where a host no rule
// names is let through (the sandbox review). Skipped where the sandbox or
// curl cannot run.
func TestSandboxDenyRuleInBypass(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "reached") }))
	defer srv.Close()
	w := apptest.NewWorld(t)
	w.Settings(`{"sandbox": {"enabled": true}}`)
	w.Deny("WebFetch(domain:127.0.0.1)")
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if !strings.Contains(string(r.Body), `"role":"tool"`) {
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"curl -s -m 30 ` + srv.URL + `/x"}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-y", "-p", "fetch it").wantCode(t, 0)
	if got := toolResult(t, w, 1, "c1"); strings.Contains(got, "reached") || !strings.Contains(got, "WebFetch(domain:...) deny rule") {
		t.Errorf("a denied host in bypassPermissions mode: %s", got)
	}
}
