package deliver

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// DefaultMaxCopyBytes is the largest file that is copied back from a
// declared host when nothing says otherwise: one gibibyte. A larger file
// is still recorded, with its path on that machine, and is not served.
const DefaultMaxCopyBytes = 1 << 30

// Options says how the feature is made. The zero value is what the
// program itself uses.
type Options struct {
	// MaxCopyBytes is the largest file that is copied back from a declared
	// host to be served. Zero means DefaultMaxCopyBytes.
	MaxCopyBytes int64

	// Rand is where tokens and the account's key are drawn from. Nil means
	// crypto/rand, and nothing else is ever right outside a test.
	Rand io.Reader

	// served, when not nil, is told the address of every server the
	// feature starts, as it starts. It is for the package's own tests.
	served func(base string)
}

// source is the name the feature's notices go under.
const source = "deliver"

// process is what one run of the program shares: the servers it has
// started, one for each project it has delivered files of.
type process struct {
	opts Options

	mu      sync.Mutex
	servers map[string]*server // by the feature's folder in a project's data folder
}

// New returns the feature that makes deliverables work in the program. It
// is handed to app.Options.Features. Nothing is started and no port is
// opened until a session a person attends has a file to serve.
func New(o Options) app.Feature {
	if o.MaxCopyBytes <= 0 {
		o.MaxCopyBytes = DefaultMaxCopyBytes
	}
	p := &process{opts: o, servers: map[string]*server{}}
	return app.Feature{
		Name: "deliverables",
		// A call that records changes nothing, so the tool does not ask. A
		// call that hands over a file is judged as reading that file is,
		// and one that names a host as the command on that host that it
		// runs: the tool says which it is (Describe).
		Tools:    []app.ToolName{{Name: app.DeliverToolName, Quiet: true}},
		Session:  p.session,
		Shutdown: p.shutdown,
	}
}

// shutdown stops every server as the program ends.
func (p *process) shutdown(context.Context) {
	p.mu.Lock()
	servers := p.servers
	p.servers = map[string]*server{}
	p.mu.Unlock()
	for _, s := range servers {
		s.close()
	}
}

// join is the feature in one session: the session's list, and the way to
// the project's register and server.
type join struct {
	p *process
	s *app.Session
	// list is the session's deliverables, as its file records them.
	list *list
	// dir is the feature's folder in the project's data folder, and
	// listen where the server is to listen, or empty when the option
	// switches it off.
	dir    string
	listen string
	// hosts are the machines the session may name, by name, with the
	// destination ssh is given for each, as the session began; hostNames
	// keeps their order. The shell's table, which a settings change
	// replaces while the session runs, is read instead where there is
	// one (hostTable).
	hosts     map[string]string
	hostNames []string

	// starting is held while the server is looked for, so that it is
	// started, and the person told of it, once.
	starting sync.Mutex

	mu sync.Mutex
	// reg is the project's register, once it has been needed, and srv the
	// server that hands its files over. unserved says why there is no
	// server, when there is none and it was looked for.
	reg      *register
	srv      *server
	unserved string
}

// session is Feature.Session. Only the session a person is in gets
// anything: a sub-agent's work is the session's to hand over, and a run
// has nobody to show anything to and must never open a port.
func (p *process) session(ctx context.Context, s *app.Session) (*app.Contribution, error) {
	if s.SubAgent || !s.Interactive {
		return &app.Contribution{}, nil
	}
	j := &join{p: p, s: s, hosts: map[string]string{}}
	if s.Dirs.Project != "" {
		j.dir = filepath.Join(s.Dirs.Project, folderName)
	}
	if s.Config != nil {
		if at := s.Config.Options.DeliverablesServer.V; at != config.ServerOff {
			j.listen = at
		}
		for _, h := range s.Config.Hosts {
			if _, twice := j.hosts[h.Name]; !twice {
				j.hosts[h.Name] = h.SSH
				j.hostNames = append(j.hostNames, h.Name)
			}
		}
	}
	var entries []transcript.Entry
	if s.Transcript != nil {
		entries = s.Transcript.Entries()
	}
	j.list = fromEntries(entries)
	return &app.Contribution{
		Tools:       []core.Tool{&tool{j: j}},
		AfterStored: j.afterStored,
		Ready:       j.ready,
		Slash: []app.SlashCommand{{
			Name:    "deliverables",
			Aliases: []string{"artifacts"},
			Summary: "What the agent has produced for you: the latest version of each thing, with its address and its file.",
			Run:     j.slash,
		}},
	}, nil
}

// ready is Contribution.Ready: the session's agent exists and no prompt
// has gone out. A project that already has delivered files is served from
// now on, so that an address handed out by an earlier session works again;
// and the front end is handed the list the session's file holds.
func (j *join) ready(*agent.Agent) {
	// Nothing is made for a project that has delivered nothing: not the
	// account's key, not a folder, not a port.
	if j.listen != "" && j.dir != "" && exists(filepath.Join(j.dir, registerName)) {
		if reg, err := j.register(); err == nil {
			served, foreign, err := reg.count()
			if err == nil && served > 0 {
				j.server()
			}
			if err == nil && foreign > 0 && j.s.Notice != nil {
				j.s.Notice(agent.LevelInfo, source, fmt.Sprintf(
					"%s holds %s that this account's gnosh did not write: the seal does not fit this account's key. They are not served.",
					reg.path(), plural(foreign, "entry", "entries")))
			}
		}
	}
	j.show()
}

// plural writes a count with its noun.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// afterStored is Contribution.AfterStored: an entry is in the session's
// file. The result of a Deliver call that went well joins the list here,
// and not when the call returns, so that the list and the file never
// disagree: a call whose result was not kept (the turn was stopped and
// the call came back too late) is not on it.
func (j *join) afterStored(ctx context.Context, e agent.Entry, written []transcript.Entry) {
	if e.Kind != agent.EntryResult || e.ToolName != app.DeliverToolName || e.Denied || e.Interrupted || e.Result.IsError {
		return
	}
	if d, _, ok := app.DeliverableOf(e.Result.Meta); ok {
		j.list.put(d)
		j.show()
	}
}

// show hands the front end the list as it stands. What has not changed is
// not sent again: the wiring compares.
func (j *join) show() {
	if j.s.Deliverables != nil {
		j.s.Deliverables(j.shown())
	}
}

// shown is the list as a front end is given it: every entry as the
// session's file records it, and for a file the address it is served at
// now. That address is worked out afresh each time, from the register and
// the server of this run: the port a server got may differ from one run
// to the next, and a file is only served while there is a server.
func (j *join) shown() []app.Deliverable {
	entries := j.list.all()
	for i := range entries {
		if entries[i].Path != "" {
			entries[i].URL = j.address(entries[i].Host, entries[i].Path)
		}
	}
	return entries
}

// address is where the file at path on host is served by this run, or
// empty when it is not.
func (j *join) address(host, path string) string {
	j.mu.Lock()
	reg, srv := j.reg, j.srv
	j.mu.Unlock()
	if reg == nil || srv == nil {
		return ""
	}
	rec, ok := reg.byTarget(host, path)
	if !ok {
		return ""
	}
	return srv.address(rec)
}

// register returns the project's register, made the first time it is
// needed. Making it reads the account's key, and makes that key when the
// account has none yet. An error means files cannot be served, and says
// why.
func (j *join) register() (*register, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.reg != nil {
		return j.reg, nil
	}
	if j.dir == "" || j.s.Dirs.State == "" {
		return nil, fmt.Errorf("this session has no project data folder to keep the register of delivered files in")
	}
	key, err := loadKey(j.s.Dirs.State, j.p.opts.Rand)
	if err != nil {
		return nil, err
	}
	j.reg = newRegister(j.dir, key, j.p.opts.Rand, j.now)
	return j.reg, nil
}

// server returns the server that hands the project's files over, starting
// it the first time it is needed in this run of the program, or nil when
// files are not served: the option switches the server off, or it could
// not be started. The person is told, once, when the server is not where
// the option says, and when there is none though one was wanted. With the
// option off nothing is looked at and nothing is made.
func (j *join) server() *server {
	j.starting.Lock()
	defer j.starting.Unlock()
	j.mu.Lock()
	srv, unserved := j.srv, j.unserved
	j.mu.Unlock()
	if srv != nil || unserved != "" {
		return srv
	}
	if j.listen == "" {
		unserved = "the option deliverablesServer is off"
	} else {
		reg, err := j.register()
		if err == nil {
			srv, err = j.p.serve(reg, j.listen)
		}
		if err != nil {
			srv, unserved = nil, err.Error()
		}
	}
	j.mu.Lock()
	j.srv, j.unserved = srv, unserved
	j.mu.Unlock()

	if j.s.Notice != nil {
		switch {
		case srv != nil && srv.moved:
			j.s.Notice(agent.LevelInfo, source, fmt.Sprintf(
				"The port of %s is taken on this machine, so the files gnosh delivers are served at %s/ in this session, to this machine only. An address handed out earlier at %s now reaches whatever holds that port: another gnosh session, or a program that is not gnosh, which would see the address.", srv.asked, srv.base, srv.asked))
		case srv == nil && j.listen != "":
			j.s.Notice(agent.LevelWarning, source, fmt.Sprintf(
				"The files gnosh delivers cannot be served in this session: %s. Each is still listed with its path.", strings.TrimSuffix(unserved, ".")))
		}
	}
	return srv
}

// whyUnserved says why files are not served, in words for a model, once
// server has returned nil.
func (j *join) whyUnserved() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.unserved
}

// serve returns the process's server for a register's folder, starting it
// when the process has none for that folder yet. One process serves a
// project from one port, however many sessions it opens in it.
func (p *process) serve(reg *register, addr string) (*server, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.servers[reg.dir]; s != nil {
		return s, nil
	}
	s, err := serve(reg, addr)
	if err != nil {
		return nil, err
	}
	p.servers[reg.dir] = s
	if p.opts.served != nil {
		p.opts.served(s.base)
	}
	return s, nil
}

// now reads the clock the program was given.
func (j *join) now() time.Time {
	if j.s.Now != nil {
		return j.s.Now()
	}
	return time.Now()
}

// slash is /deliverables, which is also /artifacts: the list in plain
// lines. A front end that draws the list itself does so from the same
// entries (Session.Deliverables).
func (j *join) slash(ctx context.Context, c *app.SlashContext) error {
	if strings.TrimSpace(c.Args) != "" {
		return fmt.Errorf("/deliverables takes nothing after it: it lists what the agent has produced for you in this session")
	}
	for _, line := range app.DeliverablesText(j.shown()) {
		c.Say("%s", line)
	}
	return nil
}
