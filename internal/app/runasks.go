package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/config"
)

// This file is a run in the background whose permission questions a
// person answers from the agent view, as Claude Code's background sessions
// wait under Needs input (code.claude.com/docs/en/agent-view) where gnosh's
// used to refuse every call that asks. gnosh --bg, /background and /fork
// give the run the base of its log's name in AsksEnv; the run then puts
// each permission question in <base>.ask and waits, with no clock, for
// <base>.answer, which the view writes (runSource.Answer). Only permission
// questions go so: a key's password, the model's own questions and an MCP
// server's still have nobody to ask. Each question carries a random token
// that the answer must give back, and the token is in no file the model
// was shown, so a model that wrote the answer file itself would not be
// heard.

// AsksEnv names the base of the files a run's questions go through: the
// path of its log without .log. gnosh takes it out of its environment as
// it starts, as it does RestartedEnv: nothing it starts may see it.
const AsksEnv = "GNOSH_RUN_ASKS"

// prepareRun readies the files of a run in the background whose log is
// log, before it starts and before it can be listed as working: what an
// earlier run of the session left of questions, answers and words is
// cleared, and a new token is written, which words sent to the run must
// carry from its first moment. It returns the run's environment: this
// one's, and where its questions go.
func prepareRun(environ []string, log string) []string {
	base := runBase(log)
	for _, pattern := range []string{".ask-*.json", ".answer-*.json", ".inbox-*.json"} {
		paths, _ := filepath.Glob(base + pattern)
		for _, p := range paths {
			_ = os.Remove(p)
		}
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	_ = config.WriteFileAtomic(base+".token", []byte(hex.EncodeToString(b[:])+"\n"), 0o600)
	env := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool { return strings.HasPrefix(kv, AsksEnv+"=") })
	return append(env, AsksEnv+"="+base)
}

// askLook is how often a run looks for the answer to its question.
const askLook = 300 * time.Millisecond

// runAsk is a question a run waits on, as its .ask file holds it: the
// question the agent view shows, and the token its answer must give back.
type runAsk struct {
	AgentQuestion
	Token string
}

// runAnswer is the answer to it, as the .answer file holds it.
type runAnswer struct {
	ID     string `json:"id"`
	Token  string `json:"token"`
	Choice string `json:"choice"`
	Text   string `json:"text,omitempty"`
}

// askPath and answerPath are the files of the question id of the run whose
// files begin at base: one pair for each question, so that two put at once
// (a sub-agent's beside the main agent's) never share one.
func askPath(base, id string) string    { return base + ".ask-" + fileKey(id) + ".json" }
func answerPath(base, id string) string { return base + ".answer-" + fileKey(id) + ".json" }

// fileKey is a question's id as a part of a file's name.
func fileKey(id string) string {
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return hex.EncodeToString([]byte(id))
		}
	}
	return id
}

// runBase is where the files of the run whose log is log begin.
func runBase(log string) string { return strings.TrimSuffix(log, ".log") }

// relayAsk puts the permission question q of the run whose files begin at
// base in its .ask file, waits for the answer the agent view writes, and
// hands it to answer. It returns when the question is answered or ctx
// ends (the question was taken back, the run is ending); the .ask file
// goes with it either way. A question that cannot be written down is
// answered no, with why, so that the run does not wait on nobody.
func relayAsk(ctx context.Context, base string, q agent.Question, now func() time.Time, answer func(agent.Answer) error) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	ask := runAsk{AgentQuestion{ID: q.ID, Title: q.Title, Text: q.Text, Choices: q.Choices, Since: now().UTC()}, hex.EncodeToString(b[:])}
	data, _ := json.Marshal(ask)
	asks, answers := askPath(base, q.ID), answerPath(base, q.ID)
	if err := config.WriteFileAtomic(asks, data, 0o600); err != nil {
		_ = answer(agent.Answer{Choice: "no", Text: fmt.Sprintf("(gnosh could not put the question to the agent view: %v. Nobody saw it.)", err)})
		return
	}
	// (Its own files go with it, and no other question's.)
	defer os.Remove(asks)
	defer os.Remove(answers)
	tick := time.NewTicker(askLook)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		data, err := os.ReadFile(answers)
		if err != nil {
			continue
		}
		var a runAnswer
		if json.Unmarshal(data, &a) != nil || a.ID != ask.ID || a.Token != ask.Token {
			// Not the answer to this question: it is not heard.
			_ = os.Remove(answers)
			continue
		}
		_ = os.Remove(answers)
		_ = answer(agent.Answer{Choice: a.Choice, Text: a.Text})
		return
	}
}

// readAsk is the question the run whose log is log waits on, if any: the
// one it put first, when it waits on more than one.
func readAsk(log string) *runAsk {
	paths, _ := filepath.Glob(runBase(log) + ".ask-*.json")
	var first *runAsk
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var a runAsk
		if json.Unmarshal(data, &a) != nil || a.ID == "" || a.Token == "" || path != askPath(runBase(log), a.ID) {
			continue
		}
		if first == nil || a.Since.Before(first.Since) {
			first = &a
		}
	}
	return first
}

// answerAsk writes the answer the person chose to the question the run
// whose log is log waits on: choice is the key of one of its choices, and
// text words that go with it.
func answerAsk(log, id, choice, text string) error {
	var a runAsk
	data, err := os.ReadFile(askPath(runBase(log), id))
	if err != nil || json.Unmarshal(data, &a) != nil || a.ID != id {
		return fmt.Errorf("the question was answered or taken back already")
	}
	known := false
	for _, c := range a.Choices {
		known = known || c.Key == choice
	}
	if !known {
		return fmt.Errorf("%q is not one of the question's answers", choice)
	}
	data, _ = json.Marshal(runAnswer{ID: a.ID, Token: a.Token, Choice: choice, Text: text})
	return config.WriteFileAtomic(answerPath(runBase(log), id), data, 0o600)
}

// A run's inbox: words a person sends a working run from the agent view
// (typed while peeking at it, as Claude Code's peek replies), which it
// reads between its steps as prompts of the person's. Each message is a
// file of its own, <base>.inbox-<time>.json, and carries the run's token,
// which the run writes to <base>.token as it starts: a message without it
// is not heard. What a run did not read before it ended stays in its
// inbox, for the view to send it back to work on (unreadWords).

// inboxLook is how often a run reads its inbox.
const inboxLook = time.Second

// inboxMessage is one message in a run's inbox.
type inboxMessage struct {
	Token string `json:"token"`
	Text  string `json:"text"`
}

// tokenFile is where the run whose log is log keeps its token.
func tokenFile(log string) string { return strings.TrimSuffix(log, ".log") + ".token" }

// inboxMessages are the files of the messages in the inbox of the run
// whose files begin at base, oldest first.
func inboxMessages(base string) []string {
	paths, _ := filepath.Glob(base + ".inbox-*.json")
	slices.Sort(paths)
	return paths
}

// readInbox hands submit each message the run's inbox gets that carries
// its token (prepareRun's, or one made here for a run started otherwise),
// until ctx ends; the channel it returns is closed once it has stopped
// reading, so that nothing it took is left half handed over.
func readInbox(ctx context.Context, base string, submit func(string) error) (<-chan struct{}, error) {
	done := make(chan struct{})
	data, err := os.ReadFile(base + ".token")
	token := strings.TrimSpace(string(data))
	if err != nil || token == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		token = hex.EncodeToString(b[:])
		if err := config.WriteFileAtomic(base+".token", []byte(token+"\n"), 0o600); err != nil {
			close(done)
			return done, err
		}
	}
	go func() {
		defer close(done)
		tick := time.NewTicker(inboxLook)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			for _, path := range inboxMessages(base) {
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				var m inboxMessage
				if json.Unmarshal(data, &m) != nil || m.Token != token {
					_ = os.Remove(path) // not heard
					continue
				}
				if ctx.Err() != nil {
					return // (what came now stays for the view)
				}
				if strings.TrimSpace(m.Text) == "" || submit(m.Text) == nil {
					_ = os.Remove(path)
				}
			}
		}
	}()
	return done, nil
}

// tellRun puts words in the inbox of the run whose log is log.
func tellRun(log, text string, now time.Time) error {
	token, err := os.ReadFile(tokenFile(log))
	if err != nil {
		return fmt.Errorf("it reads no words: a gnosh from before the agent view's replies started it")
	}
	data, _ := json.Marshal(inboxMessage{Token: strings.TrimSpace(string(token)), Text: text})
	return config.WriteFileAtomic(fmt.Sprintf("%s.inbox-%020d.json", strings.TrimSuffix(log, ".log"), now.UnixNano()), data, 0o600)
}

// unreadWords takes back the words a run was told and did not read before
// it ended: what its inbox holds, which is emptied.
func unreadWords(log string) []string {
	data, _ := os.ReadFile(tokenFile(log))
	token := strings.TrimSpace(string(data))
	var out []string
	for _, path := range inboxMessages(runBase(log)) {
		var m inboxMessage
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &m) == nil && token != "" && m.Token == token && strings.TrimSpace(m.Text) != "" {
			out = append(out, m.Text)
		}
		_ = os.Remove(path)
	}
	return out
}
