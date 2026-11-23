package cli

import "fmt"

// Relaunch says how the session a command line opened stands now, for
// RelaunchArgs. An empty field leaves what the command line said.
type Relaunch struct {
	// Session is the session's id, or the path of its file.
	Session string
	// Fresh says the session has written nothing yet, so it has no file to
	// resume: the new start makes it under its id (--session-id). (Not with
	// -n's name: a session -n named has its name written as it is made,
	// so a fresh one is one that /clear began, which -n did not name.)
	Fresh bool
	// Mode is the permission mode the session is in.
	Mode string
	// Model is the session's model. The new start is given it (--model)
	// whether or not the command line named one, so that a model picked
	// as the session started is kept.
	Model string
	// Effort is the session's thinking level. It takes the place of
	// --reasoning-effort where the command line gave one; EffortDefault
	// (the model's own default, as /effort auto sets) leaves the flag out,
	// so that the new start takes what the session saved.
	Effort string
	// Tree is the name of the --worktree work tree the session is in, or
	// that EnterWorktree moves it into: --worktree names it, added when
	// the command line had none. LeaveTree takes --worktree away: the
	// session goes back out of its work tree (ExitWorktree).
	Tree      string
	LeaveTree bool
	// AddDirs are the working directories /add-dir added while the
	// session ran: the new start is given them as --add-dir.
	AddDirs []string
	// Cwd is the folder /cd moved the session to, which --cwd names in
	// place of the command line's. --trust-folder was about the folder
	// the session left and goes, unless TrustFolder gives it again: the
	// new folder was trusted for the session only, which no record keeps.
	Cwd         string
	TrustFolder bool
}

// EffortDefault is Relaunch.Effort for a session whose thinking level is
// the model's own default.
const EffortDefault = "auto"

// RelaunchArgs is the command line that starts gnosh again in the session
// args opened (/restart): every flag of args, written out whole
// (--name=value), but the ones that chose the conversation (-c, -r,
// --session-id, --fork-session, -n), which --resume=<session> replaces,
// and with the prompt words left out, since the session has sent them.
// The permission mode, the model and the thinking level are the session's
// as they are now, a work tree whose name was made up is named, and the
// folders /add-dir added, and the one /cd moved the session to, go too. A
// session that started skipping permissions (-y, or --permission-mode
// bypassPermissions) and has since left that mode can go back to it.
func (t *Table) RelaunchArgs(args []string, r Relaunch) ([]string, error) {
	if r.Session == "" {
		return nil, fmt.Errorf("there is no session to resume")
	}
	p, err := t.walk(args)
	if err != nil {
		return nil, err
	}
	if p.help || p.cmd != t.root {
		return nil, fmt.Errorf("the command line did not open a session")
	}
	var out []string
	yolo, skipped, within, model, tree := false, false, false, false, false
	for _, o := range p.occs {
		switch name := o.f.main(); {
		case o.f.id == idContinue || o.f.id == idResume || o.f.id == idSessionID || o.f.id == idFork || name == "--name":
			continue
		case o.f.id == idBack:
			// (The new start is the run itself, never sent off again.)
			continue
		case o.f.id == idYolo:
			yolo, skipped = true, true
			continue
		case o.f.id == idMode && r.Mode != "":
			skipped = skipped || o.value == ModeBypassPermissions
			continue
		case name == "--allow-dangerously-skip-permissions":
			within = true
		case name == "--model" && r.Model != "":
			if !model {
				out = append(out, name+"="+r.Model)
			}
			model = true
			continue
		case name == "--reasoning-effort" && r.Effort == EffortDefault:
			continue
		case name == "--reasoning-effort" && r.Effort != "":
			out = append(out, name+"="+r.Effort)
			continue
		case name == "--worktree" && r.LeaveTree:
			continue
		case name == "--worktree" && r.Tree != "":
			if !tree {
				out = append(out, name+"="+r.Tree)
			}
			tree = true
			continue
		case (name == "--cwd" || name == "--trust-folder") && r.Cwd != "":
			continue
		}
		out = append(out, o.spelled()...)
	}
	if r.Model != "" && !model {
		out = append(out, "--model="+r.Model)
	}
	if r.Tree != "" && !tree && !r.LeaveTree {
		out = append(out, "--worktree="+r.Tree)
	}
	for _, d := range r.AddDirs {
		out = append(out, "--add-dir="+d)
	}
	if r.Cwd != "" {
		out = append(out, "--cwd="+r.Cwd)
		if r.TrustFolder {
			out = append(out, "--trust-folder")
		}
	}
	switch {
	case r.Mode == ModeBypassPermissions || r.Mode == "" && yolo:
		out = append(out, "--dangerously-skip-permissions")
	case r.Mode != "":
		out = append(out, "--permission-mode="+r.Mode)
		if skipped && !within {
			// Started skipping permissions, the session could go back to
			// it; the new start can too.
			out = append(out, "--allow-dangerously-skip-permissions")
		}
	}
	if r.Fresh {
		return append(out, "--session-id="+r.Session), nil
	}
	return append(out, "--resume="+r.Session), nil
}

// spelled is the flag as typed once more, written out whole: the main
// spelling, with its value after an equals sign, so that no value can be
// read as anything else. A list gives one flag for each argument it took.
func (o *occurrence) spelled() []string {
	name := o.f.main()
	switch o.f.kind {
	case kindText:
		return []string{name + "=" + o.value}
	case kindOptional, kindAttached:
		if o.has {
			return []string{name + "=" + o.value}
		}
	case kindList:
		// One flag for each argument that put something in the list: an
		// argument that put nothing ("," for rules, a blank path) would be
		// an empty list on its own, which is refused.
		var out []string
		for _, v := range o.raw {
			if items, _ := splitRules(v); o.f.list == listRules && len(items) == 0 || o.f.list != listRules && blank(v) {
				continue
			}
			out = append(out, name+"="+v)
		}
		if len(out) == 0 {
			// (A list with nothing in it parsed only where that means
			// something: --tools "".)
			out = []string{name + "="}
		}
		return out
	}
	return []string{name}
}
