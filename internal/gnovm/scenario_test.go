// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestScenarios runs the use cases in testdata/scenarios against a real
// gnosh and a real model, with gnosh's own system prompt ("v0") and with
// each prompt variant in testdata/prompts appended to it, and writes what
// happened: each turn's tool calls and answer, each check, and what the
// persistent Gno machines hold at the end.
//
// It is skipped unless GNOVM_SCENARIO_GNOSH names the gnosh to run. Every
// run gets its own config and data directories (XDG_CONFIG_HOME and
// XDG_DATA_HOME) with a gnosh.json written for it, so it never touches the
// user's settings, sessions or mem. Each turn is one `gnosh -p -y
// --output-format stream-json --cwd <project>` process.
//
//	GNOVM_SCENARIO_GNOSH      the gnosh to run (required)
//	GNOVM_SCENARIO_OUT        where runs and reports go (default a temp dir)
//	GNOVM_SCENARIO_VARIANTS   comma-separated, e.g. "v0" (default v0 and
//	                          every testdata/prompts file)
//	GNOVM_SCENARIO_ONLY       a regexp on scenario names
//	GNOVM_SCENARIO_MODEL      provider/model, the model every turn uses for
//	                          -m and --small-model (default qwen/qwen3.8-27b)
//	GNOVM_SCENARIO_BASEURL    its llama.cpp server (default
//	                          http://127.0.0.1:8080)
//	GNOVM_SCENARIO_CONTEXT    its context window (default 262144)
//	GNOVM_SCENARIO_EMBEDDINGS mem's embedding server (default
//	                          http://127.0.0.1:8091/v1; empty or "off": none)
//	GNOVM_SCENARIO_CONFIG     a gnosh.json to copy as it is instead of the one
//	                          written from the settings above
//	GNOVM_SCENARIO_REASONING  "nothink" (the default: these runs measure tool
//	                          use, and Qwen's thinking is most of their time)
//	                          turns thinking off; any other level, or "",
//	                          turns it on. A copied config keeps its own.
//	GNOVM_SCENARIO_STRICT=1   fail the test when a check fails (by default
//	                          the checks are reported: the scenarios measure
//	                          the model and the prompt as much as gnosh)
//	GNOVM_SCENARIO_ARGS       flags added to every gnosh run, split on
//	                          spaces: --disallowedTools=gnovm_run,gnovm_new,
//	                          gnovm_list,gnovm_drop runs a scenario without
//	                          mem, the plain harness, where a conversation
//	                          that runs out of room is summarized
//
// Run it with -test.parallel below the model server's slots, so that live
// sessions keep one: a conversation whose slot another request takes
// re-reads all of itself on its next step.
func TestScenarios(t *testing.T) {
	t.Parallel()
	// GNOVM_SCENARIO_KLASH is its name from before the rename.
	gnosh := cmp.Or(os.Getenv("GNOVM_SCENARIO_GNOSH"), os.Getenv("GNOVM_SCENARIO_KLASH"))
	if gnosh == "" {
		t.Skip("set GNOVM_SCENARIO_GNOSH to a gnosh binary to run the real-model scenarios")
	}
	if abs, err := filepath.Abs(gnosh); err == nil {
		gnosh = abs
	}
	out := cmp.Or(os.Getenv("GNOVM_SCENARIO_OUT"), t.TempDir())
	variants := []string{"v0"}
	prompts, _ := filepath.Glob("testdata/prompts/*.md")
	for _, p := range prompts {
		variants = append(variants, strings.TrimSuffix(filepath.Base(p), ".md"))
	}
	if v := os.Getenv("GNOVM_SCENARIO_VARIANTS"); v != "" {
		variants = strings.Split(v, ",")
	}
	reasoning, setReasoning := os.LookupEnv("GNOVM_SCENARIO_REASONING")
	if !setReasoning {
		reasoning = "nothink"
	}
	only := regexp.MustCompile(cmp.Or(os.Getenv("GNOVM_SCENARIO_ONLY"), "."))
	files, err := filepath.Glob("testdata/scenarios/*.txtar")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	var mu sync.Mutex
	var results []*scenarioRun
	t.Cleanup(func() { writeSummary(t, filepath.Join(out, "summary-"+stamp+".md"), results) })
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txtar")
		if !only.MatchString(name) {
			continue
		}
		sc, err := parseScenario(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, variant := range variants {
			t.Run(name+"/"+variant, func(t *testing.T) {
				t.Parallel()
				r := &scenarioRun{scenario: name, variant: variant, dir: filepath.Join(out, name+"-"+variant+"-"+stamp)}
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
				r.run(t, sc, gnosh, reasoning)
				for _, c := range r.checks {
					if !c.ok && os.Getenv("GNOVM_SCENARIO_STRICT") == "1" {
						t.Errorf("check failed: %s (%s)", c.line, c.got)
					}
				}
			})
		}
	}
}

// scenario is a parsed testdata/scenarios file (txtar): a description, then
// sections. "prompt" continues the session, "prompt-new" starts a new one,
// "project/<path>" is a file in the project, "mem" is Gno run in the
// project's mem as the user before the first turn, and "check" holds the
// checks.
type scenario struct {
	about  string
	turns  []turnSpec
	files  map[string]string
	mem    string
	checks []string
	// peers is how many sessions each fresh turn starts at once in the
	// project, one gnosh process each (PARALLEL.md); GNOVM_SCENARIO_PEERS
	// overrides it for a scenario that has a peers section; 0 is one.
	peers int
}

type turnSpec struct {
	prompt string
	fresh  bool
}

// TestScenarioStreamReprints checks that a turn keeps only its own calls and
// text, when gnosh's stream prints an earlier turn's rows again.
func TestScenarioStreamReprints(t *testing.T) {
	t.Parallel()
	turn1 := `{"type":"assistant","message":{"content":[{"type":"text","text":"looking"},{"type":"tool_use","id":"a1","name":"view","input":{}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a1","content":"file"}]}}
{"type":"result","result":"done one"}
`
	turn2 := `{"type":"assistant","message":{"content":[{"type":"text","text":"looking"},{"type":"tool_use","id":"a1","name":"view","input":{}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a1","content":"file"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"editing"},{"type":"tool_use","id":"b2","name":"edit","input":{}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b2","content":"ok"}]}}
{"type":"result","result":"done two"}
`
	var r scenarioRun
	one := r.fresh(parseStream(strings.NewReader(turn1)))
	two := r.fresh(parseStream(strings.NewReader(turn2)))
	require.Len(t, one.calls, 1)
	require.Equal(t, []string{"looking"}, one.said)
	require.Len(t, two.calls, 1)
	require.Equal(t, "edit", two.calls[0].name)
	require.Equal(t, "ok", two.calls[0].result)
	require.Equal(t, []string{"editing"}, two.said)
	r.turns = []turnResult{one, two}
	said := r.check("said 2 contains editing", t.TempDir(), nil)
	require.True(t, said.ok, said.got)
	require.False(t, r.check("said 2 contains looking", t.TempDir(), nil).ok)
	require.False(t, r.check("answer 2 contains editing", t.TempDir(), nil).ok)
}

// TestScenarioFilesParse checks every scenario file without a model: it
// parses, it has a prompt and checks, and each check is of a known form.
func TestScenarioFilesParse(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("testdata/scenarios/*.txtar")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	forms := regexp.MustCompile(`^(turn [1-9][0-9]* )?(` +
		`tool \S+ (>=|<=|==|>|<) \d+|` +
		`(answer|said) (last|\d+) (contains|matches) .+|` +
		`(file|after \d+ file) \S+ (exists|absent|contains .+|lacks .+)|` +
		`cmd .+|` +
		`vm-persist (>=|<=|==|>|<) \d+|` +
		`vm-dump contains .+|` +
		`mem-count (open|done|dropped|items|facts|rules) (>=|<=|==|>|<) \d+|` +
		`vm-eval [a-z][a-z0-9_-]* \S+ ((>=|<=|==|>|<) -?\d+|contains .+))$`)
	for _, file := range files {
		sc, err := parseScenario(file)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if len(sc.turns) == 0 || len(sc.checks) == 0 {
			t.Errorf("%s: needs a prompt and checks", file)
		}
		for _, line := range sc.checks {
			if !forms.MatchString(line) {
				t.Errorf("%s: unknown check %q", file, line)
			}
			if m := regexp.MustCompile(`^(?:turn|answer|after) (\d+)`).FindStringSubmatch(line); m != nil {
				if n, _ := strconv.Atoi(m[1]); n > len(sc.turns) {
					t.Errorf("%s: %q names turn %d of %d", file, line, n, len(sc.turns))
				}
			}
		}
		if sc.mem != "" {
			if err := seedMem(t.TempDir(), "/proj", sc.mem, ""); err != nil {
				t.Errorf("%s: %v", file, err)
			}
		}
	}
}

// TestScenarioMemSection checks that a scenario's mem section is in mem
// before its first turn, kept as the user's, as the agent will find it.
func TestScenarioMemSection(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "seeded.txtar")
	require.NoError(t, os.WriteFile(file, []byte(`A secret in mem before the session.
-- mem --
mem.Fact("api/staging-key", "sk-staging-0000")
-- mem --
mem.Todo("rotate the staging key")
-- prompt --
What's the staging API key?
-- check --
mem-count facts == 1
`), 0o600))
	sc, err := parseScenario(file)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, seedMem(dir, "/proj", sc.mem, ""))
	n, err := memCount(dir, "/proj", "facts")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	n, err = memCount(dir, "/proj", "open")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	_, entries, _, err := readMemJournal(filepath.Join(memDir(dir, "/proj"), "journal.jsonl"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "one call, one entry")
	require.Equal(t, "user", entries[0].Who, "kept as the user's")
	require.Error(t, seedMem(dir, "/proj", `mem.Fact("BAD NAME", "x")`, ""), "a section that fails stops the run")

	// With an embedding server, the seeded rows have their vectors before
	// the first turn.
	f := newFakeEmbedder(t)
	dir = t.TempDir()
	require.NoError(t, seedMem(dir, "/proj", sc.mem, f.URL+"/v1"))
	s, ok, err := readMemSnapshot(context.Background(), memDir(dir, "/proj"))
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEmpty(t, s.vecs)
	require.False(t, s.lacksVectors())
}

// TestScenarioEmbeddingsURL checks that the harness finds the embedding
// server a run's gnosh is given in its gnosh.json, comments and all, and
// that a URL's "//" is not taken for a comment.
func TestScenarioEmbeddingsURL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "gnosh", "gnosh.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	body := "{\n  // the box's embedder\n  \"options\": {\"memEmbeddingsUrl\": \"http://127.0.0.1:8091/v1\" /* on the CPU */},\n  \"x\": \"a // b\"\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	require.Equal(t, "http://127.0.0.1:8091/v1", memEmbeddingsURL(dir))
	require.NoError(t, os.WriteFile(path, []byte(`{"model": {"id": "qwen/qwen3.8-27b"}}`), 0o600))
	require.Empty(t, memEmbeddingsURL(dir), "none set")
	require.Empty(t, memEmbeddingsURL(t.TempDir()), "no file")
}

var sectionRe = regexp.MustCompile(`(?m)^-- (.+?) --$`)

func parseScenario(path string) (*scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data := string(b)
	locs := sectionRe.FindAllStringSubmatchIndex(data, -1)
	sc := &scenario{files: map[string]string{}}
	if len(locs) == 0 {
		return nil, fmt.Errorf("no sections")
	}
	sc.about = strings.TrimSpace(data[:locs[0][0]])
	for i, loc := range locs {
		name := data[loc[2]:loc[3]]
		start := loc[1] + 1
		end := len(data)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := ""
		if start <= end {
			body = data[start:end]
		}
		switch {
		case name == "prompt" || name == "prompt-new":
			sc.turns = append(sc.turns, turnSpec{prompt: strings.TrimSpace(body), fresh: name == "prompt-new" || len(sc.turns) == 0})
		case name == "check":
			for _, line := range strings.Split(body, "\n") {
				if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
					sc.checks = append(sc.checks, line)
				}
			}
		case strings.HasPrefix(name, "project/"):
			sc.files[strings.TrimPrefix(name, "project/")] = body
		case name == "mem":
			sc.mem = strings.TrimSpace(sc.mem + "\n" + body)
		case name == "peers":
			n, err := strconv.Atoi(strings.TrimSpace(body))
			if err != nil || n < 1 {
				return nil, fmt.Errorf("peers: %q is not a count", strings.TrimSpace(body))
			}
			sc.peers = n
		default:
			return nil, fmt.Errorf("unknown section %q", name)
		}
	}
	return sc, nil
}

// scenarioRun is one scenario under one prompt variant.
type scenarioRun struct {
	scenario, variant, dir string
	turns                  []turnResult
	after                  []map[string]string // project files after each turn
	vms                    map[string]string   // persistent VM name -> dump
	checks                 []checkResult
	took                   time.Duration
	// seen is the tool-call ids and texts of the turns so far, so that a
	// turn keeps only its own (fresh).
	seen map[string]bool
	// peersWall is how long the peers' turn took, from the first start to
	// the last end; 0 for a run without peers.
	peersWall time.Duration
}

type turnResult struct {
	prompt  string
	session string
	calls   []toolCall
	answer  string
	// said is the text of each of the turn's messages, in order: all the
	// user read, where answer is only the last message.
	said    []string
	isError bool
	took    time.Duration
	// turn is the scenario's turn this is, or a peer of: a turn worked by
	// peers has one result per peer.
	turn int
}

type toolCall struct {
	id, name, input, result string
	isError                 bool
}

type checkResult struct {
	line, got string
	ok        bool
}

func (r *scenarioRun) run(t *testing.T, sc *scenario, gnosh, reasoning string) {
	start := time.Now()
	defer func() { r.took = time.Since(start) }()
	project := filepath.Join(r.dir, "project")
	cfg, data, bin := filepath.Join(r.dir, "config"), filepath.Join(r.dir, "data"), filepath.Join(r.dir, "bin")
	for _, d := range []string{filepath.Join(cfg, "gnosh"), filepath.Join(data, "gnosh"), project, filepath.Join(r.dir, "tmp"), bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeScenarioConfig(t, filepath.Join(cfg, "gnosh", "gnosh.json"), reasoning)
	// The checks' commands ("gnosh mem show") reach the gnosh under test, not
	// whichever gnosh PATH finds first.
	if err := os.Symlink(gnosh, filepath.Join(bin, "gnosh")); err != nil {
		t.Fatal(err)
	}
	for name, body := range sc.files {
		path := filepath.Join(project, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if sc.mem != "" {
		if err := seedMem(filepath.Join(data, "gnosh", "gnovm"), project, sc.mem, memEmbeddingsURL(cfg)); err != nil {
			t.Fatal(err)
		}
	}
	var appendPrompt string
	if r.variant != "v0" {
		p, err := filepath.Abs(filepath.Join("testdata", "prompts", r.variant+".md"))
		if err != nil {
			t.Fatal(err)
		}
		appendPrompt = p
	}
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "XDG_DATA_HOME="+data, "TMPDIR="+filepath.Join(r.dir, "tmp"),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	extra := strings.Fields(os.Getenv("GNOVM_SCENARIO_ARGS"))
	model := scenarioModel()
	session := ""
	// What each turn printed goes beside project/ only after the last turn:
	// a turn's agent can read anything on disk, and an earlier turn's
	// stream holds what that turn was told, a secret the user then had mem
	// erase among it.
	var outs, errs [][]byte
	peers := sc.peers
	if v := os.Getenv("GNOVM_SCENARIO_PEERS"); v != "" && sc.peers > 0 {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("GNOVM_SCENARIO_PEERS=%q is not a count", v)
		}
		peers = n
	}
	turnCap := 40 * time.Minute
	if v := os.Getenv("GNOVM_SCENARIO_TURN_CAP"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("GNOVM_SCENARIO_TURN_CAP=%q: %v", v, err)
		}
		turnCap = d
	}
	for i, ts := range sc.turns {
		if peers > 0 && ts.fresh {
			// The peers: one gnosh process each, started together in the
			// project, all sharing its mem (each session is its own file).
			type peerOut struct {
				stdout, stderr bytes.Buffer
				err            error
			}
			outsP := make([]*peerOut, peers)
			turnStart := time.Now()
			var wg sync.WaitGroup
			for k := range peers {
				args := []string{"-p", "-y", "--output-format", "stream-json", "--cwd", project, "-m", model, "--small-model", model}
				args = append(args, "-n", fmt.Sprintf("%s %s %d p%d", r.scenario, r.variant, i+1, k+1))
				if appendPrompt != "" {
					args = append(args, "--append-system-prompt-file", appendPrompt)
				}
				args = append(args, extra...)
				args = append(args, "--", ts.prompt)
				po := &peerOut{}
				outsP[k] = po
				wg.Add(1)
				go func() {
					defer wg.Done()
					ctx, cancel := context.WithTimeout(context.Background(), turnCap)
					defer cancel()
					cmd := exec.CommandContext(ctx, gnosh, args...)
					cmd.Dir, cmd.Env = project, env
					cmd.Stdout, cmd.Stderr = &po.stdout, &po.stderr
					po.err = cmd.Run()
				}()
			}
			wg.Wait()
			r.peersWall = time.Since(turnStart)
			for k, po := range outsP {
				outs, errs = append(outs, po.stdout.Bytes()), append(errs, po.stderr.Bytes())
				tr := parseStream(bytes.NewReader(po.stdout.Bytes()))
				tr.prompt, tr.took, tr.turn = fmt.Sprintf("(peer %d of %d) %s", k+1, peers, ts.prompt), r.peersWall, i
				if po.err != nil && tr.answer == "" {
					tr.answer = fmt.Sprintf("(gnosh failed: %v; %s)", po.err, lastLine(po.stderr.String()))
					tr.isError = true
				}
				r.turns = append(r.turns, tr)
				t.Logf("turn %d peer %d: %d tool calls: %s", i+1, k+1, len(tr.calls), truncate(tr.answer, 200))
			}
			r.after = append(r.after, listFiles(project))
			t.Logf("turn %d: %d peers, %s from the first start to the last end", i+1, peers, r.peersWall.Round(time.Second))
			continue
		}
		args := []string{"-p", "-y", "--output-format", "stream-json", "--cwd", project, "-m", model, "--small-model", model}
		if !ts.fresh && session != "" {
			args = append(args, "-r", session)
		} else {
			// A named session gets no title request. On the AI box the
			// title requests of runs started together took every server
			// slot first, wiping the prompts cached there, and each run
			// then re-read its whole prompt: 1-2 minutes, not seconds.
			args = append(args, "-n", fmt.Sprintf("%s %s %d", r.scenario, r.variant, i+1))
		}
		if appendPrompt != "" {
			args = append(args, "--append-system-prompt-file", appendPrompt)
		}
		args = append(args, extra...)
		args = append(args, "--", ts.prompt)
		turnStart := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), turnCap)
		cmd := exec.CommandContext(ctx, gnosh, args...)
		cmd.Dir, cmd.Env = project, env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		outs, errs = append(outs, stdout.Bytes()), append(errs, stderr.Bytes())
		tr := r.fresh(parseStream(bytes.NewReader(stdout.Bytes())))
		tr.prompt, tr.took, tr.turn = ts.prompt, time.Since(turnStart), i
		if err != nil && tr.answer == "" {
			tr.answer = fmt.Sprintf("(gnosh failed: %v; %s)", err, lastLine(stderr.String()))
			tr.isError = true
		}
		session = cmp.Or(tr.session, session)
		r.turns = append(r.turns, tr)
		r.after = append(r.after, listFiles(project))
		t.Logf("turn %d: %d tool calls, %s: %s", i+1, len(tr.calls), tr.took.Round(time.Second), truncate(tr.answer, 200))
	}
	for i := range outs {
		_ = os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("turn-%d.jsonl", i+1)), outs[i], 0o600)
		_ = os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("turn-%d.stderr", i+1)), errs[i], 0o600)
	}
	r.vms = dumpPersistentVMs(t, filepath.Join(data, "gnosh", "gnovm"), project)
	for _, line := range sc.checks {
		r.checks = append(r.checks, r.check(line, project, env))
	}
	writeReport(t, r, sc)
}

// scenarioModel is the model every turn uses, as provider/model. Both -m and
// --small-model name it, so no side job asks a one-model router for another
// model and evicts this one.
func scenarioModel() string {
	return cmp.Or(os.Getenv("GNOVM_SCENARIO_MODEL"), "qwen/qwen3.8-27b")
}

// scenarioEmbeddings is mem's embedding server for the runs: the default
// unless GNOVM_SCENARIO_EMBEDDINGS is set, "" when it is set empty or "off".
func scenarioEmbeddings() string {
	v, set := os.LookupEnv("GNOVM_SCENARIO_EMBEDDINGS")
	if !set {
		return "http://127.0.0.1:8091/v1"
	}
	if v == "off" {
		return ""
	}
	return v
}

// writeScenarioConfig writes a run's gnosh.json: GNOVM_SCENARIO_CONFIG copied
// as it is when that is set, otherwise a config that knows the scenario
// model alone (one llama.cpp provider with model listing off, so that no run
// makes a one-model router swap models), its context window, the request
// timeout long turns need, mem's embedding server, and thinking off for
// reasoning "nothink" and on otherwise.
func writeScenarioConfig(t *testing.T, path, reasoning string) {
	if from := os.Getenv("GNOVM_SCENARIO_CONFIG"); from != "" {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatalf("GNOVM_SCENARIO_CONFIG: %v", err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	provider, model, ok := strings.Cut(scenarioModel(), "/")
	if !ok || provider == "" || model == "" {
		t.Fatalf("GNOVM_SCENARIO_MODEL=%q is not provider/model", scenarioModel())
	}
	window := 262144
	if v := os.Getenv("GNOVM_SCENARIO_CONTEXT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("GNOVM_SCENARIO_CONTEXT=%q is not a token count", v)
		}
		window = n
	}
	options := map[string]any{"requestTimeout": 900}
	if e := scenarioEmbeddings(); e != "" {
		options["memEmbeddingsUrl"] = e
	}
	cfg := map[string]any{
		"providers": map[string]any{provider: map[string]any{
			"type":       "llamacpp",
			"baseUrl":    cmp.Or(os.Getenv("GNOVM_SCENARIO_BASEURL"), "http://127.0.0.1:8080"),
			"listModels": false,
		}},
		"models":  map[string]any{provider + "/" + model: map[string]any{"contextWindow": window}},
		"model":   map[string]any{"id": provider + "/" + model, "thinking": reasoning != "nothink"},
		"options": options,
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// parseStream reads gnosh run's stream-json output: the session id, each
// tool call with its result, and the final answer.
func parseStream(r io.Reader) turnResult {
	var tr turnResult
	byID := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Result    string `json:"result"`
			IsError   bool   `json:"is_error"`
			Message   struct {
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					Content   json.RawMessage `json:"content"`
					IsError   bool            `json:"is_error"`
					Text      string          `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		tr.session = cmp.Or(tr.session, ev.SessionID)
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				switch {
				case c.Type == "tool_use":
					byID[c.ID] = len(tr.calls)
					tr.calls = append(tr.calls, toolCall{id: c.ID, name: c.Name, input: string(c.Input)})
				case c.Type == "text" && strings.TrimSpace(c.Text) != "":
					tr.said = append(tr.said, c.Text)
				}
			}
		case "user":
			for _, c := range ev.Message.Content {
				if i, ok := byID[c.ToolUseID]; ok && c.Type == "tool_result" {
					var s string
					if json.Unmarshal(c.Content, &s) != nil {
						s = string(c.Content)
					}
					tr.calls[i].result, tr.calls[i].isError = s, c.IsError
				}
			}
		case "result":
			tr.answer, tr.isError = ev.Result, ev.IsError
		}
	}
	return tr
}

// fresh drops from tr the calls and texts an earlier turn already had: when
// a pop cuts rows of an earlier turn, gnosh's stream prints them again, with
// their old tool-call ids (C4 in crushtest/IMPROVEMENTS.md), and they would
// count as this turn's.
func (r *scenarioRun) fresh(tr turnResult) turnResult {
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	calls, said := tr.calls[:0], tr.said[:0]
	for _, c := range tr.calls {
		if c.id == "" || !r.seen["call:"+c.id] {
			calls = append(calls, c)
		}
	}
	for _, text := range tr.said {
		if !r.seen["said:"+text] {
			said = append(said, text)
		}
	}
	for _, c := range calls {
		r.seen["call:"+c.id] = true
	}
	for _, text := range said {
		r.seen["said:"+text] = true
	}
	tr.calls, tr.said = calls, said
	return tr
}

// listFiles reads the files under dir, by slash-separated path, so that a
// check after a turn sees each file as that turn left it. A file over 1 MB
// keeps only its first megabyte.
func listFiles(dir string) map[string]string {
	files := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			b, _ := os.ReadFile(p)
			files[filepath.ToSlash(rel)] = string(b[:min(len(b), 1<<20)])
		}
		return nil
	})
	return files
}

// dumpPersistentVMs opens the run's VMs as gnosh left them and renders every
// package-level value of each persistent one.
func dumpPersistentVMs(t *testing.T, gnovmDir, project string) map[string]string {
	dumps := map[string]string{}
	if _, err := os.Stat(gnovmDir); err != nil {
		return dumps
	}
	h, err := NewHost(Options{Dir: gnovmDir, Client: NewClientID()})
	if err != nil {
		t.Logf("opening the VMs: %v", err)
		return dumps
	}
	defer h.Close()
	list, err := h.List(project)
	if err != nil {
		t.Logf("listing the VMs: %v", err)
		return dumps
	}
	for _, vm := range list.VMs {
		if !vm.Options.Persist {
			continue
		}
		if vm.Name == MemVM {
			dumps[vm.Name] = dumpMem(h, project)
			continue
		}
		res, err := h.Run(context.Background(), RunRequest{Project: project, Name: vm.Name, Code: "0"})
		if err != nil || res.Err != nil {
			dumps[vm.Name] = fmt.Sprintf("(could not rebuild: %v %v)", err, res.Err)
			continue
		}
		s := h.vms[projectKey(project)+"/"+vm.Name]
		dumps[vm.Name] = fmt.Sprintf("%d entries\n%s", vm.Entries, dumpGlobals(t, s.res))
	}
	return dumps
}

// seedMem runs a scenario's mem section in project's mem as the user, typed
// in no session: what mem held before the scenario's first turn, a secret
// the user means to have erased, say. With an embedding server, the rows
// get their vectors now: gnosh makes them in the background as a session
// starts, and a first prompt's recall does not wait for them.
func seedMem(gnovmDir, project, code, embeddings string) error {
	h, err := NewHost(Options{Dir: gnovmDir, Client: NewClientID()})
	if err != nil {
		return err
	}
	defer h.Close()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ensureBase()
	if _, err := h.mem(project).call(context.Background(), memRequest{Code: code, User: true}); err != nil {
		return fmt.Errorf("the mem section: %w", err)
	}
	if embeddings == "" {
		return nil
	}
	if err := fillMemVectorsNow(context.Background(), memDir(gnovmDir, project), newMemEmbedder(embeddings)); err != nil {
		return fmt.Errorf("the mem section's vectors: %w", err)
	}
	return nil
}

// memEmbeddingsURL is the embedding server a run's gnosh is given, if any:
// options.memEmbeddingsUrl in the gnosh.json under cfg (comments allowed, as
// gnosh allows them).
func memEmbeddingsURL(cfg string) string {
	b, err := os.ReadFile(filepath.Join(cfg, "gnosh", "gnosh.json"))
	if err != nil {
		return ""
	}
	var c struct {
		Options struct {
			MemEmbeddingsURL string `json:"memEmbeddingsUrl"`
		} `json:"options"`
	}
	if json.Unmarshal(stripJSONComments(b), &c) != nil {
		return ""
	}
	return c.Options.MemEmbeddingsURL
}

// stripJSONComments removes // and /* */ comments outside strings, so that a
// gnosh.json written by hand decodes as JSON.
func stripJSONComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inString:
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}

// evalInVM rebuilds a run's VM from its journal and returns an expression's
// value there.
func evalInVM(gnovmDir, project, vm, expr string) (string, error) {
	h, err := NewHost(Options{Dir: gnovmDir, Client: NewClientID()})
	if err != nil {
		return "", err
	}
	defer h.Close()
	res, err := h.Run(context.Background(), RunRequest{Project: project, Name: vm, Code: expr})
	if err == nil && res.Err != nil {
		err = res.Err
	}
	if err != nil {
		return "", err
	}
	// mem shows a trailing read as output, not as a value.
	return strings.TrimRight(cmp.Or(res.Value, res.Output), "\n"), nil
}

// dumpMem is mem as a run left it: its outline, then every change its
// journal holds, one line each ("op note fix-ci/1: the claim").
func dumpMem(h *Host, project string) string {
	var b strings.Builder
	res, err := h.Run(context.Background(), RunRequest{Project: project, Name: MemVM, Code: "mem.Show()"})
	switch {
	case err != nil:
		fmt.Fprintf(&b, "(could not show: %v)\n", err)
	case res.Err != nil:
		fmt.Fprintf(&b, "(could not show: %v)\n", res.Err)
	default:
		b.WriteString(res.Output)
	}
	_, entries, _, err := readMemJournal(filepath.Join(memDir(h.opts.Dir, project), "journal.jsonl"))
	if err != nil {
		fmt.Fprintf(&b, "(could not read the journal: %v)\n", err)
	}
	for _, e := range entries {
		for _, op := range e.Ops {
			fmt.Fprintf(&b, "op %s %s:", op.Kind, op.Name)
			for _, s := range []string{op.Field, op.Old, op.New} {
				if s != "" {
					b.WriteString(" " + s)
				}
			}
			b.WriteString("\n")
		}
	}
	return fmt.Sprintf("%d entries\n%s", len(entries), b.String())
}

// memCount is one of mem's counts, from mem.JSON(): open, done, dropped,
// facts, rules, or items (open, done and dropped).
func memCount(gnovmDir, project, what string) (int, error) {
	out, err := evalInVM(gnovmDir, project, MemVM, "mem.JSON()")
	if err != nil {
		return 0, err
	}
	var m struct {
		Counts struct{ Open, Done, Dropped, Facts, Rules int } `json:"counts"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return 0, err
	}
	c := m.Counts
	switch what {
	case "open":
		return c.Open, nil
	case "done":
		return c.Done, nil
	case "dropped":
		return c.Dropped, nil
	case "facts":
		return c.Facts, nil
	case "rules":
		return c.Rules, nil
	case "items":
		return c.Open + c.Done + c.Dropped, nil
	}
	return 0, fmt.Errorf("mem has no count %q", what)
}

var opRe = regexp.MustCompile(`^(>=|<=|==|>|<)$`)

// check evaluates one check line; see the scenario files for the forms.
func (r *scenarioRun) check(line, project string, env []string) checkResult {
	f := strings.Fields(line)
	res := checkResult{line: line}
	turn := -1 // all turns
	if len(f) > 2 && f[0] == "turn" {
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 1 || n > len(r.turns) {
			res.got = "no such turn"
			return res
		}
		turn, f = n-1, f[2:]
	}
	countOK := func(n int, op, want string) bool {
		w, err := strconv.Atoi(want)
		if err != nil || !opRe.MatchString(op) {
			return false
		}
		switch op {
		case ">=":
			return n >= w
		case "<=":
			return n <= w
		case "==":
			return n == w
		case ">":
			return n > w
		}
		return n < w
	}
	switch {
	case len(f) == 4 && f[0] == "tool":
		n := 0
		for i, tr := range r.turns {
			if turn >= 0 && i != turn {
				continue
			}
			for _, c := range tr.calls {
				if c.name == f[1] {
					n++
				}
			}
		}
		res.got, res.ok = fmt.Sprintf("%d calls", n), countOK(n, f[2], f[3])
	case len(f) >= 4 && (f[0] == "answer" || f[0] == "said"):
		idx := len(r.turns) - 1
		if f[1] != "last" {
			n, err := strconv.Atoi(f[1])
			if err != nil || n < 1 || n > len(r.turns) {
				res.got = "no such turn"
				return res
			}
			idx = n - 1
		}
		if idx < 0 {
			res.got = "no turns"
			return res
		}
		answer := r.turns[idx].answer
		if f[0] == "said" {
			// All the turn's text, as the user read it, not only its last
			// message: after a judge's reopen the last is about the fix.
			answer = strings.Join(append(append([]string(nil), r.turns[idx].said...), answer), "\n")
		}
		want := strings.Join(f[3:], " ")
		res.got = truncate(answer, 160)
		switch f[2] {
		case "contains":
			res.ok = strings.Contains(strings.ToLower(answer), strings.ToLower(want))
		case "matches":
			re, err := regexp.Compile(want)
			res.ok = err == nil && re.MatchString(answer)
		}
	case len(f) >= 3 && (f[0] == "file" || f[0] == "after"):
		files := listFiles(project)
		if f[0] == "after" {
			n, err := strconv.Atoi(f[1])
			if err != nil || n < 1 || n > len(r.after) || len(f) < 5 {
				res.got = "bad after"
				return res
			}
			files, f = r.after[n-1], f[2:]
		}
		content, exists := files[f[1]]
		switch f[2] {
		case "exists":
			res.ok, res.got = exists, fmt.Sprint(exists)
		case "absent":
			res.ok, res.got = !exists, fmt.Sprint(exists)
		case "contains":
			res.ok = exists && strings.Contains(content, strings.Join(f[3:], " "))
			res.got = truncate(content, 120)
		case "lacks":
			// A file that does not hold the words, or is not there at all:
			// what a turn must not have done yet.
			res.ok = !strings.Contains(content, strings.Join(f[3:], " "))
			res.got = truncate(content, 120)
		}
	case len(f) >= 2 && f[0] == "cmd":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		// A login shell's profile may put another gnosh first on PATH again;
		// the run's own bin (the gnosh under test) goes back in front.
		script := "export PATH='" + filepath.Join(r.dir, "bin") + "':\"$PATH\"; " + strings.Join(f[1:], " ")
		cmd := exec.CommandContext(ctx, "bash", "-lc", script)
		cmd.Dir, cmd.Env = project, env
		b, err := cmd.CombinedOutput()
		res.ok, res.got = err == nil, truncate(strings.TrimSpace(string(b)), 200)
	case len(f) == 3 && f[0] == "vm-persist":
		res.got, res.ok = fmt.Sprintf("%d persistent", len(r.vms)), countOK(len(r.vms), f[1], f[2])
	case len(f) >= 3 && f[0] == "vm-dump" && f[1] == "contains":
		want := strings.ToLower(strings.Join(f[2:], " "))
		for name, d := range r.vms {
			if strings.Contains(strings.ToLower(d), want) {
				res.ok, res.got = true, "in "+name
			}
		}
		if !res.ok {
			res.got = fmt.Sprintf("not in %d persistent VMs", len(r.vms))
		}
	case len(f) == 4 && f[0] == "mem-count":
		// mem-count <what> <op> <n>: one of mem's counts.
		n, err := memCount(filepath.Join(r.dir, "data", "gnosh", "gnovm"), project, f[1])
		res.got = fmt.Sprintf("%d %s", n, f[1])
		if err != nil {
			res.got = err.Error()
		}
		res.ok = err == nil && countOK(n, f[2], f[3])
	case len(f) >= 5 && f[0] == "vm-eval":
		// vm-eval <vm> <expression, no spaces> <op or contains> <value>:
		// the expression's value in the VM, rebuilt from its journal.
		got, err := evalInVM(filepath.Join(r.dir, "data", "gnosh", "gnovm"), project, f[1], f[2])
		res.got = truncate(got, 160)
		switch {
		case err != nil:
			res.got = err.Error()
		case f[3] == "contains":
			res.ok = strings.Contains(strings.ToLower(got), strings.ToLower(strings.Join(f[4:], " ")))
		default:
			n, err := strconv.Atoi(got)
			res.ok = err == nil && countOK(n, f[3], f[4])
		}
	default:
		res.got = "unknown check"
	}
	return res
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// writeReport writes what a run did, for a person to read.
func writeReport(t *testing.T, r *scenarioRun, sc *scenario) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s / %s\n\n%s\n\n", r.scenario, r.variant, sc.about)
	for _, tr := range r.turns {
		kind := "same session"
		if sc.turns[tr.turn].fresh {
			kind = "new session"
		}
		fmt.Fprintf(&b, "## Turn %d (%s, %s)\n\n**Prompt:** %s\n\n", tr.turn+1, kind, tr.took.Round(time.Second), tr.prompt)
		for _, c := range tr.calls {
			mark := ""
			if c.isError {
				mark = " (error)"
			}
			fmt.Fprintf(&b, "- `%s`%s %s\n  → %s\n", c.name, mark, truncate(c.input, 400), truncate(strings.ReplaceAll(c.result, "\n", " ⏎ "), 300))
		}
		fmt.Fprintf(&b, "\n**Answer:** %s\n\n", tr.answer)
	}
	b.WriteString("## Checks\n\n")
	for _, c := range r.checks {
		mark := "FAIL"
		if c.ok {
			mark = "ok"
		}
		fmt.Fprintf(&b, "- %s: `%s` (%s)\n", mark, c.line, c.got)
	}
	b.WriteString("\n## Persistent Gno machines at the end\n\n")
	names := make([]string, 0, len(r.vms))
	for n := range r.vms {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		b.WriteString("(none)\n")
	}
	for _, n := range names {
		fmt.Fprintf(&b, "### %s\n\n```\n%s\n```\n\n", n, truncate(r.vms[n], 4000))
	}
	if err := os.WriteFile(filepath.Join(r.dir, "report.md"), []byte(b.String()), 0o600); err != nil {
		t.Logf("writing the report: %v", err)
	}
}

// writeSummary writes a table of every run's checks.
func writeSummary(t *testing.T, path string, runs []*scenarioRun) {
	if len(runs) == 0 {
		return
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].scenario != runs[j].scenario {
			return runs[i].scenario < runs[j].scenario
		}
		return runs[i].variant < runs[j].variant
	})
	var b strings.Builder
	b.WriteString("| scenario | variant | checks passed | gnovm calls | other calls | time | report |\n|---|---|---|---|---|---|---|\n")
	for _, r := range runs {
		passed := 0
		for _, c := range r.checks {
			if c.ok {
				passed++
			}
		}
		gn, other := 0, 0
		for _, tr := range r.turns {
			for _, c := range tr.calls {
				if strings.HasPrefix(c.name, "gnovm_") {
					gn++
				} else {
					other++
				}
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %d/%d | %d | %d | %s | %s |\n", r.scenario, r.variant, passed, len(r.checks), gn, other,
			r.took.Round(time.Second), filepath.Join(r.dir, "report.md"))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Logf("writing the summary: %v", err)
	}
	t.Logf("summary (%s):\n%s", path, b.String())
}

// The checks on mem read mem v2 as a run left it: it is counted among the
// persistent VMs, its dump holds its outline and every change its journal
// holds, vm-eval shows a read of it, and mem-count reads its counts.
func TestScenarioChecksReadMem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := newTestHost(t, dir)
	for _, code := range []string{
		`d := mem.Todo("implement Div"); d.Todo("write it"); d.Todo("test it")`,
		`mem.Note("implement-div/write", "see calc.go:12")`,
		`mem.Done("implement-div/write", "done")`,
		`mem.Fact("db/staging", "db-stg-9")`,
	} {
		run(t, h, MemVM, code)
	}
	require.NoError(t, h.Close())

	dumps := dumpPersistentVMs(t, dir, "/proj")
	require.Len(t, dumps, 1)
	require.Contains(t, dumps[MemVM], "4 entries\n")
	require.Contains(t, dumps[MemVM], "[ ] `implement-div` \"implement Div\"")
	require.Contains(t, dumps[MemVM], "op note implement-div/write#see-calc-go-12: see calc.go:12\n")
	require.Contains(t, dumps[MemVM], "op fact db/staging: db-stg-9\n")

	got, err := evalInVM(dir, "/proj", MemVM, "mem.Show()")
	require.NoError(t, err)
	require.Contains(t, got, "[x] `implement-div/write` \"write it\" → done")
	for what, want := range map[string]int{"open": 2, "done": 1, "items": 3, "facts": 1, "rules": 0} {
		n, err := memCount(dir, "/proj", what)
		require.NoError(t, err)
		require.Equal(t, want, n, what)
	}
}
