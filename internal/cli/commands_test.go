package cli

import "testing"

// helpFor is the Invocation that asks for a command's help.
func helpFor(words ...string) Invocation {
	return Invocation{Command: CmdHelp, HelpFor: words}
}

// TestCommands covers every subcommand: its flags, its arguments, and what
// it refuses.
func TestCommands(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		// doctor
		{line: `gnosh doctor`, want: Invocation{Command: CmdDoctor}},
		{line: `gnosh doctor --cwd /work/app`, want: Invocation{Command: CmdDoctor, Cwd: "/work/app"}},
		{line: `gnosh --cwd /work/app doctor`, want: Invocation{Command: CmdDoctor, Cwd: "/work/app"}},
		{line: `gnosh doctor --cwd=/work/app`, want: Invocation{Command: CmdDoctor, Cwd: "/work/app"}},
		{line: `gnosh doctor --hosts`, want: Invocation{Command: CmdDoctor, Hosts: true}},
		{line: `gnosh doctor --hosts --cwd /work/app`, want: Invocation{Command: CmdDoctor, Hosts: true, Cwd: "/work/app"}},
		{line: `gnosh --hosts`, kind: ErrUnknownFlag, say: []string{"--hosts"}},
		{line: `gnosh doctor now`, kind: ErrUsage, say: []string{"now was typed after gnosh doctor, which takes nothing after it but flags"}},
		{line: `gnosh doctor -- now`, kind: ErrUsage, say: []string{"now was typed after gnosh doctor"}},
		{line: `gnosh doctor -y`, kind: ErrUnknownFlag, say: []string{"-y does not go with gnosh doctor", "It is a flag of gnosh and gnosh agents."}},
		{line: `gnosh -y doctor`, kind: ErrUnknownFlag, say: []string{"-y (--yolo) was typed before gnosh doctor and does not go with it", "It is a flag of gnosh and gnosh agents; flags of gnosh doctor go after its name."}},
		{line: `gnosh doctor --model x`, kind: ErrUnknownFlag, say: []string{"--model does not go with gnosh doctor"}},
		{line: `gnosh doctor -p`, kind: ErrUnknownFlag, say: []string{"-p does not go with gnosh doctor"}},
		{line: `gnosh doctor --frobnicate`, kind: ErrUnknownFlag, say: []string{"--frobnicate is not a flag of gnosh doctor", `"gnosh doctor --help" lists the flags.`}},

		// trust
		{line: `gnosh trust`, want: Invocation{Command: CmdTrust}},
		{line: `gnosh trust ../other`, want: Invocation{Command: CmdTrust, Args: []string{"../other"}}},
		{line: `gnosh trust "my folder"`, want: Invocation{Command: CmdTrust, Args: []string{"my folder"}}},
		{line: `gnosh trust --list`, want: Invocation{Command: CmdTrust, List: true}},
		{line: `gnosh trust --forget`, want: Invocation{Command: CmdTrust, Forget: true}},
		{line: `gnosh trust --forget ../other`, want: Invocation{Command: CmdTrust, Forget: true, Args: []string{"../other"}}},
		{line: `gnosh trust ../other --forget`, want: Invocation{Command: CmdTrust, Forget: true, Args: []string{"../other"}}},
		{line: `gnosh trust -- -odd-name`, want: Invocation{Command: CmdTrust, Args: []string{"-odd-name"}}},
		{line: `gnosh trust --cwd /work ./app`, want: Invocation{Command: CmdTrust, Cwd: "/work", Args: []string{"./app"}}},
		{line: `gnosh trust a b`, kind: ErrUsage, say: []string{"b is one argument too many for gnosh trust", "It is written: gnosh trust [folder]"}},
		{line: `gnosh trust me on this`, kind: ErrUsage, say: []string{"on is one argument too many for gnosh trust", "needs quotes"}},
		{line: `gnosh trust --list --forget`, kind: ErrConflict, say: []string{"--list and --forget ask for two different things"}},
		{line: `gnosh -c -p x --no-session-persistence`, kind: ErrConflict, say: []string{"--no-session-persistence keeps nothing of the run, and -c is about a session that is kept"}},
		{line: `gnosh -p x --session-id 0f8fad5b-d9cb-469f-a165-70867728950e --no-session-persistence`, kind: ErrConflict, say: []string{"--session-id is about a session that is kept"}},
		{line: `gnosh --no-session-persistence`, kind: ErrUsage, say: []string{"--no-session-persistence only does something in print mode"}},
		{line: `gnosh trust --list ../x`, kind: ErrUsage, say: []string{"--list shows every trusted folder and takes no folder, and ../x was typed with it"}},
		{line: `gnosh trust ""`, kind: ErrBadValue, say: []string{"An empty argument was typed after gnosh trust"}},
		{line: `gnosh trust --lst`, kind: ErrUnknownFlag, hint: "--list"},
		{line: `gnosh trust --trust-folder`, kind: ErrUnknownFlag, say: []string{"--trust-folder does not go with gnosh trust", "It is a flag of gnosh."}},

		// models
		{line: `gnosh models`, want: Invocation{Command: CmdModels}},
		{line: `gnosh models --cwd x`, want: Invocation{Command: CmdModels, Cwd: "x"}},
		{line: `gnosh models --json`, kind: ErrUnknownFlag, say: []string{"--json does not go with gnosh models", "It is a flag of gnosh session list, gnosh session show, gnosh session last and others."}},
		{line: `gnosh models qwen`, kind: ErrUsage, say: []string{"qwen was typed after gnosh models"}},

		// session
		{line: `gnosh session list`, want: Invocation{Command: CmdSessionList}},
		{line: `gnosh sessions list`, want: Invocation{Command: CmdSessionList}},
		{line: `gnosh s ls`, want: Invocation{Command: CmdSessionList}},
		{line: `gnosh session ls --json`, want: Invocation{Command: CmdSessionList, JSON: true}},
		{line: `gnosh session --json list`, want: Invocation{Command: CmdSessionList, JSON: true}},
		{line: `gnosh session --cwd /w list`, want: Invocation{Command: CmdSessionList, Cwd: "/w"}},
		{line: `gnosh --cwd /w session list --json`, want: Invocation{Command: CmdSessionList, Cwd: "/w", JSON: true}},
		{line: `gnosh session show abc`, want: Invocation{Command: CmdSessionShow, Args: []string{"abc"}}},
		{line: `gnosh session show --json abc`, want: Invocation{Command: CmdSessionShow, Args: []string{"abc"}, JSON: true}},
		{line: `gnosh session last`, want: Invocation{Command: CmdSessionLast}},
		{line: `gnosh session last --json`, want: Invocation{Command: CmdSessionLast, JSON: true}},
		{line: `gnosh session rename abc New title here`, want: Invocation{Command: CmdSessionRename, Args: []string{"abc", "New title here"}}},
		{line: `gnosh session rename abc "New title" --json`, want: Invocation{Command: CmdSessionRename, Args: []string{"abc", "New title"}, JSON: true}},
		{line: `gnosh session rename abc -- -dash title`, want: Invocation{Command: CmdSessionRename, Args: []string{"abc", "-dash title"}}},
		{line: `gnosh session`, kind: ErrUsage, say: []string{"gnosh session needs one of its commands after it, so nothing was done: list, show, last or rename", `"gnosh session --help" explains them`}},
		{line: `gnosh s --json`, kind: ErrUsage, say: []string{"gnosh session needs one of its commands", "If s is a prompt, put -- before it."}},
		{line: `gnosh session lst`, kind: ErrUnknownCommand, hint: "list", say: []string{"lst is not a command of gnosh session, so nothing was done. Did you mean list? Its commands are list, show, last and rename."}},
		{line: `gnosh session List`, kind: ErrUnknownCommand, hint: "list"},
		{line: `gnosh session renam abc x`, kind: ErrUnknownCommand, hint: "rename"},
		{line: `gnosh session ren abc x`, kind: ErrUnknownCommand, hint: "rename"},
		{line: `gnosh session xyzzy`, kind: ErrUnknownCommand, say: []string{"xyzzy is not a command of gnosh session", "Its commands are"}},
		{line: `gnosh session show`, kind: ErrUsage, say: []string{"gnosh session show needs more after it", "It is written: gnosh session show <id>"}},
		{line: `gnosh session show a b`, kind: ErrUsage, say: []string{"b is one argument too many for gnosh session show"}},
		{line: `gnosh session show ""`, kind: ErrBadValue, say: []string{"An empty argument was typed after gnosh session show"}},
		{line: `gnosh session list extra`, kind: ErrUsage, say: []string{"extra was typed after gnosh session list"}},
		{line: `gnosh session rename abc`, kind: ErrUsage, say: []string{"gnosh session rename needs more after it", "gnosh session rename <id> <title...>"}},
		{line: `gnosh session rename abc ""`, kind: ErrBadValue, say: []string{"An empty argument"}},
		{line: `gnosh session list --follow`, kind: ErrUnknownFlag, say: []string{"--follow does not go with gnosh session list", "It is a flag of gnosh logs."}},
		{line: `gnosh session --follow list`, kind: ErrUnknownFlag, say: []string{"--follow does not go with gnosh session", "It is a flag of gnosh logs."}},
		{line: `gnosh -r abc session list`, kind: ErrUnknownFlag, say: []string{"-r (--resume) was typed before gnosh session and does not go with it"}},
		{line: `gnosh session list -y`, kind: ErrUnknownFlag, say: []string{"-y does not go with gnosh session list"}},

		// mem: the words after it are mem's to read.
		{line: `gnosh mem`, want: Invocation{Command: CmdMem}},
		{line: `gnosh mem show`, want: Invocation{Command: CmdMem, Args: []string{"show"}}},
		{line: `gnosh mem show plan notes`, want: Invocation{Command: CmdMem, Args: []string{"show", "plan", "notes"}}},
		{line: `gnosh mem find flaky test`, want: Invocation{Command: CmdMem, Args: []string{"find", "flaky", "test"}}},
		{line: `gnosh mem log 20`, want: Invocation{Command: CmdMem, Args: []string{"log", "20"}}},
		{line: `gnosh mem undo`, want: Invocation{Command: CmdMem, Args: []string{"undo"}}},
		{line: `gnosh mem find -- -race`, want: Invocation{Command: CmdMem, Args: []string{"find", "-race"}}},
		{line: `gnosh mem --cwd /w find x`, want: Invocation{Command: CmdMem, Cwd: "/w", Args: []string{"find", "x"}}},
		{line: `gnosh mem find ""`, want: Invocation{Command: CmdMem, Args: []string{"find", ""}}},
		{line: `gnosh mem find -race`, kind: ErrUnknownFlag, say: []string{"-race: -r is not a flag of gnosh mem", "a word for mem that begins with a dash goes after --"}},
		{line: `gnosh mem show --json`, kind: ErrUnknownFlag, say: []string{"--json is not a flag of gnosh mem", "goes after --"}},

		// logs
		{line: `gnosh logs`, want: Invocation{Command: CmdLogs}},
		{line: `gnosh logs -f`, want: Invocation{Command: CmdLogs, Follow: true}},
		{line: `gnosh logs --follow`, want: Invocation{Command: CmdLogs, Follow: true}},
		{line: `gnosh logs -t 50`, want: Invocation{Command: CmdLogs, Tail: 50}},
		{line: `gnosh logs --tail 50`, want: Invocation{Command: CmdLogs, Tail: 50}},
		{line: `gnosh logs --tail=50`, want: Invocation{Command: CmdLogs, Tail: 50}},
		{line: `gnosh logs -ft 50`, want: Invocation{Command: CmdLogs, Follow: true, Tail: 50}},
		{line: `gnosh logs -f -t 20 --cwd /w`, want: Invocation{Command: CmdLogs, Follow: true, Tail: 20, Cwd: "/w"}},
		{line: `gnosh logs -tf 50`, kind: ErrMissingValue, say: []string{"-tf: -t takes a value (<n>), so it has to be the last letter"}},
		{line: `gnosh logs -t 0`, kind: ErrBadValue, say: []string{"-t takes a whole number of 1 or more, and 0 is not one"}},
		{line: `gnosh logs -t many`, kind: ErrBadValue, say: []string{"many is not one"}},
		{line: `gnosh logs -t`, kind: ErrMissingValue, say: []string{"-t needs a value after it (<n>)"}},
		{line: `gnosh logs 50 60`, kind: ErrUsage, say: []string{"60 is one argument too many for gnosh logs", "gnosh logs [session]"}},

		// dirs
		{line: `gnosh dirs`, want: Invocation{Command: CmdDirs}},
		{line: `gnosh dirs --cwd /w`, want: Invocation{Command: CmdDirs, Cwd: "/w"}},
		{line: `gnosh dirs --json`, kind: ErrUnknownFlag, say: []string{"--json does not go with gnosh dirs"}},

		// mcp
		{line: `gnosh mcp add greeter -- npx -y greeter --port 8080`, want: Invocation{Command: CmdMCPAdd, Args: []string{"greeter", "npx", "-y", "greeter", "--port", "8080"}}},
		{line: `gnosh mcp add -e A=1 B=2 -s user greeter /bin/greeter`, want: Invocation{Command: CmdMCPAdd, Args: []string{"greeter", "/bin/greeter"}, MCPEnv: []string{"A=1", "B=2"}, MCPScope: "user"}},
		{line: `gnosh mcp add -e A=1 greeter /bin/greeter -e B=x=y`, want: Invocation{Command: CmdMCPAdd, Args: []string{"greeter", "/bin/greeter"}, MCPEnv: []string{"A=1", "B=x=y"}}},
		{line: `gnosh mcp add -t http -H "Authorization: Bearer x" "X-A: b" far https://mcp.example.org/x`, want: Invocation{Command: CmdMCPAdd, Args: []string{"far", "https://mcp.example.org/x"}, MCPTransport: "http", MCPHeaders: []string{"Authorization: Bearer x", "X-A: b"}}},
		{line: `gnosh mcp add -t http -H "A: b" https://mcp.example.org/x`, kind: ErrUsage, say: []string{"gnosh mcp add needs more after it"}},
		{line: `gnosh mcp add -t sse far https://x`, kind: ErrBadValue, say: []string{"the sse transport is not one gnosh speaks"}},
		{line: `gnosh mcp add -e 1A=b x y`, kind: ErrBadValue, say: []string{"1A=b is not KEY=value"}},
		{line: `gnosh mcp add greeter npx -y x`, kind: ErrUnknownFlag, say: []string{"goes after --"}},
		{line: `gnosh mcp add greeter`, kind: ErrUsage, say: []string{"gnosh mcp add needs more after it"}},
		{line: `gnosh mcp add-json far '{"type":"http","url":"https://x"}'`, want: Invocation{Command: CmdMCPAddJSON, Args: []string{"far", `{"type":"http","url":"https://x"}`}}},
		{line: `gnosh mcp add-json far -t http x`, kind: ErrUnknownFlag, say: []string{"-t does not go with gnosh mcp add-json"}},
		{line: `gnosh mcp add-json far -`, want: Invocation{Command: CmdMCPAddJSON, Args: []string{"far", "-"}}},
		{line: `gnosh mcp get -`, kind: ErrUsage, say: []string{"- by itself means nothing"}},
		{line: `gnosh mcp list`, want: Invocation{Command: CmdMCPList}},
		{line: `gnosh mcp ls --cwd /w`, want: Invocation{Command: CmdMCPList, Cwd: "/w"}},
		{line: `gnosh mcp get far`, want: Invocation{Command: CmdMCPGet, Args: []string{"far"}}},
		{line: `gnosh mcp rm far -s project`, want: Invocation{Command: CmdMCPRemove, Args: []string{"far"}, MCPScope: "project"}},
		{line: `gnosh mcp remove far -s everywhere`, kind: ErrBadValue},
		{line: `gnosh mcp`, kind: ErrUsage, say: []string{"gnosh mcp needs one of its commands after it"}},
		{line: `gnosh help mcp`, want: helpFor("mcp")},
		{line: `gnosh -p --mcp-config '{"mcpServers": {}}' ./mcp.json --strict-mcp-config "do it"`, want: pr(Invocation{Prompt: "do it", MCPConfigs: []string{`{"mcpServers": {}}`, "./mcp.json"}, StrictMCPConfig: true})},
		{line: `gnosh -p --mcp-config ./mcp.json '{"mcpServers": {}}'`, want: pr(Invocation{Prompt: `{"mcpServers": {}}`, MCPConfigs: []string{"./mcp.json"}})},
		{line: `gnosh --mcp-config a.json b.json`, want: inter(Invocation{MCPConfigs: []string{"a.json", "b.json"}})},

		// help and version, as flags and as commands
		{line: `gnosh -h`, want: helpFor()},
		{line: `gnosh --help`, want: helpFor()},
		{line: `gnosh help`, want: helpFor()},
		{line: `gnosh --help`, env: &script, want: helpFor()},
		{line: `gnosh doctor --help`, want: helpFor("doctor")},
		{line: `gnosh doctor -h`, want: helpFor("doctor")},
		{line: `gnosh --help doctor`, want: helpFor("doctor")},
		{line: `gnosh help doctor`, want: helpFor("doctor")},
		{line: `gnosh session --help`, want: helpFor("session")},
		{line: `gnosh session list -h`, want: helpFor("session", "list")},
		{line: `gnosh s ls -h`, want: helpFor("session", "list")},
		{line: `gnosh --help session list`, want: helpFor("session", "list")},
		{line: `gnosh help session list`, want: helpFor("session", "list")},
		{line: `gnosh help s ls`, want: helpFor("session", "list")},
		{line: `gnosh help help`, want: helpFor("help")},
		{line: `gnosh doctor extra --help`, want: helpFor("doctor")},
		{line: `gnosh mem show --help`, want: helpFor("mem")},
		{line: `gnosh trust --help --list --forget`, want: helpFor("trust")},
		{line: `gnosh -p --help`, want: helpFor()},
		{line: `gnosh "fix it" --help`, want: helpFor()},
		{line: `gnosh --help -p x`, want: helpFor()},
		{line: `gnosh --help --bogus`, want: helpFor()},
		{line: `gnosh --help --model`, want: helpFor()},
		{line: `gnosh -h -- doctor`, want: helpFor()},
		{line: `gnosh --fork-session --help`, want: helpFor()},
		{line: `gnosh --bogus --help`, kind: ErrUnknownFlag, say: []string{"--bogus is not a flag of gnosh"}},
		{line: `gnosh help sesion`, kind: ErrUnknownCommand, hint: "session", say: []string{"sesion is not a command of gnosh", "Did you mean session?"}},
		{line: `gnosh help session lst`, kind: ErrUnknownCommand, hint: "list"},
		{line: `gnosh help --json`, kind: ErrUnknownFlag, say: []string{"--json does not go with gnosh help"}},
		{line: `gnosh --help=yes`, kind: ErrBadValue, say: []string{"takes no value"}},
		{line: `gnosh version`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh version --help`, want: helpFor("version")},
		{line: `gnosh version now`, kind: ErrUsage, say: []string{"now was typed after gnosh version"}},
	})
}

// TestWhatIsNotInTheTable covers the commands and flags that
// spec/DECISIONS.md item 26 took off the command line, and the ones of the
// specs whose parts are not built: none of them is read as what it once
// was. A flag is unknown, a command of a group is unknown, and a first
// word that names no command is the prompt, as any other word is.
func TestWhatIsNotInTheTable(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		// Print mode is -p. "run" is a word, and its -q is no flag.
		{line: `gnosh run`, want: inter(Invocation{Prompt: "run"})},
		{line: `gnosh run the tests`, want: inter(Invocation{Prompt: "run the tests"})},
		{line: `gnosh run "fix the build"`, env: &captured, want: pr(Invocation{Prompt: "run fix the build", PrintImplied: true})},
		{line: `gnosh r x`, want: inter(Invocation{Prompt: "r x"})},
		{line: `gnosh -p run the tests`, want: pr(Invocation{Prompt: "run the tests"})},
		{line: `gnosh help run`, kind: ErrUnknownCommand, say: []string{"run is not a command of gnosh"}},
		{line: `gnosh run -q -y --cwd DIR "do the task"`, kind: ErrUnknownFlag, say: []string{"-q is not a flag of gnosh, so nothing was started."}},
		{line: `gnosh -p -q x`, kind: ErrUnknownFlag, say: []string{"-q is not a flag of gnosh"}},
		{line: `gnosh -p --quiet x`, kind: ErrUnknownFlag, say: []string{"--quiet is not a flag of gnosh"}},

		// A session is named with --resume, and a new one with --session-id.
		{line: `gnosh -s abc`, kind: ErrUnknownFlag, say: []string{"-s does not go with gnosh", "It is a flag of gnosh mcp add, gnosh mcp add-json and gnosh mcp remove."}},
		{line: `gnosh --session abc`, kind: ErrUnknownFlag, hint: "--session-id", say: []string{"--session is not a flag of gnosh"}},
		{line: `gnosh -ys abc`, kind: ErrUnknownFlag, say: []string{"-ys: -s does not go with gnosh"}},

		// There is no flag for another data folder.
		{line: `gnosh -D /tmp/data`, kind: ErrUnknownFlag, hint: "-d", say: []string{"-D is not a flag of gnosh", "Did you mean -d (--debug)? Letter case counts."}},
		{line: `gnosh --data-dir /tmp/data`, kind: ErrUnknownFlag, say: []string{"--data-dir is not a flag of gnosh"}},
		{line: `gnosh doctor -D /tmp/data`, kind: ErrUnknownFlag, say: []string{"-D is not a flag of gnosh doctor"}},

		// The flags of parts that are not built.
		{line: `gnosh -p x --permission-prompts some`, kind: ErrBadValue, say: []string{"--permission-prompts takes host or none"}},
		{line: `gnosh -p x --include-partial-messages`, kind: ErrUsage, say: []string{"--include-partial-messages only does something with --output-format stream-json"}},
		{line: `gnosh -p --input-format stream-json`, kind: ErrUsage, say: []string{"--input-format stream-json is a conversation in JSON lines both ways, and needs --output-format stream-json"}},
		{line: `gnosh -p x --input-format stream-json --output-format stream-json`, kind: ErrUsage, say: []string{"the prompts come on standard input"}},
		{line: `gnosh -p x --replay-user-messages`, kind: ErrUsage, say: []string{"--replay-user-messages only does something with --input-format stream-json and --output-format stream-json"}},

		// The commands of parts that are not built: in a group, unknown; as
		// a first word, the prompt.
		{line: `gnosh session delete abc`, kind: ErrUnknownCommand, say: []string{"delete is not a command of gnosh session", "Its commands are list, show, last and rename."}},
		{line: `gnosh session rm abc`, kind: ErrUnknownCommand, say: []string{"rm is not a command of gnosh session"}},
		{line: `gnosh session web`, kind: ErrUnknownCommand, say: []string{"web is not a command of gnosh session"}},
		{line: `gnosh prompt tools`, kind: ErrUnknownCommand, say: []string{"tools is not a command of gnosh prompt", "Its commands are show, path and export. If \"prompt tools\" is a prompt, put -- before it."}},
		{line: `gnosh prompt context`, kind: ErrUnknownCommand, say: []string{"context is not a command of gnosh prompt"}},
		{line: `gnosh "prompt"`, kind: ErrUsage, say: []string{"gnosh prompt needs one of its commands after it", "If prompt is a prompt, put -- before it."}},
		{line: `gnosh -- prompt show`, want: inter(Invocation{Prompt: "prompt show"})},
		// (gnosh ssh is built: main runs it before the table is read, and
		// the table has it for help.)
		{line: `gnosh ssh box.example.com`, want: Invocation{Command: CmdSSH, Args: []string{"box.example.com"}}},
		{line: `gnosh projects`, want: inter(Invocation{Prompt: "projects"})},
		{line: `gnosh stats`, want: inter(Invocation{Prompt: "stats"})},
		{line: `gnosh stats --all`, kind: ErrUnknownFlag, say: []string{"--all does not go with gnosh, so nothing was started", "It is a flag of gnosh agents."}},
		{line: `gnosh projects --json`, kind: ErrUnknownFlag, say: []string{"--json does not go with gnosh"}},
	})

	// None of them has an entry, so none can be marked built by mistake.
	tab := Default()
	have := map[string]bool{}
	for _, e := range tab.Entries() {
		have[e.Name] = true
	}
	for _, gone := range []string{
		"run", "--quiet", "--session", "--data-dir",
		"session delete", "session web", "session web --port",
		"prompt tools", "prompt context", "prompt tools --json", "prompt tools --export",
		"projects", "projects --json", "stats", "stats --all", "stats --crawl-dir",
	} {
		if have[gone] {
			t.Errorf("the table has an entry %q, which spec/DECISIONS.md item 26 took off it", gone)
		}
		if err := tab.SetStatus(gone, Built); err == nil {
			t.Errorf("SetStatus(%q) worked: the table still knows the name", gone)
		}
	}
}
