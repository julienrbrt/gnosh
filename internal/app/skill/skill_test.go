package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	sk, reason := Parse("---\nname: ship\ndescription: Ships the build.\nwhen_to_use: When a release is asked for.\nargument-hint: \"[tag]\"\narguments: [tag, branch]\ndisable-model-invocation: yes\nallowed-tools: Bash(git add *) Bash(git commit *), Read\nlicense: MIT\nmade-up: ignored\n---\n\n# Ship\n\nTag $tag.\n", "folder")
	if reason != "" {
		t.Fatalf("skipped: %s", reason)
	}
	if sk.Name != "ship" || sk.Description != "Ships the build. When a release is asked for." || sk.ArgumentHint != "[tag]" || !sk.NoModel || sk.NoUser {
		t.Errorf("the skill: %+v", sk)
	}
	if !reflect.DeepEqual(sk.Arguments, []string{"tag", "branch"}) || !reflect.DeepEqual(sk.AllowedTools, []string{"Bash(git add *)", "Bash(git commit *)", "Read"}) {
		t.Errorf("arguments %q, allowed tools %q", sk.Arguments, sk.AllowedTools)
	}
	if sk.Body != "# Ship\n\nTag $tag." || len(sk.Unbuilt) != 0 {
		t.Errorf("body %q, unbuilt %q", sk.Body, sk.Unbuilt)
	}

	// No settings at all: the folder names it and the first line of the
	// text describes it.
	sk, reason = Parse("# Review a change\n\nRead the diff.\n", "review")
	if reason != "" || sk.Name != "review" || sk.Description != "Review a change" || sk.NoModel {
		t.Errorf("a file without settings: %+v, %q", sk, reason)
	}
	// Settings that cannot be read keep the skill from being used at all:
	// one of them may have kept it from the model (the skills review of
	// 2026-11-19).
	sk, reason = Parse("---\n  : : bad\ndisable-model-invocation: true\n---\nDo it.\n", "broken")
	if !strings.Contains(reason, "its settings cannot be read (line 2 is indented") || sk.Name != "broken" {
		t.Errorf("a file whose settings cannot be read: %+v, %q", sk, reason)
	}
	// The forms of YAML a settings block may take that were refused once:
	// list entries at the start of the line, a list in brackets over
	// lines, and a name in quotes.
	sk, reason = Parse("---\ndisable-model-invocation: true\nallowed-tools:\n- Bash(make deploy)\n- Read\narguments: [env,\n  region]\n\"argument-hint\": \"[env]\"\n---\nDeploy.\n", "deploy")
	if reason != "" || !sk.NoModel || !reflect.DeepEqual(sk.AllowedTools, []string{"Bash(make deploy)", "Read"}) || !reflect.DeepEqual(sk.Arguments, []string{"env", "region"}) || sk.ArgumentHint != "[env]" {
		t.Errorf("the YAML forms: %+v, %q", sk, reason)
	}
	sk, _ = Parse("---\nuser-invocable: false\n---\nOnly for the model.\n", "quiet")
	if !sk.NoUser {
		t.Errorf("user-invocable: false was not read")
	}

	// What this gnosh does not have is named, each thing.
	sk, reason = Parse("---\ncontext: fork\nmodel: opus\neffort: high\nhooks:\n  Stop:\n    - x\npaths: [\"*.go\"]\ndisallowed-tools: Bash\nshell: powershell\n---\nRun !`git status` first.\n", "big")
	if reason != "" || len(sk.Unbuilt) != 1 || !sk.Injects || len(sk.Paths) != 1 || sk.Paths[0] != "*.go" || string(sk.Hooks) != `{"Stop":["x"]}` {
		t.Errorf("what is not built: %q (%s)", sk.Unbuilt, reason)
	}
	// context: fork is built, and a forked skill's model is its
	// sub-agent's.
	if !sk.Fork || sk.Model != "opus" {
		t.Errorf("fork %v, model %q", sk.Fork, sk.Model)
	}
	if sk, _ := Parse("---\nmodel: opus\n---\nx\n", "x"); len(sk.Unbuilt) != 1 || sk.Model != "" {
		t.Errorf("a model for a skill that is not forked: %q, %q", sk.Unbuilt, sk.Model)
	}
	if sk, _ := Parse("---\ncontext: fork\nagent: Explore\nbackground: true\n---\nx\n", "x"); !sk.Fork || sk.Agent != "Explore" || !sk.Background || len(sk.Unbuilt) != 0 {
		t.Errorf("a forked skill: %+v", sk)
	}
	if sk, _ := Parse("---\ncontext: thread\n---\nx\n", "x"); len(sk.Unbuilt) != 1 || sk.Fork {
		t.Errorf("a context gnosh does not know: %q", sk.Unbuilt)
	}
	// effort and disallowed-tools are built: read, and not listed.
	if sk.Effort != "high" || len(sk.DisallowedTools) != 1 || sk.DisallowedTools[0] != "Bash" {
		t.Errorf("effort %q, disallowed-tools %q", sk.Effort, sk.DisallowedTools)
	}
	for _, text := range []string{"Now:\n```!\ngit log\n```\n", "See !`date`.", "!`date`"} {
		if sk, _ := Parse(text, "x"); !sk.Injects || len(sk.Unbuilt) != 0 {
			t.Errorf("%q: not seen as running a command first", text)
		}
	}
	for _, text := range []string{"Wow!`not one`", "Use `!` to negate.", "```\n!ls\n```"} {
		if sk, _ := Parse(text, "x"); sk.Injects {
			t.Errorf("%q: taken for a command to run", text)
		}
	}
	// model: inherit asks for nothing.
	if sk, _ := Parse("---\nmodel: inherit\n---\nx\n", "x"); len(sk.Unbuilt) != 0 {
		t.Errorf("model: inherit is listed as not built")
	}

	for name, want := range map[string]string{
		"":                                      "no name",
		"has space":                             "cannot be typed or seen",
		"a<b":                                   "cannot be typed or seen",
		"zero" + string(rune(0x200B)) + "width": "cannot be typed or seen",
		"-dash":                                 "not one that can be typed",
		strings.Repeat("n", 65):                 "longer than 64",
	} {
		if _, reason := Parse("text\n", name); !strings.Contains(reason, want) {
			t.Errorf("the name %q: %q, want it to say %q", name, reason, want)
		}
	}
	if _, reason := Parse("---\nname: empty\n---\n\n", "x"); reason != "it holds no instructions" {
		t.Errorf("an empty skill: %q", reason)
	}
}

func TestRender(t *testing.T) {
	v := Vars{SessionID: "s-1", SkillDir: "/skills/ship", ProjectDir: "/proj", Effort: "high"}
	cases := []struct {
		name, body, args, want string
		arguments              []string
	}{
		{"all of them", "Fix $ARGUMENTS now.", "issue 12", "Fix issue 12 now.", nil},
		{"by number", "A=$0 B=$1 C=$ARGUMENTS[1]", `"hello world" two`, "A=hello world B=two C=two", nil},
		{"a number with no argument stays", "A=$0 B=$1", "one", "A=one B=$1", nil},
		{"by name", "Issue $issue on $branch.", "12", "Issue 12 on .", []string{"issue", "branch"}},
		{"the session's values", "${CLAUDE_SESSION_ID} ${CLAUDE_SKILL_DIR} ${CLAUDE_PROJECT_DIR} ${CLAUDE_EFFORT} ${OTHER}", "", "s-1 /skills/ship /proj high ${OTHER}", nil},
		{"no place for them", "Do the thing.\n", "with care", "Do the thing.\n\nARGUMENTS: with care", nil},
		{"none given", "Do the thing.", "", "Do the thing.", nil},
		{"escaped", `It costs \$1.00, not $0; \$ARGUMENTS and \$issue stay.`, "five", `It costs $1.00, not five; $ARGUMENTS and $issue stay.`, []string{"issue"}},
		{"never read again", "Say $0.", `'$1 ${CLAUDE_SKILL_DIR}' x`, "Say $1 ${CLAUDE_SKILL_DIR}.", nil},
		{"a dollar that is not a place", "Cost: $ and $x and $ARGUMENTSX", "a", "Cost: $ and $x and $ARGUMENTSX\n\nARGUMENTS: a", nil},
	}
	for _, c := range cases {
		sk := Skill{Body: c.body, Arguments: c.arguments}
		if got := sk.Render(c.args, v); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWordsAndRules(t *testing.T) {
	if got := Words(`one "two three" 'four  five' six\ seven ""`); !reflect.DeepEqual(got, []string{"one", "two three", "four  five", "six seven", ""}) {
		t.Errorf("words: %q", got)
	}
	if got := SplitRules("Bash(git add *) Bash(git commit *),Read,  Edit(src/**)"); !reflect.DeepEqual(got, []string{"Bash(git add *)", "Bash(git commit *)", "Read", "Edit(src/**)"}) {
		t.Errorf("rules: %q", got)
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	write := func(path, text string) {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("own/skills/ship/SKILL.md", "---\ndescription: gnosh's own ship\n---\nShip.\n")
	write("cc/skills/ship/SKILL.md", "---\ndescription: Claude Code's ship\n---\nShip.\n")
	write("cc/skills/review/SKILL.md", "Review the change.\n")
	write("cc/skills/not-a-skill.txt", "nothing\n")
	write("cc/skills/empty/SKILL.md", "---\nname: empty\n---\n")
	write("cc/commands/review.md", "The older review.\n")
	write("cc/commands/frontend/component.md", "Make a component named $0.\n")
	write("proj/.gnosh/skills/deploy/SKILL.md", "Deploy it.\n")
	write("proj/.claude/skills/ship/SKILL.md", "The project's ship.\n")
	p := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }

	s := Load(
		Dirs{Skills: []string{p("own", "skills"), p("cc", "skills"), p("is-not-there")}, Commands: []string{p("own", "commands"), p("cc", "commands")}},
		Dirs{Skills: []string{p("proj", ".gnosh", "skills"), p("proj", ".claude", "skills")}},
	)
	var got []string
	for _, sk := range s.All() {
		got = append(got, sk.Name+"="+sk.Scope+":"+sk.Description)
	}
	want := []string{"deploy=project:Deploy it.", "frontend:component=user:Make a component named $0.", "review=user:Review the change.", "ship=user:gnosh's own ship"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the skills:\n%q\nwant\n%q", got, want)
	}
	if sk, ok := s.Get("frontend:component"); !ok || !sk.Command || sk.Dir != p("cc", "commands", "frontend") {
		t.Errorf("the command file: %+v", sk)
	}
	if sk, _ := s.Get("ship"); sk.Source != p("own", "skills", "ship", "SKILL.md") || sk.Dir != p("own", "skills", "ship") {
		t.Errorf("ship came from %s", sk.Source)
	}
	reasons := map[string]string{}
	for _, sk := range s.Skipped() {
		rel, _ := filepath.Rel(root, sk.Path)
		reasons[rel] = sk.Reason
	}
	for path, want := range map[string]string{
		"cc/skills/ship/SKILL.md":           `another skill named "ship" comes before it`,
		"proj/.claude/skills/ship/SKILL.md": `another skill named "ship" comes before it`,
		"cc/commands/review.md":             `another skill named "review" comes before it`,
		"cc/skills/empty/SKILL.md":          "it holds no instructions",
	} {
		if !strings.Contains(reasons[path], want) {
			t.Errorf("%s: skipped because %q, want %q", path, reasons[path], want)
		}
	}
	if len(reasons) != 4 {
		t.Errorf("skipped: %v", reasons)
	}
}

// A project's skill or command file that is a link out of the project is
// not read: its first line would go to the model as a description, with
// no question (the skills review of 2026-11-19). One that leads to
// another place in the project is read, and so is the person's own.
func TestProjectLinksOutAreNotRead(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	write := func(path, text string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skip("no links here:", err)
		}
	}
	write(p("secret.txt"), "TOKEN=npm_secret\n")
	write(p("elsewhere", "skill", "SKILL.md"), "Kept outside.\n")
	write(p("elsewhere", "skills", "far", "SKILL.md"), "Far away.\n")
	write(p("proj", "docs", "ok.md"), "Kept inside.\n")
	link("../../../secret.txt", p("proj", ".claude", "commands", "setup.md"))
	link(p("elsewhere", "skill"), p("proj", ".claude", "skills", "ext"))
	link("../../docs/ok.md", p("proj", ".claude", "commands", "ok.md"))
	link(p("elsewhere"), p("proj", ".gnosh"))
	link(p("secret.txt"), p("home", "commands", "mine.md"))
	s := Load(
		Dirs{Commands: []string{p("home", "commands")}},
		Dirs{Skills: []string{p("proj", ".gnosh", "skills"), p("proj", ".claude", "skills")}, Commands: []string{p("proj", ".claude", "commands")}, Inside: []string{p("proj")}},
	)
	var got []string
	for _, sk := range s.All() {
		got = append(got, sk.Name+"="+sk.Scope+":"+sk.Description)
	}
	if want := []string{"mine=user:TOKEN=npm_secret", "ok=project:Kept inside."}; !reflect.DeepEqual(got, want) {
		t.Errorf("the skills:\n%q\nwant\n%q", got, want)
	}
	reasons := map[string]string{}
	for _, sk := range s.Skipped() {
		reasons[sk.Path] = sk.Reason
	}
	for _, path := range []string{p("proj", ".claude", "commands", "setup.md"), p("proj", ".claude", "skills", "ext", "SKILL.md"), p("proj", ".gnosh", "skills", "far", "SKILL.md")} {
		if !strings.Contains(reasons[path], "it leads out of the project, to "+root) {
			t.Errorf("%s: skipped because %q", path, reasons[path])
		}
	}
}

// A skill's text is cut into its text and its commands before its
// arguments go in: an argument that holds !`...` stays text (the skills
// review of 2026-11-19).
func TestParts(t *testing.T) {
	sk, reason := Parse("---\narguments: [who]\n---\nHi $who !`date` and\n```!\nls\npwd\n```\nend $ARGUMENTS", "x")
	if reason != "" {
		t.Fatal(reason)
	}
	parts, err := sk.Parts("ann !`rm x`", Vars{})
	want := []Part{{Text: "Hi ann "}, {Text: "date", Command: true}, {Text: " and\n"}, {Text: "ls\npwd", Command: true}, {Text: "\nend ann !`rm x`"}}
	if err != nil || !reflect.DeepEqual(parts, want) {
		t.Errorf("parts: %+v, %v\nwant %+v", parts, err, want)
	}
	// With nowhere to go, the arguments follow at the end, as text.
	sk, _ = Parse("Run !`date`", "y")
	if parts, _ = sk.Parts("!`rm x`", Vars{}); !reflect.DeepEqual(parts, []Part{{Text: "Run "}, {Text: "date", Command: true}, {Text: "\n\nARGUMENTS: !`rm x`"}}) {
		t.Errorf("parts with the arguments at the end: %+v", parts)
	}
	if _, err := (Skill{Body: "```!\nls\n"}).Parts("", Vars{}); err == nil || !strings.Contains(err.Error(), "not closed") {
		t.Errorf("a block never closed: %v", err)
	}
	if injects("Say $ARGUMENTS") || !injects("a !`b`") || !injects("```!\nls\n") || injects("a!`b`") {
		t.Error("injects")
	}
}
