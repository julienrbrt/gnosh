package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/cli"
	"github.com/gnosh-ai/gnosh/internal/mcp"
	"github.com/gnosh-ai/gnosh/internal/trust"
)

// This file is `gnosh mcp`: add, add-json, get, list and remove, as Claude
// Code's `claude mcp` has them (spec/cc/mcp.md, section 3), and login and
// logout for a server at a URL that wants a sign-in (mcpauth.go). A server is
// kept in one of three places (its scope): local, in gnosh's own mcp.json
// under "projects" by the project's root, for one project and the person
// alone; user, at the top of that file, for every project; and project,
// in the project's .mcp.json. add and remove change the file and nothing
// else, so they work even where the configuration has a mistake; get and
// list connect to each server to say whether it works, and never start a
// project's servers in a folder that is not trusted.

// mcpPlaces are where a folder's servers are kept, in the order in which
// a name is looked for: the order of precedence.
type mcpPlaces struct {
	root    string // the project's root
	user    string // gnosh's own mcp.json
	project string // the project's .mcp.json
}

func (p *program) mcpPlaces(inv cli.Invocation) (mcpPlaces, error) {
	cwd := p.o.Cwd
	if inv.Cwd != "" {
		cwd = p.absFrom(inv.Cwd)
	}
	folder, err := trust.Resolve(cwd, p.home)
	if err != nil {
		return mcpPlaces{}, err
	}
	return mcpPlaces{
		root:    folder.Root,
		user:    filepath.Join(p.configDir(), "mcp.json"),
		project: filepath.Join(folder.Root, ".mcp.json"),
	}, nil
}

// place is where one scope keeps its servers.
func (m mcpPlaces) place(scope string) mcp.Place {
	switch scope {
	case "project":
		return mcp.Place{Path: m.project}
	case "user":
		return mcp.Place{Path: m.user}
	}
	return mcp.Place{Path: m.user, Project: m.root}
}

// projectLeadsOut is where the project's .mcp.json leads when it is a link
// out of the project, or "": a project server written through it was one
// of wherever it led, the person's own mcp.json among them, started in
// every project with no trust question (the settings-commands review of
// 2026-11-20).
func (m mcpPlaces) projectLeadsOut() string {
	if _, err := os.Lstat(m.project); err != nil {
		return ""
	}
	if target, out := trust.LeadsOut(m.project, m.root); out {
		return firstOf(target, m.project)
	}
	return ""
}

// where says, for the person, where a scope keeps its servers.
func (m mcpPlaces) where(scope string) string {
	switch scope {
	case "project":
		return "project (" + m.project + ")"
	case "user":
		return "user (" + m.user + ")"
	}
	return "local (" + m.user + ", for " + m.root + ")"
}

// mcpScopes are the scopes in their order of precedence.
var mcpScopes = []string{"local", "project", "user"}

func (p *program) mcpCommand(ctx context.Context, inv cli.Invocation) int {
	places, err := p.mcpPlaces(inv)
	if err != nil {
		p.errf("%v", err)
		return cli.ExitFailure
	}
	switch inv.Command {
	case cli.CmdMCPAdd, cli.CmdMCPAddJSON:
		return p.mcpAdd(inv, places)
	case cli.CmdMCPRemove:
		return p.mcpRemove(inv, places)
	case cli.CmdMCPLogin, cli.CmdMCPLogout:
		return p.mcpSignIn(ctx, inv, places)
	}
	return p.mcpShow(ctx, inv, places)
}

// maxMCPEntry is the most of a server's entry read from standard input.
const maxMCPEntry = 1 << 20

// mcpAdd is `gnosh mcp add` and `add-json`.
func (p *program) mcpAdd(inv cli.Invocation, places mcpPlaces) int {
	name := inv.Args[0]
	if why := mcp.CheckName(name); why != "" {
		p.errf("%q cannot be a server's name: %s. Nothing was added.", name, why)
		return cli.ExitFailure
	}
	scope := firstOf(inv.MCPScope, "local")
	var entry json.RawMessage
	switch {
	case inv.Command == cli.CmdMCPAddJSON && inv.Args[1] == "-":
		// The entry from standard input, so that a key in it stays off
		// the command line and out of the shell's history.
		if p.o.StdinIsTerminal || p.o.Stdin == nil {
			p.errf("with - the entry is read from standard input, and none is piped in: echo '{...}' | gnosh mcp add-json %s -. Nothing was added.", name)
			return cli.ExitFailure
		}
		data, err := io.ReadAll(io.LimitReader(p.o.Stdin, maxMCPEntry+1))
		switch {
		case err != nil:
			p.errf("the entry could not be read from standard input: %v. Nothing was added.", err)
			return cli.ExitFailure
		case len(data) > maxMCPEntry:
			p.errf("the entry on standard input is longer than %d KiB, which no server's entry is. Nothing was added.", maxMCPEntry>>10)
			return cli.ExitFailure
		}
		entry = json.RawMessage(bytes.TrimSpace(data))
	case inv.Command == cli.CmdMCPAddJSON:
		entry = json.RawMessage(inv.Args[1])
	default:
		var err error
		entry, err = mcp.Entry(inv.MCPTransport, inv.Args[1], inv.Args[2:], inv.MCPEnv, inv.MCPHeaders)
		if err != nil {
			p.errf("%v, so nothing was added.", err)
			return cli.ExitFailure
		}
	}
	if err := mcp.CheckEntry(name, entry); err != nil {
		p.errf("the entry of %s would not be used: %v. Nothing was added.", name, err)
		return cli.ExitFailure
	}
	if target := places.projectLeadsOut(); scope == "project" && target != "" {
		p.errf("the project's .mcp.json is a link to %s, outside the project, so nothing was added; gnosh does not write a project's servers through it.", target)
		return cli.ExitFailure
	}
	pl := places.place(scope)
	had, err := mcp.Lookup(pl, name)
	if err != nil {
		p.errf("%v. Nothing was added.", err)
		return cli.ExitFailure
	}
	if had != nil {
		p.errf("there is a server named %s in %s already, so nothing was added. To replace it, remove it first: gnosh mcp remove %s -s %s", name, places.where(scope), name, scope)
		return cli.ExitFailure
	}
	// The user's file may hold keys: it is its owner's to read alone. A
	// project's .mcp.json goes with the project.
	mode := fs.FileMode(0o600)
	if scope == "project" {
		mode = 0o644
	}
	if err := mcp.Put(pl, name, entry, mode); err != nil {
		p.errf("%s could not be added: %v", name, err)
		return cli.ExitFailure
	}
	fmt.Fprintf(p.stdout, "Added %s to %s.\n", name, places.where(scope))
	switch scope {
	case "project":
		fmt.Fprintln(p.stdout, "A project's servers are used once its folder is trusted (gnosh trust); share .mcp.json with the project to share the server.")
	default:
		fmt.Fprintln(p.stdout, "Sessions started from now on connect to it: gnosh mcp get "+name+" says whether it works.")
	}
	return cli.ExitOK
}

// mcpRemove is `gnosh mcp remove`.
func (p *program) mcpRemove(inv cli.Invocation, places mcpPlaces) int {
	name := inv.Args[0]
	scopes := mcpScopes
	if inv.MCPScope != "" {
		scopes = []string{inv.MCPScope}
	}
	var in []string
	for _, scope := range scopes {
		got, err := mcp.Lookup(places.place(scope), name)
		if err != nil {
			p.errf("%v. Nothing was removed.", err)
			return cli.ExitFailure
		}
		if got != nil {
			in = append(in, scope)
		}
	}
	switch {
	case len(in) == 0 && inv.MCPScope != "":
		p.errf("there is no server named %s in %s, so nothing was removed.", name, places.where(inv.MCPScope))
		return cli.ExitFailure
	case len(in) == 0:
		p.errf("there is no server named %s here: not local, not in the project's .mcp.json, and not the user's. Nothing was removed. gnosh mcp list names the servers.", name)
		return cli.ExitFailure
	case len(in) > 1:
		var ws []string
		for _, scope := range in {
			ws = append(ws, places.where(scope))
		}
		p.errf("%s is in more than one place: %s. Say which with -s (%s); nothing was removed.", name, strings.Join(ws, ", and "), strings.Join(in, ", "))
		return cli.ExitFailure
	}
	if target := places.projectLeadsOut(); in[0] == "project" && target != "" {
		p.errf("the project's .mcp.json is a link to %s, outside the project, so nothing was removed; gnosh does not write a project's servers through it.", target)
		return cli.ExitFailure
	}
	if _, err := mcp.Delete(places.place(in[0]), name); err != nil {
		p.errf("%s could not be removed: %v", name, err)
		return cli.ExitFailure
	}
	fmt.Fprintf(p.stdout, "Removed %s from %s.\n", name, places.where(in[0]))
	return cli.ExitOK
}

// mcpShow is `gnosh mcp list` and `get`.
func (p *program) mcpShow(ctx context.Context, inv cli.Invocation, places mcpPlaces) int {
	st, cfgs, probs, waiting, code := p.mcpLoad(ctx, inv, places)
	if st == nil {
		return code
	}
	if inv.Command == cli.CmdMCPGet {
		return p.mcpGet(ctx, inv.Args[0], cfgs, waiting, places, st)
	}
	if len(cfgs) == 0 && len(waiting) == 0 {
		fmt.Fprintln(p.stdout, "No MCP server is configured here. gnosh mcp add adds one: gnosh mcp add <name> -- <command> [args...], or gnosh mcp add -t http <name> <url>.")
	}
	results := p.mcpCheckAll(ctx, cfgs, places.root, st)
	for i, c := range cfgs {
		fmt.Fprintf(p.stdout, "%s: %s (%s, %s) - %s\n", c.Name, c.Shown, c.Type, c.Scope, results[i])
	}
	for _, name := range waiting {
		fmt.Fprintf(p.stdout, "%s: in the project's .mcp.json, not used until this folder is trusted (gnosh trust)\n", name)
	}
	for _, pr := range probs {
		fmt.Fprintf(p.stdout, "not in force: %s\n", pr.String())
	}
	return cli.ExitOK
}

// mcpLoad reads the servers this folder uses: those in force, the
// problems of the files, and the project's servers that wait for the
// folder to be trusted. A nil startup comes with the exit code.
func (p *program) mcpLoad(ctx context.Context, inv cli.Invocation, places mcpPlaces) (st *startup, cfgs []mcp.Config, probs []mcp.Problem, waiting []string, code int) {
	if st, code = p.prepared(ctx, inv); st == nil {
		return nil, nil, nil, nil, code
	}
	var keys []string
	for _, pc := range st.cfg.Providers {
		if v, ok := strings.CutPrefix(pc.KeyFrom, "$"); ok {
			keys = append(keys, v)
		}
	}
	getenv := func(name string) (string, bool) {
		v, ok := p.env[name]
		return v, ok
	}
	trusted := st.decision.Trusted
	cfgs, probs = mcp.LoadConfig(mcp.Sources{User: places.user, Project: places.project, Root: places.root,
		Trusted: trusted, Getenv: getenv, Withheld: keys})
	// A project's servers in a folder that is not trusted are named, and
	// left alone.
	if !trusted {
		theirs, _ := mcp.LoadConfig(mcp.Sources{Project: places.project, Root: places.root, Trusted: true, Getenv: getenv, Withheld: keys})
		for _, c := range theirs {
			waiting = append(waiting, c.Name)
		}
	}
	return st, cfgs, probs, waiting, cli.ExitOK
}

// mcpLogins is where the sign-ins are kept, for a command of the shell.
func (st *startup) mcpLogins() *mcp.LoginStore {
	return &mcp.LoginStore{Path: filepath.Join(st.cfg.Paths.StateDir, "mcp-logins.json")}
}

// mcpGet is `gnosh mcp get`.
func (p *program) mcpGet(ctx context.Context, name string, cfgs []mcp.Config, waiting []string, places mcpPlaces, st *startup) int {
	i := slices.IndexFunc(cfgs, func(c mcp.Config) bool { return c.Name == name })
	if i < 0 {
		if slices.Contains(waiting, name) {
			fmt.Fprintf(p.stdout, "%s is in the project's .mcp.json (%s), and not used until this folder is trusted (gnosh trust).\n", name, places.project)
			return cli.ExitOK
		}
		p.errf("there is no server named %s here. gnosh mcp list names the servers.", name)
		return cli.ExitFailure
	}
	c := cfgs[i]
	fmt.Fprintf(p.stdout, "%s:\n", c.Name)
	fmt.Fprintf(p.stdout, "  scope: %s\n", places.where(c.Scope))
	fmt.Fprintf(p.stdout, "  type: %s\n", c.Type)
	// What is shown is the entry as it is written, with ${VAR} unfilled,
	// and of its variables and headers only the names: their values may be
	// keys.
	raw, _ := mcp.Lookup(places.place(c.Scope), name)
	var e struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	_ = json.Unmarshal(raw, &e)
	names := func(m map[string]string) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ", ")
	}
	if c.Type == "stdio" {
		fmt.Fprintf(p.stdout, "  command: %s\n", e.Command)
		if len(e.Args) > 0 {
			fmt.Fprintf(p.stdout, "  args: %s\n", strings.Join(e.Args, " "))
		}
		if len(e.Env) > 0 {
			fmt.Fprintf(p.stdout, "  env: %s (values not shown)\n", names(e.Env))
		}
	} else {
		fmt.Fprintf(p.stdout, "  url: %s\n", e.URL)
		if len(e.Headers) > 0 {
			fmt.Fprintf(p.stdout, "  headers: %s (values not shown)\n", names(e.Headers))
		}
	}
	if c.Timeout > 0 {
		fmt.Fprintf(p.stdout, "  timeout of a call: %s\n", c.Timeout)
	}
	if c.Type == "http" {
		if _, ok := st.mcpLogins().Get(c.URL); ok {
			fmt.Fprintf(p.stdout, "  sign-in: kept (gnosh mcp logout %s forgets it)\n", c.Name)
		}
	}
	fmt.Fprintf(p.stdout, "  status: %s\n", p.mcpCheckAll(ctx, []mcp.Config{c}, places.root, st)[0])
	fmt.Fprintf(p.stdout, "To remove it: gnosh mcp remove %s -s %s\n", c.Name, c.Scope)
	return cli.ExitOK
}

// mcpCheckAll connects to each server, all at once, and says how each
// went: connected, with how many tools, or why not. A started server
// starts in the project's root, as in a session.
func (p *program) mcpCheckAll(ctx context.Context, cfgs []mcp.Config, root string, st *startup) []string {
	out := make([]string, len(cfgs))
	var wg sync.WaitGroup
	for i, c := range cfgs {
		wg.Go(func() { out[i] = p.mcpCheck(ctx, c, root, st) })
	}
	wg.Wait()
	return out
}

func (p *program) mcpCheck(ctx context.Context, c mcp.Config, root string, st *startup) string {
	timeout := st.cfg.Limits.MCPTimeout.V
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	opt := mcp.Options{ClientName: "gnosh", ClientVersion: firstOf(p.o.BuildID, "devel"), StartTimeout: timeout}
	if c.Type == "http" {
		opt.Auth = &mcp.Authorizer{Logins: st.mcpLogins(), Server: c.URL, HTTP: &http.Client{Timeout: 30 * time.Second}}
	}
	client, err := mcpConnect(ctx, c, root, p.mcpEnviron(), []string{root}, opt)
	if _, ok := errNeedsAuth(err); ok {
		return "wants a sign-in: gnosh mcp login " + c.Name
	}
	if err != nil {
		return "not connected: " + oneLine(err.Error(), 300)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx)
	if err != nil {
		return "connected, and its tools could not be listed: " + oneLine(err.Error(), 300)
	}
	info := client.Info()
	return fmt.Sprintf("connected (%s %s), %s", firstOf(info.Name, "the server"), info.Version, countWords(len(tools), "tool", "tools"))
}

// mcpSignIn is `gnosh mcp login` and `logout`: the shell's way to sign in
// to a server at a URL, as /mcp login is a session's. gnosh says where to
// sign in, and waits for the browser to come back to this machine, or for
// the address it tried to go to, pasted on a line.
func (p *program) mcpSignIn(ctx context.Context, inv cli.Invocation, places mcpPlaces) int {
	st, cfgs, _, waiting, code := p.mcpLoad(ctx, inv, places)
	if st == nil {
		return code
	}
	name := inv.Args[0]
	i := slices.IndexFunc(cfgs, func(c mcp.Config) bool { return c.Name == name })
	switch {
	case i < 0 && slices.Contains(waiting, name):
		p.errf("%s is in the project's .mcp.json, and not used until this folder is trusted (gnosh trust).", name)
		return cli.ExitFailure
	case i < 0:
		p.errf("there is no server named %s here. gnosh mcp list names the servers.", name)
		return cli.ExitFailure
	case cfgs[i].Type != "http":
		p.errf("%s is a server gnosh starts, not one at a URL: it has no sign-in.", name)
		return cli.ExitFailure
	}
	c := cfgs[i]
	store := st.mcpLogins()
	if inv.Command == cli.CmdMCPLogout {
		had, err := store.Delete(c.URL)
		switch {
		case err != nil:
			p.errf("the sign-in to %s could not be forgotten: %v", name, err)
			return cli.ExitFailure
		case !had:
			fmt.Fprintf(p.stdout, "No sign-in to %s is kept.\n", name)
		default:
			fmt.Fprintf(p.stdout, "The sign-in to %s is forgotten (the authorization server may still hold the token until it runs out).\n", name)
		}
		return cli.ExitOK
	}
	// What the server says without a sign-in names its metadata.
	www := ""
	probe := mcp.Options{ClientName: "gnosh", ClientVersion: firstOf(p.o.BuildID, "devel"), StartTimeout: st.cfg.Limits.MCPTimeout.V}
	pctx, cancel := context.WithTimeout(ctx, st.cfg.Limits.MCPTimeout.V)
	client, err := mcpConnect(pctx, c, places.root, p.mcpEnviron(), []string{places.root}, probe)
	cancel()
	if na, ok := errNeedsAuth(err); ok {
		www = na.WWWAuthenticate
	} else if err == nil {
		client.Close()
	}
	si, err := startSignIn(ctx, c, www, store)
	if err != nil {
		p.errf("%v", err)
		return cli.ExitFailure
	}
	defer si.stop()
	address := si.flow.URL()
	fmt.Fprintf(p.stdout, "Sign in to %s at this address:\n%s\n", name, address)
	if !inv.NoBrowser && openBrowser(address) {
		fmt.Fprintln(p.stdout, "(A browser was opened at it.)")
	}
	fmt.Fprintln(p.stdout, "When the browser cannot come back to this machine (gnosh runs over ssh), paste here the address it tried to open, and Enter.")
	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(p.o.Stdin)
		sc.Buffer(make([]byte, 64<<10), 64<<10)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) != "" {
				lines <- sc.Text()
				return
			}
		}
	}()
	var back url.Values
	select {
	case back = <-si.back:
	case line := <-lines:
		if back, err = mcp.Callback(line); err != nil {
			p.errf("%v", err)
			return cli.ExitFailure
		}
	case <-time.After(mcpLoginWait):
		p.errf("no answer came from the browser in %s: the sign-in to %s is given up.", mcpLoginWait, name)
		return cli.ExitFailure
	case <-ctx.Done():
		return cli.ExitFailure
	}
	if err := finishSignIn(ctx, c, si, back, store); err != nil {
		p.errf("%v", err)
		return cli.ExitFailure
	}
	fmt.Fprintf(p.stdout, "Signed in to %s: %s\n", name, p.mcpCheck(ctx, c, places.root, st))
	return cli.ExitOK
}
