package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/hooks"
	"github.com/gnosh-ai/gnosh/internal/permission"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
)

// The settings files are looked at again while the session runs, before a
// request to the model, as Claude Code takes in a change to its settings
// files: a host added to gnosh.json can be named in the next call, and
// permission rules written in or taken out by hand are in force, or gone,
// from then on. The rest of what the files say (hooks, env, options, the
// model) takes effect in a new session, and the person is told so. A file
// that gnosh would not load now leaves the session as it was, and the
// person is told why.
//
// (Box 1, 16 November: a host added to gnosh.json mid-session was no host
// the session knew, and the model, told so, tried to reach the machine
// with ssh spelled so as to slip past the refusal.)

// settingsWatch is how the settings files stood when they were last
// looked at: a few stats before each request, which is all a look costs.
type settingsWatch struct {
	mu sync.Mutex
	// stamps are the files as they were last looked at, and accepted as
	// they were last taken in: a change a ConfigChange hook blocked has
	// been seen and not accepted, so that no later reload, for another
	// file's change, takes it in unchecked. recheck says the hooks were
	// stopped before they had judged, so the files are looked at again.
	stamps, accepted map[string]fileStamp
	recheck          bool
	// hosts are the hosts in force since the settings were read again; nil
	// while they are the ones the session started with.
	hosts []config.Host
}

// currentHosts are the hosts in force: the ones the session started with,
// or those the settings declared since.
func (l *live) currentHosts() []config.Host {
	w := &l.settings
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hosts != nil {
		return w.hosts
	}
	return l.cfg.Hosts
}

// fileStamp is a file's state, as far as telling a change goes.
type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

// stampOf is how the file at path stands now.
func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: info.ModTime(), size: info.Size(), ok: true}
}

// settingsPaths are the settings files the session reads, there or not:
// the person's own gnosh.json, the project's, the project's local file, and
// a file --settings names.
func (l *live) settingsPaths() []string {
	p := l.cfg.Paths
	out := []string{p.UserSettings, p.LocalSettings}
	if project := filepath.Join(l.st.inputs.Cwd, "gnosh.json"); project != p.UserSettings {
		out = append(out, project)
	}
	// The --settings file as the loader read it (absFrom: a ~ filled in,
	// a relative path taken from where gnosh was started). Made again from
	// the flag, it missed both, and with --cwd named another file, so
	// edits to the real one were never taken in (the config review of
	// 2026-11-19).
	if s := strings.TrimSpace(l.st.inputs.Flags.Settings); s != "" && !strings.HasPrefix(s, "{") {
		out = append(out, s)
	}
	return out
}

// sawOwnWrite notes a settings file gnosh itself just wrote (saved
// approvals, /permissions, /skills): what it put in force already is not
// a change to take in again. before is how the file stood when gnosh read
// it to write it: when that is not how gnosh last took it in, the person
// changed it by hand meanwhile, the write holds their change too, and it
// is taken in as any change is; marked as gnosh's own, a deny rule they
// added while a question waited was never put in force (the approvals
// review of 2026-11-19).
func (l *live) sawOwnWrite(path string, before fileStamp) {
	w := &l.settings
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stamps != nil && w.accepted[path] == before {
		s := stampOf(path)
		w.stamps[path], w.accepted[path] = s, s
	}
}

// watchSettings notes how the settings files stand as the session starts:
// what it loaded is what it has accepted, so that a change made before the
// first request is a change too.
func (l *live) watchSettings() {
	w := &l.settings
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stamps, w.accepted = map[string]fileStamp{}, map[string]fileStamp{}
	for _, path := range l.settingsPaths() {
		s := stampOf(path)
		w.stamps[path], w.accepted[path] = s, s
	}
}

// settingsChanged is a moment before each request to the model: when a
// settings file changed since it was last looked at, the configuration is
// read again and what can be put in force is (applySettings); the model is
// told of hosts that came or went.
func (l *live) settingsChanged(ctx context.Context, info agent.RequestInfo) ([]agent.Part, error) {
	w := &l.settings
	w.mu.Lock()
	if w.stamps == nil {
		// (Not noted as the session started: this first look is the
		// baseline.)
		w.stamps, w.accepted = map[string]fileStamp{}, map[string]fileStamp{}
		for _, path := range l.settingsPaths() {
			s := stampOf(path)
			w.stamps[path], w.accepted[path] = s, s
		}
		w.mu.Unlock()
		return nil, nil
	}
	changed := w.recheck
	w.recheck = false
	// pending are the files that are not as they were last taken in: the
	// ones that changed now, and any whose change was blocked before.
	var pending []string
	for _, path := range l.settingsPaths() {
		s := stampOf(path)
		if old, seen := w.stamps[path]; seen && old != s || !seen && s.ok {
			changed = true
		}
		w.stamps[path] = s
		if acc, ok := w.accepted[path]; ok && acc != s || !ok && s.ok {
			pending = append(pending, path)
		}
	}
	w.mu.Unlock()
	if !changed || len(pending) == 0 {
		return nil, nil
	}
	if pending = l.sandboxHeldSettings(pending); len(pending) == 0 {
		return nil, nil
	}
	names := make([]string, len(pending))
	for i, p := range pending {
		names[i] = l.tilde(p)
	}
	which := strings.Join(names, " and ")
	why, blocked, stopped := l.configChange(ctx, pending)
	switch {
	case stopped:
		// Judged again before the next request: nothing is taken in
		// that its hooks did not pass.
		w.mu.Lock()
		w.recheck = true
		w.mu.Unlock()
		return nil, nil
	case blocked:
		l.flog.logf("settings", "%s changed, and a ConfigChange hook blocked it", which)
		l.notice(agent.LevelWarning, "settings", fmt.Sprintf("%s changed, and a ConfigChange hook the user set up blocked it: the session keeps the settings it had. %s", which, oneLine(why, 300)))
		return nil, nil
	}
	cfg, problems, err := config.Load(l.st.inputs)
	if err != nil || cfg == nil {
		why := "it could not be read"
		if errs := config.Errors(problems); len(errs) > 0 {
			why = errs[0].String()
		} else if err != nil {
			why = err.Error()
		}
		l.notice(agent.LevelWarning, "settings", fmt.Sprintf("%s changed, and gnosh would not load the settings now (%s): the session keeps the settings it started with.", which, oneLine(why, 300)))
		return nil, nil
	}
	parts := l.applySettings(cfg, which)
	w.mu.Lock()
	for _, p := range pending {
		w.accepted[p] = w.stamps[p]
	}
	w.mu.Unlock()
	return parts, nil
}

// configChange runs the ConfigChange hooks for each settings file that is
// not as it was last taken in, told which it is, and reports whether one
// blocked the change (and why), or whether they were stopped before they
// had judged: either way none of the new settings is taken in. A file
// named by --settings is no source Claude Code has, and runs none.
func (l *live) configChange(ctx context.Context, changed []string) (why string, blocked, stopped bool) {
	if !l.hooks.has(hooks.ConfigChange) {
		return "", false, false
	}
	var whys []string
	for _, path := range changed {
		source := l.settingsSource(path)
		if source == "" {
			continue
		}
		out, err := l.hooks.run(ctx, hooks.Input{Event: hooks.ConfigChange, Source: source, FilePath: path})
		switch {
		case err != nil:
			// (The turn was stopped while they ran.)
			return "", false, true
		case out.Blocked:
			whys = append(whys, firstOf(out.Reason, "It gave no reason."))
		}
	}
	return strings.Join(whys, " "), len(whys) > 0, false
}

// settingsSource is Claude Code's name for the settings file at path:
// user_settings, project_settings or local_settings; "" for another.
func (l *live) settingsSource(path string) string {
	p := l.cfg.Paths
	switch path {
	case p.UserSettings:
		return "user_settings"
	case p.LocalSettings:
		return "local_settings"
	case filepath.Join(l.st.inputs.Cwd, "gnosh.json"):
		return "project_settings"
	}
	return ""
}

// applySettings puts in force what of cfg can be while the session runs:
// the hosts, and the permission rules of the settings files. which names
// the files that changed, for the person.
func (l *live) applySettings(cfg *config.Config, which string) []agent.Part {
	var parts []agent.Part
	var done []string
	if l.shell != nil {
		// The whole of each entry: a host given a new address, or a new
		// description, under the same name kept its old one (the hosts
		// review of 2026-11-19).
		was, hosts := l.shell.Hosts(), shelltool.Declared(shellHosts(cfg))
		if !slices.Equal(was, hosts) {
			l.shell.SetHosts(hosts)
			l.settings.mu.Lock()
			l.settings.hosts = append([]config.Host{}, cfg.Hosts...)
			l.settings.mu.Unlock()
			done = append(done, "its hosts")
			var list []string
			for _, h := range hosts {
				list = append(list, h.Name+" ("+h.Target+")")
			}
			text := "The settings changed while you worked: no other machine is declared for this session now, so leave Bash's host out."
			if len(list) > 0 {
				text = "The settings changed while you worked: the machines Bash's host may name are now " + strings.Join(list, ", ") + ". Name one as host to run a command there; never reach one with ssh in a command."
			}
			parts = append(parts, agent.Part{Source: "settings", Text: prompt.Wrap(text)})
		}
	}
	// The rules of the settings files, in place of the ones they had; the
	// session's own (approved at a prompt, flags) stay as they are.
	for _, sc := range []permission.Scope{permission.ScopeUser, permission.ScopeProject, permission.ScopeLocal, permission.ScopeSettingsFlag} {
		l.engine.RemoveLayers(sc)
	}
	held := map[permission.List][]string{}
	for _, ly := range cfg.Permissions.Layers {
		layer := ruleLayer(ly)
		switch layer.Scope {
		case permission.ScopeUser, permission.ScopeProject, permission.ScopeLocal, permission.ScopeSettingsFlag:
			if err := l.engine.AddLayer(layer); err != nil {
				l.notice(agent.LevelWarning, "settings", fmt.Sprintf("A rule of %s could not be put in force (%v).", layer.Name, err))
			}
		}
		if layer.Scope == permission.ScopeLocal {
			held[permission.ListAllow] = append(held[permission.ListAllow], layer.Allow...)
			held[permission.ListAsk] = append(held[permission.ListAsk], layer.Ask...)
			held[permission.ListDeny] = append(held[permission.ListDeny], layer.Deny...)
		}
	}
	// The approvals kept at a prompt are what the local file holds: one
	// taken out of it by hand is no longer in force.
	l.engine.KeepOnly(held)
	done = append(done, "its permission rules")
	l.flog.logf("settings", "%s changed: %s in force", which, strings.Join(done, " and "))
	l.notice(agent.LevelInfo, "settings", fmt.Sprintf("%s changed: %s are in force now; the rest of what it says (hooks, env, options, the model) takes effect in a new session.", which, strings.Join(done, " and ")))
	return parts
}

// sandboxHeldSettings leaves out of the changes to take in the project's
// own settings files while the sandbox is on: a command in it may write
// in the project's folder, and one that wrote a settings file there (one
// that was not there to keep read-only) would have given itself rules
// for the next call, asking out of the sandbox among them (the sandbox
// review). They are read when the session starts again, and the person is
// told so, once for each change. The person's own files go on as before.
func (l *live) sandboxHeldSettings(pending []string) []string {
	if l.st == nil || !l.st.sandboxOf(l.cfg).on {
		return pending
	}
	project := []string{l.cfg.Paths.LocalSettings}
	if p := filepath.Join(l.st.inputs.Cwd, "gnosh.json"); p != l.cfg.Paths.UserSettings {
		project = append(project, p)
	}
	var keep, held []string
	for _, p := range pending {
		if slices.Contains(project, p) {
			held = append(held, p)
		} else {
			keep = append(keep, p)
		}
	}
	if len(held) > 0 {
		w := &l.settings
		w.mu.Lock()
		for _, p := range held {
			w.accepted[p] = w.stamps[p]
		}
		w.mu.Unlock()
		names := make([]string, len(held))
		for i, p := range held {
			names[i] = l.tilde(p)
		}
		l.notice(agent.LevelWarning, "settings", fmt.Sprintf("%s changed during the session. With the sandbox on, a command in it could have written it, so it is not taken in now: it is read when the session starts again (/restart).", strings.Join(names, " and ")))
	}
	return keep
}
