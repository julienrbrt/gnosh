package trust

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// What an untrusted folder's settings may still do: only restrict.
func TestRestricts(t *testing.T) {
	for kind, want := range map[string]bool{
		KindDenyRule: true, KindAskRule: true, KindDisabledTool: true, KindHiddenSkill: true,

		KindProvider: false, KindModel: false, KindHost: false, KindHook: false, KindHookOff: false,
		KindMCPLocal: false, KindMCPOnline: false, KindLSP: false, KindAllowRule: false,
		KindMode: false, KindAddDir: false, KindEnv: false, KindOption: false, KindExtraPath: false,
		KindAddress: false, KindRemoval: false, KindRunsCommand: false, KindGitProgram: false,
		KindSkillsLink: false, KindStoredState: false,

		// A kind nobody has heard of is held: the gate fails closed.
		"": false, "something-new": false, "DENY-RULE": false, "deny-rule ": false,
	} {
		if got := Restricts(kind); got != want {
			t.Errorf("Restricts(%q) = %v, want %v", kind, got, want)
		}
	}
}

// The words for the kinds of setting are shared with the config package,
// which cannot be imported here. Its test pins the same list: change one
// side and its test fails until both agree.
func TestKindsMatchTheConfigPackage(t *testing.T) {
	got := []string{
		KindProvider, KindModel, KindHost, KindHook, KindHookOff,
		KindAllowRule, KindAskRule, KindDenyRule, KindMode, KindAddDir, KindEnv, KindOption, KindExtraPath,
		KindAddress, KindHiddenSkill, KindRemoval,
	}
	want := []string{
		"provider", "model", "host", "hook", "hook-off",
		"allow-rule", "ask-rule", "deny-rule", "mode", "add-dir", "env", "option", "extra-path",
		"address", "hidden-skill", "removal",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the kinds changed:\n got %q\nwant %q\nChange internal/config/config.go and its test to match.", got, want)
	}
}

// One plain sentence per kind of harm, and only for the kinds this
// folder's settings could do.
func TestRisks(t *testing.T) {
	says := map[string]string{
		KindHook:        "run as you",
		KindMCPLocal:    "steal your SSH and wallet keys, wipe your files, or plant a program that stays",
		KindLSP:         "run as you",
		KindAllowRule:   "A line hidden in any file here could then get the agent to run what these settings allow, without asking you",
		KindMode:        "without asking you",
		KindProvider:    "read everything the agent sees, could take your API key, and could tell the agent what to do",
		KindMCPOnline:   "see what the agent sends it, and could steer the agent",
		KindEnv:         "a changed search path that swaps one program for another",
		KindHost:        "logging in to other machines with your SSH keys",
		KindSkillsLink:  "reading files outside this folder",
		KindExtraPath:   "reading files outside this folder",
		KindAddDir:      "reading files outside this folder",
		KindAddress:     "sending your work off this computer",
		KindRunsCommand: "Any command hidden in a setting runs as you",
		KindGitProgram:  "Git would run them as you",
		KindModel:       "unloads the one you are using",
		KindHookOff:     "switch off or remove things you set up yourself",
		KindRemoval:     "switch off or remove things you set up yourself",
		KindStoredState: "turn on things your own settings turned off",
		KindOption:      "change how gnosh behaves in this folder",
		KindAskRule:     "change how gnosh behaves in this folder",
		"something-new": "change how gnosh behaves in this folder",
	}
	for kind, want := range says {
		got := Risks([]Holding{{Kind: kind, Text: "x"}})
		if len(got) != 1 || !strings.Contains(got[0], want) {
			t.Errorf("a %s alone: %q, want one sentence holding %q", kind, got, want)
		}
	}
	if got := Risks(nil); len(got) != 0 {
		t.Errorf("nothing held, nothing to fear: %q", got)
	}
	// Several settings of one kind of harm give its sentence once, and the
	// sentences come in a fixed order.
	got := Risks([]Holding{
		{Kind: KindHost}, {Kind: KindOption}, {Kind: KindHook}, {Kind: KindLSP}, {Kind: KindMCPLocal}, {Kind: KindProvider}, {Kind: KindHook},
	})
	if len(got) != 4 || !strings.Contains(got[0], "run as you") || !strings.Contains(got[1], "API key") ||
		!strings.Contains(got[2], "SSH keys") || got[3] != otherRisk {
		t.Errorf("order and once each: %q", got)
	}
	// A folder with only a provider is told about the provider, and not
	// about commands it could not run.
	only := strings.Join(Risks([]Holding{{Kind: KindProvider}}), " ")
	for _, not := range []string{"run as you", "SSH keys", "without asking", "off this computer"} {
		if strings.Contains(only, not) {
			t.Errorf("a harm this folder's settings cannot do is mentioned: %q in %q", not, only)
		}
	}
}

func TestQuestion(t *testing.T) {
	f := Folder{Start: "/work/proj/sub", Root: "/work/proj", Key: "/work/proj"}
	held := []Holding{
		{Kind: KindHook, Text: "hook Stop: ./hooks/gate.sh", File: "/work/proj/gnosh.json", Line: 4},
		{Kind: KindAllowRule, Text: "allow Bash", File: "/work/proj/.gnosh/settings.local.json", Line: 2},
		{Kind: KindGitProgram, Text: "git core.fsmonitor = ./watch.sh", File: "/work/proj/.git/config"},
	}
	q := NewQuestion(f, []string{"/work/proj/gnosh.json", "/work/proj/.gnosh/settings.local.json"}, held)
	if q.Folder != "/work/proj" || q.SessionOnly || len(q.Files) != 2 {
		t.Errorf("question: %+v", q)
	}
	want := []string{
		"hook Stop: ./hooks/gate.sh  (/work/proj/gnosh.json, line 4)",
		"allow Bash  (/work/proj/.gnosh/settings.local.json, line 2)",
		"git core.fsmonitor = ./watch.sh  (/work/proj/.git/config)",
	}
	if !slices.Equal(q.Would, want) {
		t.Errorf("what the settings would do, in the order given:\n got %q\nwant %q", q.Would, want)
	}
	if len(q.Risks) != 3 {
		t.Errorf("risks: %q", q.Risks)
	}
	text := q.String()
	for _, piece := range []string{
		"Do you trust the files in this folder?", "/work/proj", "these files would be read", "  - /work/proj/.gnosh/settings.local.json\n",
		"hook Stop: ./hooks/gate.sh", "What could go wrong",
		"run as you", "without asking you", "Git would run them", "asks again next time", "gnosh trust --forget",
	} {
		if !strings.Contains(text, piece) {
			t.Errorf("the question should say %q:\n%s", piece, text)
		}
	}
	// "No" comes before "Yes": it is the answer selected first.
	if no, yes := strings.Index(text, "\nNo:"), strings.Index(text, "\nYes:"); no < 0 || yes < 0 || no > yes {
		t.Errorf("No is offered first:\n%s", text)
	}

	// Asked in the middle of a session (/cd), it says the same facts, on
	// no blank line, and leaves the choices to the asker.
	facts := q.Facts()
	for _, piece := range []string{"Do you trust the files in this folder?\n  /work/proj\n", "  - /work/proj/.gnosh/settings.local.json\n", "hook Stop: ./hooks/gate.sh", "Git would run them", "gnosh trust --forget undoes it"} {
		if !strings.Contains(facts, piece) {
			t.Errorf("the facts should say %q:\n%s", piece, facts)
		}
	}
	if strings.Contains(facts, "\n\n") || strings.HasSuffix(facts, "\n") || strings.Contains(facts, "No:") || strings.Contains(facts, "Yes:") {
		t.Errorf("the facts hold a blank line or the start's choices:\n%s", facts)
	}

	// The home directory: a yes lasts for the session.
	q = NewQuestion(Folder{Root: "/home/dev", Key: "/home/dev", SessionOnly: true}, nil, held)
	if !q.SessionOnly || !strings.Contains(q.String(), "for this session") || strings.Contains(q.String(), "from now on") {
		t.Errorf("the home directory's question: %s", q.String())
	}
	if !strings.Contains(q.Facts(), "A yes lasts for this session") {
		t.Errorf("the home directory's facts: %s", q.Facts())
	}
}

// Names and commands from a stranger's files are shown escaped, never as
// terminal control sequences.
func TestEscape(t *testing.T) {
	tests := map[string]string{
		"plain text, ünïcödé and 日本語": "plain text, ünïcödé and 日本語",
		"clear\x1b[2J\x1b[Hscreen":    `clear\x1b[2J\x1b[Hscreen`,
		"two\nlines\rback\ttab":       `two\x0alines\x0dback\x09tab`,
		"bell\x07 and delete\x7f":     `bell\x07 and delete\x7f`,
		"back\\slash":                 `back\\slash`,
		"bad\xffbyte":                 `bad\xffbyte`,
		"c1\u009bcsi":                 "c1" + `\u` + "009bcsi",
		"":                            "",
		"title\x1b]0;evil\x07":        `title\x1b]0;evil\x07`,
	}
	// The characters that reorder text on screen, written by number so
	// that this file holds none of them.
	for _, r := range []rune{0x85, 0x202e, 0x202a, 0x2066, 0x2069, 0x200f, 0xfeff} {
		tests["a"+string(r)+"b"] = fmt.Sprintf("a%s%04xb", `\u`, r)
	}
	for in, want := range tests {
		got := Escape(in)
		if got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
		for _, r := range got {
			if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
				t.Errorf("Escape(%q) still holds the control character %U", in, r)
			}
		}
	}

	// The whole question, notice and report, built from hostile text.
	evil := "evil\x1b[31m\nrm -rf ~" + string(rune(0x202e))
	f := Folder{Root: "/work/" + evil, Key: "/work/" + evil}
	held := []Holding{{Kind: KindHook, Text: "hook Stop: " + evil, File: "/work/" + evil + "/gnosh.json", Line: 1}}
	for name, text := range map[string]string{
		"question": NewQuestion(f, []string{"/work/" + evil + "/gnosh.json"}, held).String(),
		"notice":   Notice(f),
		"report":   Report(f, held),
	} {
		for _, r := range text {
			if r == 0x1b || r == 0x202e || r == '\r' {
				t.Errorf("the %s lets a control character through: %q", name, text)
			}
		}
		if !strings.Contains(text, `evil\x1b[31m\x0arm -rf ~\u202e`) {
			t.Errorf("the %s should show the text, escaped: %q", name, text)
		}
	}
}

// Without a person nothing is ever asked: the settings stay out, and one
// line on stderr says so, with the two ways to use them.
func TestNotice(t *testing.T) {
	n := Notice(Folder{Root: "/work/proj", Key: "/work/proj"})
	if strings.Contains(n, "\n") {
		t.Errorf("the notice is one line: %q", n)
	}
	for _, piece := range []string{"/work/proj", "not trusted", "project settings are off", "gnosh trust /work/proj", "--trust-folder"} {
		if !strings.Contains(n, piece) {
			t.Errorf("the notice should say %q: %s", piece, n)
		}
	}
}

func TestDecide(t *testing.T) {
	f := Folder{Root: "/work/proj", Key: "/work/proj"}
	yes := Verdict{Trusted: true, By: "/work/proj"}
	tests := []struct {
		name  string
		asked Asked
		want  Decision
	}{
		{"recorded", Asked{Folder: f, Verdict: yes, Interactive: true, Held: 3}, Decision{Trusted: true}},
		{"recorded, in a run", Asked{Folder: f, Verdict: yes, Held: 3}, Decision{Trusted: true}},
		{"--trust-folder, in a run", Asked{Folder: f, TrustFolder: true, Held: 3}, Decision{Trusted: true}},
		{"--trust-folder, with a person", Asked{Folder: f, TrustFolder: true, Interactive: true, Held: 3}, Decision{Trusted: true}},
		{"not trusted, a person, something held", Asked{Folder: f, Interactive: true, Held: 3}, Decision{Ask: true}},
		{"not trusted, a person, nothing held", Asked{Folder: f, Interactive: true}, Decision{}},
		{"not trusted, a run, something held", Asked{Folder: f, Held: 3}, Decision{Notice: Notice(f)}},
		{"not trusted, a run, nothing held", Asked{Folder: f}, Decision{}},
	}
	for _, tt := range tests {
		got := Decide(tt.asked)
		if got.Reason == "" {
			t.Errorf("%s: a decision says why", tt.name)
		}
		got.Reason = ""
		if got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
	// --trust-folder says that nothing is recorded.
	if d := Decide(Asked{Folder: f, TrustFolder: true}); !strings.Contains(d.Reason, "not recorded") {
		t.Errorf("--trust-folder: %q", d.Reason)
	}
}

// gnosh trust prints what the folder's settings now do, the same "what
// could go wrong", and how to undo it.
func TestReport(t *testing.T) {
	f := Folder{Root: "/work/feature", Key: "/work/main", Worktree: true}
	text := Report(f, []Holding{{Kind: KindProvider, Text: "provider x at https://203.0.113.5/v1", File: "/work/feature/gnosh.json", Line: 1}})
	for _, piece := range []string{
		"Trusted: /work/main", "main checkout", "provider x at https://203.0.113.5/v1", "What could go wrong",
		"could take your API key", "To undo: gnosh trust --forget /work/main",
	} {
		if !strings.Contains(text, piece) {
			t.Errorf("the report should say %q:\n%s", piece, text)
		}
	}
	text = Report(Folder{Root: "/work/plain", Key: "/work/plain"}, nil)
	if !strings.Contains(text, "nothing that was waiting for trust") || strings.Contains(text, "What could go wrong") || !strings.Contains(text, "--forget /work/plain") {
		t.Errorf("a folder with nothing held:\n%s", text)
	}
}

// A path that is a link out of the folder leads out; one that stays in
// does not.
func TestLeadsOut(t *testing.T) {
	tmp := root(t)
	proj := mkdir(t, tmp, "proj")
	inside := mkdir(t, proj, ".gnosh", "skills")
	outside := mkdir(t, tmp, "elsewhere", "skills")
	symlink(t, outside, filepath.Join(proj, "linked-out"))
	symlink(t, inside, filepath.Join(proj, "linked-in"))
	symlink(t, filepath.Join(tmp, "nowhere"), filepath.Join(proj, "dangling"))
	for path, want := range map[string]bool{
		inside:                                 false,
		proj:                                   false,
		filepath.Join(proj, "linked-in"):       false,
		filepath.Join(proj, "linked-out"):      true,
		filepath.Join(proj, "dangling"):        true,
		filepath.Join(proj, "not-there"):       true,
		outside:                                true,
		filepath.Join(proj, "..", "elsewhere"): true,
	} {
		target, out := LeadsOut(path, proj)
		if out != want {
			t.Errorf("LeadsOut(%s) = %v (%s), want %v", path, out, target, want)
		}
	}
	if target, _ := LeadsOut(filepath.Join(proj, "linked-out"), proj); target != outside {
		t.Errorf("where the link leads is said: %q", target)
	}
	// Inside any one of the folders given is inside.
	if _, out := LeadsOut(filepath.Join(proj, "linked-out"), proj, filepath.Join(tmp, "elsewhere")); out {
		t.Error("a link into the second folder given leads out")
	}
}

// The person's own settings that would run a program in the folder are
// set apart from the folder's: they are not the folder's settings, and a
// No keeps them waiting too, which the No says.
func TestQuestionSetsThePersonsOwnApart(t *testing.T) {
	f := Folder{Root: "/work/repo"}
	q := NewQuestion(f, nil, []Holding{{Kind: KindMCPLocal, Text: "your MCP server notes (npx -y notes)", File: "/home/u/.config/gnosh/mcp.json", Own: true}})
	s := q.String()
	for _, want := range []string{"These settings of your own would run programs in this folder, and wait for a yes too:", "your MCP server notes",
		"Your own programs started here read what the folder holds", "No: start without the folder's settings, and with those of your own that run in it waiting"} {
		if !strings.Contains(s, want) {
			t.Errorf("the question has no %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Its own settings are not in force") {
		t.Errorf("the person's own were called the folder's:\n%s", s)
	}
	q = NewQuestion(f, []string{"/work/repo/gnosh.json"}, []Holding{
		{Kind: KindHook, Text: "a hook", File: "/work/repo/gnosh.json"},
		{Kind: KindLSP, Text: "your language server gopls", Own: true},
	})
	s = q.Facts()
	if !strings.Contains(s, "They would add:\n  - a hook") || !strings.Contains(s, "These settings of your own would run programs in this folder, and wait for a yes too:\n  - your language server gopls") {
		t.Errorf("both, in the facts:\n%s", s)
	}
}
