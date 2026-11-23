package deliver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/core"
	gtext "github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
)

// The arguments of the tool.
const (
	argLabel  = "label"  // a short, stable name for the deliverable
	argTarget = "target" // the file's path, or an http or https address; left out, the call is a claim
	argHost   = "host"   // the declared machine the file is on; left out, this one
)

// maxLabel is the longest label, in characters.
const maxLabel = 80

// unrecorded begins every refusal: the model must not take a call that
// failed for a deliverable on the person's screen.
const unrecorded = "Nothing was recorded: "

// tool is the Deliver tool of one session.
type tool struct {
	j *join
	// judged is the file each call was described with, by call id, its
	// links followed then: what the person or the rules judged, which
	// the run is held to.
	judged sync.Map
}

// Spec is what the model is told about the tool. The words are the same in
// every session, whatever is declared and wherever files are served.
func (t *tool) Spec() core.ToolSpec {
	text := fmt.Sprintf(`Records a deliverable: the thing the user asked you to produce, such as a rendered video, a generated report or file, or a page you published. The user is usually on another machine and does not follow your tool calls. What you record here is shown to them, apart from the files you merely changed, and stays listed.

- Call it with %[1]s alone before you start on the thing. That claims it: the user sees it as pending.
- Call it again with the same %[1]s and a %[2]s once the thing exists. The target is the file's path, or an address that begins http:// or https://. A file must exist: one that is not there is refused and nothing is recorded.
- With %[3]s, the file is on a declared machine. Its path is read there (one that does not begin with a slash, from the account's home directory), and the file is copied back to this machine when it is at most %[4]s.
- The same label again takes the place of the entry before, so use one label for one thing, and the same label for a newer version of it. Letter case and extra blanks do not make a label a different one.

The result says what was recorded and, when gnosh serves the file, the URL to give the user.`,
		argLabel, argTarget, argHost, size(t.j.p.opts.MaxCopyBytes))
	return core.ToolSpec{
		Name:        app.DeliverToolName,
		Description: text,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"label":{"type":"string","description":"A short, stable name for the deliverable, such as \"demo video\": at most ` + strconv.Itoa(maxLabel) + ` characters."},` +
			`"target":{"type":"string","description":"The file's path, or an address that begins http:// or https://. Leave it out to claim the deliverable before it exists."},` +
			`"host":{"type":"string","description":"The name of the declared machine the file is on. Leave it out for a file on this machine."}` +
			`},"required":["label"],"additionalProperties":false}`),
	}
}

// request is one call of the tool, read and checked.
type request struct {
	// label is the label on one line, as it is shown.
	label string
	// target is the file's path or the address, and empty for a claim.
	// link says it is an address.
	target string
	link   bool
	// host is the declared machine the file is on, and empty for this one.
	host string
}

// scheme matches the start of an address of any kind: a name, a colon and
// two slashes.
var scheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// read reads and checks a call's arguments. An error is worded for the
// model, and is what Describe returns and what Run shows.
func (t *tool) read(raw json.RawMessage) (request, error) {
	var req request
	var fields map[string]json.RawMessage
	if raw = bytes.TrimSpace(raw); len(raw) > 0 {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return req, errors.New("the arguments are not one JSON object")
		}
	}
	text := func(name string) (string, error) {
		value := bytes.TrimSpace(fields[name])
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			return "", nil
		}
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return "", fmt.Errorf("%s must be a string", name)
		}
		return s, nil
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if name == argLabel || name == argTarget || name == argHost {
			continue
		}
		hint := ""
		if slices.Contains([]string{"path", "file", "file_path", "url", "uri", "link"}, strings.ToLower(name)) {
			hint = fmt.Sprintf(" The file's path or the address goes in %s.", argTarget)
		}
		return req, fmt.Errorf("%q is not an argument of %s, which takes %s, %s and %s.%s", name, app.DeliverToolName, argLabel, argTarget, argHost, hint)
	}

	label, err := text(argLabel)
	if err != nil {
		return req, err
	}
	req.label = cleanLabel(label)
	switch n := utf8.RuneCountInString(req.label); {
	case n == 0:
		return req, fmt.Errorf("%s is missing: give a short, stable name for what the user asked for, such as \"demo video\"", argLabel)
	case n > maxLabel:
		return req, fmt.Errorf("the %s is %d characters long, and a label is a short name of at most %d: the path or the address goes in %s, and what the thing is about goes in your reply", argLabel, n, maxLabel, argTarget)
	}

	target, err := text(argTarget)
	if err != nil {
		return req, err
	}
	target = strings.TrimSpace(target)
	if hidden(target) {
		return req, fmt.Errorf("the %s holds a line break or another character that cannot be seen, so it is not a path or an address as it stands", argTarget)
	}
	host, err := text(argHost)
	if err != nil {
		return req, err
	}
	if host = strings.TrimSpace(host); host == shelltool.LocalName {
		host = ""
	}
	if hidden(host) {
		return req, fmt.Errorf("the %s holds a character that cannot be seen, so it names no machine as it stands", argHost)
	}

	switch found := scheme.FindString(target); strings.ToLower(found) {
	case "":
		req.target, req.host = target, host
	case "http://", "https://":
		u, err := url.Parse(target)
		if err != nil || u.Hostname() == "" {
			return req, fmt.Errorf("the %s %s cannot be read as an address: it names no host", argTarget, target)
		}
		// An address is the same from every machine: a host says nothing
		// about it. It is kept as it was given, but for the letter case
		// of its first word, which a link is told by.
		req.target, req.link = strings.ToLower(found)+target[len(found):], true
	case "file://":
		u, err := url.Parse(target)
		if err != nil || u.Path == "" || (u.Host != "" && u.Host != "localhost") {
			return req, fmt.Errorf("the %s %s cannot be read as a file's address. Give the file's path as it is", argTarget, target)
		}
		req.target, req.host = u.Path, host
	default:
		return req, fmt.Errorf("the %s is a file's path or an address that begins http:// or https://, and %s is neither. If the thing is published somewhere, give the http or https address the user would open; if it is a file, give its path", argTarget, target)
	}
	if req.target == "" {
		// A claim: there is no file yet, so no machine to name.
		req.host = ""
	}
	return req, nil
}

// copyLimit is the largest file that is copied back from a host. With the
// server switched off nothing is served, so nothing is copied: the file is
// only looked for, which a limit of nothing does.
func (j *join) copyLimit() int64 {
	if j.listen == "" {
		return 0
	}
	return j.p.opts.MaxCopyBytes
}

// hostTable is the machines the session may name now, by name, with the
// destination ssh is given for each, and their names in order: the
// shell's table, which a settings change replaces while the session runs.
// (The table the session began with was kept: a question named the old
// address of a host whose file came from the new one, the hosts review of
// 2026-11-19.)
func (j *join) hostTable() (map[string]string, []string) {
	if j.s.Shell != nil {
		if shell := j.s.Shell(); shell != nil {
			where, names := map[string]string{}, []string{}
			for _, h := range shell.Hosts() {
				where[h.Name], names = h.Target, append(names, h.Name)
			}
			return where, names
		}
	}
	return j.hosts, j.hostNames
}

// unknownHost words the refusal of a machine that is not declared.
func (j *join) unknownHost(name string) error {
	hosts, hostNames := j.hostTable()
	if len(hostNames) == 0 {
		return fmt.Errorf("the %s %q was given, and no other machine is declared for this session. Leave %s out for a file on this machine. To deliver a file from another machine, ask the user to declare that machine as a host in gnosh's config", argHost, name, argHost)
	}
	listed := make([]string, len(hostNames))
	for i, n := range hostNames {
		listed[i] = n + " (" + hosts[n] + ")"
	}
	return fmt.Errorf("the %s %q is not a machine declared for this session. The declared hosts are: %s. Leave %s out for a file on this machine", argHost, name, strings.Join(listed, ", "), argHost)
}

// cwdOf is the directory a relative path of a call is read from.
func (j *join) cwdOf(env *core.ToolEnv) string {
	if env != nil && filepath.IsAbs(env.Cwd) {
		return env.Cwd
	}
	return j.s.Dirs.Cwd
}

// localPath makes the path of a file on this machine absolute: a leading
// ~ is the home directory, and a relative path is under cwd.
func (j *join) localPath(target, cwd string) string {
	switch home := j.s.Dirs.Home; {
	case home == "":
	case target == "~":
		target = home
	case strings.HasPrefix(target, "~/"):
		target = filepath.Join(home, target[2:])
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	return filepath.Clean(target)
}

// checkFile looks at a file on this machine that is to be delivered, and
// returns its size, or says in plain words why it cannot be handed over
// and what to do about it.
func checkFile(path, cwd string) (length int64, problem string) {
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		problem = fmt.Sprintf("there is no file at %s on this machine (the working directory is %s). A deliverable is recorded once it exists: create it first, or give the path it was really written to. If it is on another machine, name that machine as %s", path, cwd, argHost)
		if near := filepath.Join(cwd, filepath.Base(path)); near != path {
			if st, err := os.Stat(near); err == nil && st.Mode().IsRegular() {
				problem += ". There is a file of that name at " + near
			}
		}
		return 0, problem
	case errors.Is(err, os.ErrPermission):
		return 0, fmt.Sprintf("this account may not look at %s (permission denied), so whether the file is there cannot be told from here. Nothing under a folder that is closed to this account can be delivered", path)
	case err != nil:
		return 0, fmt.Sprintf("%s cannot be looked at: %v", path, err)
	case st.IsDir():
		return 0, fmt.Sprintf("%s is a directory. A deliverable is one file or one address: name the file the user would open, such as the page inside it or an archive of it", path)
	case !st.Mode().IsRegular():
		return 0, fmt.Sprintf("%s is not a regular file (it is a device, a pipe or a socket), so it cannot be handed over", path)
	}
	// Asked, not opened: a pipe put in the file's place since the look
	// above would hold an open for reading until something wrote to it
	// (the deliverables review of 2026-11-19).
	if err := mayRead(path); err != nil {
		return 0, fmt.Sprintf("%s is there, and this account may not read it (%v), so it cannot be handed over", path, err)
	}
	return st.Size(), ""
}

// Describe says what a call would do, for the person's rules and for the
// person who may be asked. A claim and an address change and reach
// nothing. A file on this machine is named as the file that would be
// handed over, so that the rules about files are held against it. A file
// on a host is fetched by a command on that machine, which is given
// exactly as it will run. An error is the refusal Run would give.
func (t *tool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	j := t.j
	req, err := t.read(call.Input)
	if err != nil {
		return core.Action{Kind: core.ActionOther, Summary: "Record a deliverable"}, err
	}
	switch {
	case req.target == "":
		return core.Action{Kind: core.ActionOther, Summary: fmt.Sprintf("Claim the deliverable %q, which is then shown as pending", req.label)}, nil
	case req.link:
		return core.Action{Kind: core.ActionOther, Summary: fmt.Sprintf("Record the deliverable %q: the address %s", req.label, req.target)}, nil
	case req.host != "":
		summary := fmt.Sprintf("Record the deliverable %q: the file %s on %s", req.label, req.target, req.host)
		hosts, _ := j.hostTable()
		where, known := hosts[req.host]
		if !known {
			return core.Action{Kind: core.ActionOther, Summary: summary}, j.unknownHost(req.host)
		}
		most := j.copyLimit()
		summary = fmt.Sprintf("Record the deliverable %q: look for %s on %s (%s)", req.label, req.target, req.host, where)
		if most > 0 {
			summary += fmt.Sprintf(" and copy it back to this machine, if it is at most %s, to serve it from here", size(most))
		}
		return core.Action{Kind: core.ActionExecute, Command: shelltool.CopyCommand(req.target, most), Summary: summary}, nil
	}
	cwd := j.cwdOf(env)
	path := j.localPath(req.target, cwd)
	action := core.Action{Kind: core.ActionRead, Paths: []string{path}, Summary: fmt.Sprintf("Record the deliverable %q: the file %s", req.label, path)}
	if _, problem := checkFile(path, cwd); problem != "" {
		return action, errors.New(problem)
	}
	if call.ID != "" {
		t.judged.Store(call.ID, realFile(path))
	}
	return action, nil
}

// realFile is a path with its links followed, or the path when they
// cannot be.
func realFile(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// Run carries out a call.
func (t *tool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	j := t.j
	req, err := t.read(call.Input)
	if err != nil {
		return refuse(err.Error()), nil
	}
	before, had := j.list.get(req.label)
	switch {
	case req.target == "":
		return j.claim(req, before, had), nil
	case req.link:
		d := app.Deliverable{Label: req.label, URL: req.target}
		return recorded(d, had && before.Delivered(), "Link: "+req.target, "The user is shown it."), nil
	case req.host != "":
		return j.fromHost(ctx, req, before, had), nil
	}
	cwd := j.cwdOf(env)
	// The file the call was judged by is the one it may deliver: a cd or
	// a link changed between the question and the run makes the same
	// words name another (the deliverables review of 2026-11-19).
	if v, ok := t.judged.LoadAndDelete(call.ID); ok {
		if was, now := v.(string), realFile(j.localPath(req.target, cwd)); was != now {
			return refuse(fmt.Sprintf("the call was judged for the file %s, and %s now leads to %s. Deliver it again, so that what is delivered is what is judged", was, req.target, now)), nil
		}
	}
	return j.local(ctx, req, cwd, before, had), nil
}

// refuse is the result of a call that recorded nothing.
func refuse(why string) core.ToolResult {
	return core.ErrorResult(unrecorded + strings.TrimSuffix(why, ".") + ".")
}

// recorded is the result of a call that delivered: what the model reads,
// and the entry as it now stands, which the front ends draw and the list
// is built from.
func recorded(d app.Deliverable, replaced bool, lines ...string) core.ToolResult {
	first := fmt.Sprintf("Recorded %q.", d.Label)
	if replaced {
		first = fmt.Sprintf("Recorded %q, in the place of the version recorded before.", d.Label)
	}
	res := core.TextResult(strings.Join(append([]string{first}, lines...), "\n"))
	res.Meta = map[string]any{app.MetaDeliverable: d, app.MetaReplaced: replaced}
	return res
}

// claim records that a deliverable is on its way. What was delivered under
// the label before stays on the entry until the new version takes its
// place: it is still the latest version there is.
func (j *join) claim(req request, before app.Deliverable, had bool) core.ToolResult {
	d := app.Deliverable{Label: req.label, Pending: true}
	text := fmt.Sprintf("Claimed %q: the user sees it as pending. When it exists, call %s again with the same %s and its %s.", req.label, app.DeliverToolName, argLabel, argTarget)
	switch {
	case had && before.Pending:
		d.Path, d.Host, d.URL = before.Path, before.Host, before.URL
		text = fmt.Sprintf("%q is claimed already, and the user sees it as pending. Call %s with the same %s and its %s when it exists.", req.label, app.DeliverToolName, argLabel, argTarget)
	case had && before.Delivered():
		d.Path, d.Host, d.URL = before.Path, before.Host, before.URL
		text = fmt.Sprintf("Claimed %q again: the user sees that a new version is pending, and the version recorded before stays listed until you deliver the new one with the same %s and its %s.", req.label, argLabel, argTarget)
	}
	res := core.TextResult(text)
	res.Meta = map[string]any{app.MetaDeliverable: d, app.MetaReplaced: false}
	return res
}

// local records a file on this machine. It is served from where it is,
// with its links followed once, now: the address hands over the file that
// was delivered, whatever its path is made to point at later.
func (j *join) local(ctx context.Context, req request, cwd string, before app.Deliverable, had bool) core.ToolResult {
	path := j.localPath(req.target, cwd)
	length, problem := checkFile(path, cwd)
	if problem != "" {
		return refuse(problem)
	}
	file, err := filepath.EvalSymlinks(path)
	if err != nil {
		file = path
	}
	d := app.Deliverable{Label: req.label, Path: path}
	lines := []string{fmt.Sprintf("File: %s (%s)", path, size(length))}
	address, note := j.serveFile(ctx, "", path, file, req.label)
	d.URL = address
	return recorded(d, had && before.Delivered(), append(lines, note...)...)
}

// fromHost records a file on a declared machine. It is looked for there
// and copied back by the road every command for that machine takes, and
// served from the copy. A file that is too large to copy is recorded with
// its path there and not served.
func (j *join) fromHost(ctx context.Context, req request, before app.Deliverable, had bool) core.ToolResult {
	hosts, _ := j.hostTable()
	if _, known := hosts[req.host]; !known {
		return refuse(j.unknownHost(req.host).Error())
	}
	var shell *shelltool.Manager
	if j.s.Shell != nil {
		shell = j.s.Shell()
	}
	if shell == nil {
		return refuse("this session has no way to run a command on another machine, so a file there cannot be looked for")
	}
	d := app.Deliverable{Label: req.label, Path: req.target, Host: req.host}
	replaced := had && before.Delivered()
	where := d.Where()
	most := j.copyLimit()

	if most == 0 {
		// Nothing is served, so the file is only looked for: a limit of
		// nothing copies nothing, and says how large the file is.
		dir, err := os.MkdirTemp("", "gnosh-deliver-")
		if err != nil {
			return refuse(fmt.Sprintf("no folder could be made on this machine to look for %s from (%v)", where, err))
		}
		defer os.RemoveAll(dir)
		c := shell.CopyFromHost(ctx, req.host, req.target, filepath.Join(dir, "look"), 0)
		if c.Problem != "" && !c.TooBig {
			return refuse(c.Problem)
		}
		return recorded(d, replaced, fmt.Sprintf("File: %s (%s)", where, size(c.Size)),
			fmt.Sprintf("No URL: gnosh is not serving delivered files in this session (%s), so the file was not copied to this machine. The user is shown its path on %s.", strings.TrimSuffix(j.unservedWhy(), "."), req.host))
	}

	reg, err := j.register()
	if err == nil {
		err = reg.makeFolder()
	}
	if err != nil {
		return refuse(fmt.Sprintf("%s could not be copied to this machine: %v", where, err))
	}
	final := reg.copyPath(req.host, req.target)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return refuse(fmt.Sprintf("%s could not be copied to this machine: the folder for the copy could not be made (%v)", where, err))
	}
	if err := noLinks(reg.dir, filepath.Dir(final)); err != nil {
		return refuse(fmt.Sprintf("%s could not be copied to this machine: %v", where, err))
	}
	incoming, err := os.CreateTemp(filepath.Dir(final), ".incoming-*")
	if err != nil {
		return refuse(fmt.Sprintf("%s could not be copied to this machine: no file could be made for the copy (%v)", where, err))
	}
	incoming.Close()
	defer os.Remove(incoming.Name())

	c := shell.CopyFromHost(ctx, req.host, req.target, incoming.Name(), most)
	switch {
	case c.TooBig:
		// A copy made when the file was smaller is no longer the file: it
		// is taken away, so that the address it had does not go on
		// serving an old version under the label.
		if dropped, _ := reg.drop(ctx, req.host, req.target); dropped {
			os.Remove(final)
		}
		return recorded(d, replaced, fmt.Sprintf("File: %s (%s)", where, size(c.Size)),
			fmt.Sprintf("No URL: the file is larger than the %s gnosh copies back from another machine, so it is not served from this one. The user is shown its path on %s. For something they can open from where they are, deliver a smaller version of it, or the address it is published at.", size(most), req.host))
	case c.Problem != "":
		return refuse(c.Problem)
	}
	if err := os.Rename(incoming.Name(), final); err != nil {
		return refuse(fmt.Sprintf("%s was copied to this machine, and the copy could not be put in its place (%v)", where, err))
	}
	lines := []string{fmt.Sprintf("File: %s (%s), copied to this machine.", where, size(c.Size))}
	address, note := j.serveFile(ctx, req.host, req.target, final, req.label)
	d.URL = address
	return recorded(d, replaced, append(lines, note...)...)
}

// serveFile puts a delivered file in the register and returns the address
// it is served at, with the lines the model reads about it. With no
// server, or when the register cannot be written, there is no address,
// and the lines say why: the file is recorded all the same.
func (j *join) serveFile(ctx context.Context, host, path, file, label string) (address string, note []string) {
	srv := j.server()
	if srv == nil {
		return "", []string{fmt.Sprintf("No URL: gnosh is not serving delivered files in this session (%s). The user is shown the path.", strings.TrimSuffix(j.unservedWhy(), "."))}
	}
	reg, err := j.register()
	var rec record
	if err == nil {
		rec, err = reg.add(ctx, host, path, file, label)
	}
	if err != nil {
		return "", []string{fmt.Sprintf("No URL: %v. The user is shown the path.", err)}
	}
	address = srv.address(rec)
	forward := ""
	if at := strings.TrimPrefix(srv.base, "http://"); at != "" {
		if _, port, err := net.SplitHostPort(at); err == nil {
			forward = fmt.Sprintf(" (ssh -L %s:%s <this machine>)", port, at)
		}
	}
	return address, []string{
		"URL: " + address,
		"The user is shown both. Give them the URL: gnosh serves the file there to this machine only, and from another machine it is reached through an ssh port forward" + forward + ".",
	}
}

// unservedWhy says why files are not served, once the server was looked
// for and there is none.
func (j *join) unservedWhy() string {
	if why := j.whyUnserved(); why != "" {
		return why
	}
	return "the option deliverablesServer is off"
}

// size writes a number of bytes the way a person reads it: "812 bytes",
// "48.2 MiB", "1 GiB". A size that is not known is said to be so.
func size(n int64) string {
	switch {
	case n < 0:
		return "size not known"
	case n == 1:
		return "1 byte"
	case n < 1024:
		return strconv.FormatInt(n, 10) + " bytes"
	}
	v, unit := float64(n), ""
	for _, u := range []string{"KiB", "MiB", "GiB", "TiB"} {
		v /= 1024
		unit = u
		if v < 1024 {
			break
		}
	}
	return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0") + " " + unit
}

// hidden reports whether s holds a control character, or one a terminal
// draws as nothing or that turns the order text is drawn in (a bidi
// override, a zero-width space): with one, what the person is shown is
// not what the link or the path is (the deliverables review of
// 2026-11-19). The joiners emoji and some scripts are written with stay.
func hidden(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0 || gtext.ShowInvisible(s) != s
}
