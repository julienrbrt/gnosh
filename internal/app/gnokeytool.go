package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/shellquote"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// This file is the gnokey tool (spec/gnosh/harness-tools.md, TOOL-40 and
// TOOL-41; Jae asked for "a real Gno integration (gno toolchain, realm
// deploy, gnokey signing)"). The model runs gnokey, gno.land's tool for
// keys and transactions, with no shell. When the command signs, gnosh asks
// the person for the key's password at a masked prompt that names the key
// and shows the exact command, hands the password to gnokey on its
// standard input (by gnokey's own flag for that, which gnosh puts in
// itself), and takes it out of what gnokey printed. The model never sees
// the password, and neither do the session's file, its log or a hook.
// Commands that make, change or hand out keys are refused: they stay in
// the person's own terminal. Only a session somebody attends has the tool,
// and only where gnokey is installed; the shell refuses the password flag
// (shelltool), so that nothing but this tool's prompt can sign.

const gnokeyToolName = "gnokey"

const (
	// gnokeyPasswordFlag is gnokey's flag for a password on its standard
	// input: the one way it takes a password without a terminal.
	gnokeyPasswordFlag = "-insecure-password-stdin"
	// gnokeyOutput is the most of gnokey's output the model is given, and
	// gnokeyWait how long a gnokey command may run.
	gnokeyOutput = 30000
	gnokeyWait   = 5 * time.Minute
)

// gnokeyRuns are the gnokey commands the tool runs; gnokeyKeys are those
// that make, change or hand out keys, which it refuses by name. gnokey
// reads a command's name in any letter case.
var (
	gnokeyRuns = []string{"query", "list", "sign", "verify", "broadcast", "maketx", "version"}
	gnokeyKeys = []string{"add", "delete", "rotate", "import", "export", "generate", "recover"}
)

// gnokeyBools are gnokey's flags that take no value of their own; every
// other flag written without = takes the next word as its value (gnokey
// of November 2026, and of March 2026).
var gnokeyBools = []string{"broadcast", "quiet", "insecure-password-stdin", "dry-run", "prove", "session", "h", "help"}

// gnokeyMainnet names gno.land's main network, by its chain ID and the
// host of its node: what is signed there is real.
var (
	gnokeyMainnetIDs  = []string{"gnoland1", "gnoland-1"}
	gnokeyMainnetHost = "rpc.gno.land"
)

const gnokeyDescription = `Runs gnokey, gno.land's tool for keys and transactions, with the command line you give, and returns what it printed. There is no shell: give gnokey's words alone, quoted as you would type them, with no pipe, redirection or variable.

- Queries (gnokey query), listing keys (gnokey list), verifying, broadcasting a transaction that is already signed, and making one with -broadcast=false (maketx then prints it unsigned) run as they are.
- A command that signs (maketx, which signs and broadcasts unless given -broadcast=false; gnokey sign) asks the user for the key's password at a masked prompt that shows them the exact command. You never see the password: never ask the user for it, never put it in a command, and never try to sign in the shell. Do not pass -insecure-password-stdin: gnosh adds what is needed. If the user does not give the password, nothing is signed; say so and stop.
- Making, changing or handing out keys (add, delete, rotate, import, export, generate, recover) is refused: tell the user the command to run in their own terminal.

Put the command right after gnokey (gnokey maketx call ..., not gnokey -home x maketx ...); gnokey -config is refused, so give every flag on the command line. On gno.land's main network (chain ID gnoland1) a transaction moves real value: say so before you sign there.`

var gnokeySchema = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The gnokey command line, beginning with gnokey, as it would be typed."},"description":{"type":"string","description":"One line saying what the command does, for the user."},"working_dir":{"type":"string","description":"Optional: the directory to run it in, such as one that holds a package to add. The working directory when left out."}},"required":["command","description"],"additionalProperties":false}`)

// gnokeyTool is the gnokey tool of one session: bin is gnokey's path, its
// links followed, and was what the file there was when the session
// started, which it must still be when a call runs (a model that rebuilt
// or replaced gnokey would be handed the password).
type gnokeyTool struct {
	l   *live
	bin string
	was os.FileInfo
}

// newGnokeyTool is the tool for the gnokey at path, or nil when the file
// cannot be looked at.
func newGnokeyTool(l *live, path string) *gnokeyTool {
	bin, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil
	}
	info, err := os.Stat(bin)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return &gnokeyTool{l: l, bin: bin, was: info}
}

// gnokeyCall is a call of the tool, read: gnokey's arguments, the command
// they are (with maketx's own, for maketx), and, for one that signs, the
// key, the master account a session key acts for, and whether the chain
// is gno.land's main one; head is how many of the arguments name the
// command, after which gnosh puts the password's flag.
type gnokeyCall struct {
	args        []string
	command     string
	head        int
	signs       bool
	key         string
	master      string
	mainnet     bool
	dir         string
	description string
	// flags are the command's flags, by name, with their values.
	flags map[string][]string
	// fileAt is where in args the file or folder the transaction is made
	// of is named (maketx run's script, sign's -tx-path, addpkg's
	// -pkgdir), or -1; fileEq is the "-flag=" before it when flag and
	// value are one word.
	fileAt int
	fileEq string
}

// gnokeyFacts are the flags of a command that signs that say what it
// moves, pays and calls: the signing question lists them first, whole,
// whatever the command's length.
var gnokeyFacts = []string{"send", "to", "max-deposit", "deposit", "gas-fee", "gas-wanted", "pkgpath", "func", "args", "chainid", "remote"}

const (
	// gnokeyLineMax is the longest command that signs which the question
	// asking for the password shows: the person reads all of it.
	gnokeyLineMax = 2000
	// gnokeyFileMax and gnokeyFileLines bound the file a transaction is
	// made of, which the question shows whole.
	gnokeyFileMax   = 64 << 10
	gnokeyFileLines = 80
)

// hiddenIn returns the first character of w that a terminal draws as
// nothing or that changes what is drawn around it: a control character, a
// format character (the bidi controls, the zero-width ones, tags), or one
// text.CleanInvisible takes out.
func hiddenIn(w string) (rune, bool) {
	for _, r := range w {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 || unicode.Is(unicode.Cf, r) {
			return r, true
		}
		if r >= 0x80 {
			if s := string(r); func() bool { cleaned, _ := text.CleanInvisible(s); return cleaned != s }() {
				return r, true
			}
		}
	}
	return 0, false
}

func (t *gnokeyTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: gnokeyToolName, Description: gnokeyDescription, InputSchema: gnokeySchema}
}

// read reads a call as gnokey reads its command line, and says in words
// for the model what is wrong with it when something is.
func (t *gnokeyTool) read(input json.RawMessage, cwd string) (gnokeyCall, string) {
	var in struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		WorkingDir  string `json:"working_dir"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return gnokeyCall{}, fmt.Sprintf("the arguments cannot be read (%v): command is the gnokey command line, description one line saying what it does", err)
	}
	words, err := shellquote.Split(strings.TrimSpace(in.Command))
	if err != nil {
		return gnokeyCall{}, "the command cannot be read as gnokey's words: " + err.Error()
	}
	if len(words) == 0 || words[0] != gnokeyToolName {
		return gnokeyCall{}, `the command must begin with gnokey: give the gnokey command line as it would be typed ("gnokey query auth/accounts/g1...")`
	}
	c := gnokeyCall{args: words[1:], description: strings.TrimSpace(in.Description), dir: cwd}
	c.fileAt = -1
	for _, w := range c.args {
		if strings.ContainsAny(w, "\n\r") {
			return c, "a word of the command holds a line break, which the user would not be shown as one command: write the command on one line"
		}
		// A character drawn as nothing, or one that turns what is drawn
		// around, would let the question show the person a command other
		// than the one signed (the gno review of 2026-11-19).
		if r, hidden := hiddenIn(w); hidden {
			return c, fmt.Sprintf("a word of the command holds %U, a character a terminal draws as nothing or that changes what is drawn around it: the user would not read what is signed. Write the command without it", r)
		}
	}
	if len(c.args) == 0 || strings.HasPrefix(c.args[0], "-") {
		return c, "put gnokey's command right after gnokey, before any flag (gnokey query ..., gnokey maketx call ...)"
	}
	name := strings.ToLower(c.args[0])
	switch {
	case slices.Contains(gnokeyKeys, name):
		return c, fmt.Sprintf("gnokey %s makes, changes or hands out a key, which stays in the user's own terminal: tell the user the command to run there", name)
	case !slices.Contains(gnokeyRuns, name):
		return c, fmt.Sprintf("gnokey %s is not a command this tool runs; it runs gnokey %s", c.args[0], strings.Join(gnokeyRuns, ", "))
	}
	c.command, c.head = name, 1
	if name == "maketx" {
		if len(c.args) < 2 || strings.HasPrefix(c.args[1], "-") {
			return c, "put maketx's own command right after it (gnokey maketx call, run, send, addpkg or session)"
		}
		c.command, c.head = "maketx "+strings.ToLower(c.args[1]), 2
		if !slices.Contains([]string{"maketx call", "maketx run", "maketx send", "maketx addpkg", "maketx session"}, c.command) {
			return c, fmt.Sprintf("gnokey maketx %s is not a command this tool runs; maketx's are call, run, send, addpkg and session", c.args[1])
		}
	}
	// The flags and their values, and the words that are neither.
	// (With where each value stands in args.)
	flags := map[string][]string{}
	valAt, valEq := map[string][]int{}, map[string][]string{}
	var positional []string
	var posAt []int
	rest := c.args[c.head:]
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		if w == "--" {
			for k := i + 1; k < len(rest); k++ {
				positional, posAt = append(positional, rest[k]), append(posAt, c.head+k)
			}
			break
		}
		if !strings.HasPrefix(w, "-") || w == "-" {
			positional, posAt = append(positional, w), append(posAt, c.head+i)
			continue
		}
		fname, value, set := strings.Cut(strings.TrimLeft(w, "-"), "=")
		at, eq := c.head+i, ""
		if set {
			eq = w[:strings.IndexByte(w, '=')+1]
		}
		if !set && !slices.Contains(gnokeyBools, fname) && i+1 < len(rest) {
			i++
			value, at = rest[i], c.head+i
		}
		flags[fname] = append(flags[fname], value)
		valAt[fname], valEq[fname] = append(valAt[fname], at), append(valEq[fname], eq)
	}
	c.flags = flags
	switch {
	case flags["insecure-password-stdin"] != nil:
		return c, "the command passes gnokey's " + gnokeyPasswordFlag + " itself, which only gnosh may do: leave it out. When the command signs, gnosh asks the user for the key's password and hands it to gnokey itself"
	case flags["config"] != nil:
		return c, "gnokey -config reads flags from a file, which the user would not be shown: give every flag on the command line instead"
	}
	switch {
	case c.command == "sign":
		c.signs = true
	case strings.HasPrefix(c.command, "maketx "):
		off := false
		for _, v := range flags["broadcast"] {
			off = slices.Contains([]string{"false", "0", "f", "F", "FALSE", "False"}, v)
		}
		c.signs = !off
	}
	if c.signs {
		// gnokey's key is the first word that is no flag: the only one for
		// sign and most of maketx's, the one before the file for maketx
		// run.
		if len(positional) == 0 {
			return c, "the command signs, and the key's name is missing: gnokey takes the key that signs after the flags"
		}
		c.key = positional[0]
		if m := flags["master"]; len(m) > 0 {
			c.master = m[len(m)-1]
		}
		// What the transaction is made of, when that is a file: the
		// question shows it, and gnokey reads a copy of what was shown.
		// (gnokey takes a flag's last value.)
		last := func(name string) {
			if at := valAt[name]; len(at) > 0 {
				c.fileAt, c.fileEq = at[len(at)-1], valEq[name][len(at)-1]
			}
		}
		switch c.command {
		case "maketx run":
			if len(posAt) >= 2 {
				c.fileAt = posAt[1]
			}
		case "sign":
			last("tx-path")
		case "maketx addpkg":
			last("pkgdir")
		}
		if n := len(c.line()); n > gnokeyLineMax {
			return c, fmt.Sprintf("the command is %d characters long, more than the user can read in the question that asks for the key's password (%d at most): make it shorter, or tell the user the command to run in their own terminal", n, gnokeyLineMax)
		}
	}
	for _, v := range flags["chainid"] {
		c.mainnet = c.mainnet || slices.Contains(gnokeyMainnetIDs, v)
	}
	for _, v := range flags["remote"] {
		host := v
		if u, err := url.Parse(v); err == nil && u.Host != "" {
			host = u.Host
		}
		if h, _, _ := strings.Cut(host, ":"); strings.EqualFold(h, gnokeyMainnetHost) {
			c.mainnet = true
		}
	}
	if d := strings.TrimSpace(in.WorkingDir); d != "" {
		if !filepath.IsAbs(d) {
			d = filepath.Join(cwd, d)
		}
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			return c, fmt.Sprintf("working_dir %s is not a directory that is there", d)
		}
		c.dir = filepath.Clean(d)
	}
	// A file gnokey is to read that is no file, such as standard input
	// under another name, would read the password.
	words = slices.Clone(positional)
	for _, vs := range flags {
		words = append(words, vs...)
	}
	for _, w := range words {
		if w == "" {
			continue
		}
		p := w
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.dir, p)
		}
		if info, err := os.Stat(p); err == nil && !info.Mode().IsRegular() && !info.IsDir() {
			return c, fmt.Sprintf("%s is no file but a device, a pipe or a socket, which gnokey would read the password from: name a file", w)
		}
	}
	return c, ""
}

// line is the command as gnokey is run with it, without the password
// flag gnosh adds: what the person is shown.
func (c gnokeyCall) line() string {
	return gnokeyToolName + " " + shellquote.Words(c.args)
}

// Describe says what a call would run, so that the person is asked about
// the command itself, as for a shell command.
func (t *gnokeyTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	c, wrong := t.read(call.Input, env.Cwd)
	act := core.Action{Kind: core.ActionExecute, Summary: firstOf(c.description, "Run gnokey")}
	if len(c.args) > 0 {
		act.Command = c.line()
	}
	if wrong != "" {
		return act, errors.New(wrong)
	}
	return act, nil
}

// Run runs gnokey, asking for the key's password first when the command
// signs.
func (t *gnokeyTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	c, wrong := t.read(call.Input, env.Cwd)
	if wrong != "" {
		return core.ErrorResult("gnokey was not run: " + wrong + "."), nil
	}
	if now, err := os.Stat(t.bin); err != nil || !sameFile(t.was, now) {
		return core.ErrorResult("gnokey was not run: " + t.bin + " is not the gnokey this session started with (it was changed, replaced or removed since), and gnosh hands a password only to that one. Tell the user; a new session takes the gnokey there now."), nil
	}
	args := c.args
	var password string
	// signedBack is where gnokey sign's signed transaction goes when gnokey
	// signed a private copy (from), and from is that copy.
	var signedBack, from string
	var shownData []byte
	if c.signs {
		a := agent.FromContext(ctx)
		if a == nil {
			return core.ToolResult{}, errors.New("the gnokey tool was run outside a session")
		}
		var body []string
		if c.mainnet {
			body = append(body, "This is gno.land's main network: what it signs moves real value.")
		}
		who := "the key " + c.key
		if c.master != "" {
			who += ", for the account " + c.master
		}
		shown, data, name, why := c.signedFile()
		if why != "" {
			return core.ErrorResult("gnokey was not run: " + why + "."), nil
		}
		shownData = data
		// The question shows all of what is signed: what it sends, pays
		// and calls first, then the whole command, then the file it is
		// made of. It cut the command at 600 bytes, so a long -args hid a
		// -send after it (the gno review of 2026-11-19).
		body = append(body, "gnokey is to sign with "+who+":")
		if facts := c.factLines(); len(facts) > 0 {
			body = append(body, "What it sends, pays and calls:")
			body = append(body, facts...)
			body = append(body, "The whole command:")
		}
		body = append(body, "  "+c.line())
		body = append(body, shown...)
		if c.dir != env.Cwd {
			body = append(body, "It runs in "+t.l.tilde(c.dir)+".")
		}
		// The gnokey a password was last handed to, in any session, is
		// remembered: one rebuilt or replaced since (make install in a gno
		// checkout is routine, and the model can run it) is said, for the
		// person to stop if they did not do it (the gno review of
		// 2026-11-19).
		sum := t.binSum()
		if seen, ok := t.lastSigned(); ok && (seen.Path != t.bin || seen.Sum != sum) {
			body = append(body, "This gnokey ("+t.l.tilde(t.bin)+") is not the one gnosh last handed a password to: it was rebuilt or replaced since. If you did not do that yourself, press Esc.")
		}
		body = append(body, "The password goes to gnokey alone: the model, the session's file and its log never see it. Esc signs nothing and stops the turn.")
		ans, err := a.Ask(ctx, agent.Question{Kind: secretKind, Title: "Password for the key " + c.key, Text: strings.Join(body, "\n"), CallID: call.ID, Tool: gnokeyToolName})
		switch {
		case errors.Is(err, agent.ErrNobodyToAsk):
			return core.ErrorResult("gnokey was not run: the command signs, and nobody is at the terminal to type the key's password. Give the user the exact command to run in their own terminal, and stop."), nil
		case err != nil:
			return core.ToolResult{}, err
		}
		if password = ans.Text; password == "" {
			return core.ErrorResult("gnokey was not run: no password was given, so nothing was signed. Say so, and stop."), nil
		}
		if data != nil {
			// gnokey reads a copy of what the question showed, which
			// nothing else can change: the file itself could be changed
			// after it was shown (a command left running in the
			// background), and what is signed is what was read.
			dir, err := os.MkdirTemp("", "gnosh-gnokey-")
			if err != nil {
				return core.ErrorResult("gnokey was not run: a private copy of " + name + " could not be made (" + err.Error() + "), so nothing was signed."), nil
			}
			defer os.RemoveAll(dir)
			copyPath := filepath.Join(dir, filepath.Base(name))
			if err := os.WriteFile(copyPath, data, 0o600); err != nil {
				return core.ErrorResult("gnokey was not run: a private copy of " + name + " could not be written (" + err.Error() + "), so nothing was signed."), nil
			}
			args = slices.Clone(args)
			args[c.fileAt] = c.fileEq + copyPath
			if c.command == "sign" {
				// gnokey sign writes the signed transaction into the file
				// it read: it goes to the person's file after.
				signedBack, from = name, copyPath
				if !filepath.IsAbs(signedBack) {
					signedBack = filepath.Join(c.dir, signedBack)
				}
			}
		}
		// The flag goes right after the command's name, before its other
		// flags, where gnokey reads every flag.
		args = slices.Insert(slices.Clone(args), c.head, gnokeyPasswordFlag)
	}
	runCtx, cancel := context.WithTimeout(ctx, gnokeyWait)
	defer cancel()
	cmd := exec.CommandContext(runCtx, t.bin, args...)
	cmd.Dir = c.dir
	cmd.Env = t.l.ownCommandEnv()
	// gnokey runs without the terminal, as the shell's commands do: it
	// reads nothing typed at gnosh's.
	noTerminal(cmd)
	if c.signs {
		cmd.Stdin = strings.NewReader(password + "\n")
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	if runErr == nil && c.signs {
		t.signedWith()
	}
	if runErr == nil && signedBack != "" {
		if signed, err := os.ReadFile(from); err == nil && !bytes.Equal(signed, shownData) {
			mode := os.FileMode(0o600)
			if info, err := os.Stat(signedBack); err == nil {
				mode = info.Mode().Perm()
			}
			if err := os.WriteFile(signedBack, signed, mode); err != nil {
				out.WriteString("\ngnosh could not write the signed transaction to " + signedBack + ": " + err.Error())
			}
		}
	}
	text := scrubPassword(out.String(), password)
	password = ""
	if len(text) > gnokeyOutput {
		text = text[:gnokeyOutput] + fmt.Sprintf("\n[%d more bytes of gnokey's output were left out]", len(text)-gnokeyOutput)
	}
	text = strings.TrimRight(text, "\n")
	var exit *exec.ExitError
	switch {
	case runErr == nil:
		return core.TextResult(firstOf(text, "(gnokey printed nothing)")), nil
	case runCtx.Err() != nil && c.signs:
		// (Stopped after it had the password: it may have sent the
		// transaction already.)
		said := fmt.Sprintf("gnokey was stopped after it was given the password (it did not end within %s, or the turn was stopped), and the transaction may have been sent already. Check the account's sequence or the transaction before signing it again, or it may be paid for twice.", roundTime(gnokeyWait))
		if ctx.Err() != nil {
			return core.ErrorResult(said), nil
		}
		return core.ErrorResult(said + "\n" + text), nil
	case errors.As(runErr, &exit):
		said := fmt.Sprintf("gnokey exited with status %d.", exit.ExitCode())
		if runCtx.Err() != nil && ctx.Err() == nil {
			said = fmt.Sprintf("gnokey did not end within %s and was stopped.", roundTime(gnokeyWait))
		}
		if text != "" {
			said += "\n" + text
		}
		return core.ErrorResult(said), nil
	case ctx.Err() != nil:
		return core.ToolResult{}, ctx.Err()
	}
	return core.ErrorResult("gnokey could not be run: " + runErr.Error() + "."), nil
}

// gnokeySeen is the gnokey a password was last handed to: its path and the
// SHA-256 of the file.
type gnokeySeen struct {
	Path string `json:"path"`
	Sum  string `json:"sum"`
}

// seenPath is where gnosh keeps gnokeySeen, beside its other state.
func (t *gnokeyTool) seenPath() string {
	return filepath.Join(t.l.cfg.Paths.StateDir, "gnokey-seen.json")
}

// binSum is the SHA-256 of the session's gnokey, "" when it cannot be read.
func (t *gnokeyTool) binSum() string {
	f, err := os.Open(t.bin)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lastSigned is the gnokey a password was last handed to, if gnosh has
// handed one to any.
func (t *gnokeyTool) lastSigned() (gnokeySeen, bool) {
	var seen gnokeySeen
	data, err := os.ReadFile(t.seenPath())
	if err != nil || json.Unmarshal(data, &seen) != nil || seen.Path == "" {
		return seen, false
	}
	return seen, true
}

// signedWith remembers the session's gnokey as the one a password was last
// handed to.
func (t *gnokeyTool) signedWith() {
	if sum := t.binSum(); sum != "" {
		data, _ := json.Marshal(gnokeySeen{Path: t.bin, Sum: sum})
		_ = os.MkdirAll(filepath.Dir(t.seenPath()), 0o700)
		_ = os.WriteFile(t.seenPath(), data, 0o600)
	}
}

// factLines are the flags of the command that say what it sends, pays and
// calls, one to a line, whole.
func (c gnokeyCall) factLines() []string {
	var out []string
	for _, f := range gnokeyFacts {
		for _, v := range c.flags[f] {
			out = append(out, "  -"+f+" "+shellquote.Words([]string{v}))
		}
	}
	return out
}

// signedFile is what the question shows of the file or folder a
// transaction is made of; for a file, data is all of it, which gnokey is
// handed a copy of. why, when not empty, says why the command cannot be
// shown, and so is not signed.
func (c gnokeyCall) signedFile() (shown []string, data []byte, name, why string) {
	if c.fileAt < 0 {
		return nil, nil, "", ""
	}
	name = strings.TrimPrefix(c.args[c.fileAt], c.fileEq)
	p := name
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.dir, p)
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, nil, name, fmt.Sprintf("%s cannot be read (%v), and the user must be shown what is signed", name, err)
	}
	if info.IsDir() {
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, nil, name, fmt.Sprintf("the folder %s cannot be read (%v), and the user must be shown what is signed", name, err)
		}
		shown = append(shown, "It signs the package in "+name+", which the model may have written: read its files before you type the password:")
		for i, e := range entries {
			if i == 40 {
				shown = append(shown, fmt.Sprintf("  and %d more", len(entries)-40))
				break
			}
			if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
				shown = append(shown, fmt.Sprintf("  %s (%d bytes)", e.Name(), fi.Size()))
			} else {
				shown = append(shown, "  "+e.Name())
			}
		}
		return shown, nil, name, ""
	}
	if info.Size() > gnokeyFileMax {
		return nil, nil, name, fmt.Sprintf("%s is %d bytes, more than the question that asks for the password can show (%d at most), and the user must read what is signed: tell them the command to run in their own terminal", name, info.Size(), gnokeyFileMax)
	}
	if data, err = os.ReadFile(p); err != nil {
		return nil, nil, name, fmt.Sprintf("%s cannot be read (%v), and the user must be shown what is signed", name, err)
	}
	if !utf8.Valid(data) {
		return nil, nil, name, name + " is not text the question that asks for the password can show, and the user must read what is signed: tell them the command to run in their own terminal"
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > gnokeyFileLines {
		return nil, nil, name, fmt.Sprintf("%s has %d lines, more than the question that asks for the password can show (%d at most), and the user must read what is signed: tell them the command to run in their own terminal", name, len(lines), gnokeyFileLines)
	}
	shown = append(shown, "It signs what "+name+" holds, which the model may have written. This is all of it, and gnokey reads a copy of just this:")
	for _, l := range lines {
		if r, hidden := hiddenIn(strings.ReplaceAll(strings.TrimSuffix(l, "\r"), "\t", "    ")); hidden {
			return nil, nil, name, fmt.Sprintf("%s holds %U, a character a terminal draws as nothing or that changes what is drawn around it: the user would not read what is signed", name, r)
		}
		shown = append(shown, "  "+l)
	}
	return shown, data, name, ""
}

// scrubPassword takes a password out of what gnokey printed: as typed, and
// as JSON writes it. A password of fewer than three bytes is left: it
// would take out letters of every word.
func scrubPassword(text, password string) string {
	if len(password) < 3 {
		return text
	}
	text = strings.ReplaceAll(text, password, "[the password]")
	if quoted, err := json.Marshal(password); err == nil {
		if inner := string(quoted[1 : len(quoted)-1]); inner != password {
			text = strings.ReplaceAll(text, inner, "[the password]")
		}
	}
	return text
}
