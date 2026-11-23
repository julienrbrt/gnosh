package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// The cache figures: a request that begins with the one before and reads
// it back from the cache is no miss; one that reads it again is, by about
// what it read again; one that begins otherwise (a summary, a new system
// prompt) is a rebuild; and a server that never says what its cache
// served has no misses.
func TestCacheTotals(t *testing.T) {
	long := func(word string) string { return strings.Repeat(word+" ", 3000) }
	conv := []core.Message{core.UserText(long("first"))}
	req := func(system string, msgs []core.Message) *core.Request {
		return &core.Request{System: []string{system}, Messages: append([]core.Message(nil), msgs...)}
	}
	turn := func(msgs []core.Message, reply, next string) []core.Message {
		return append(append([]core.Message(nil), msgs...),
			core.Message{Role: core.RoleAssistant, Blocks: []core.Block{core.Text(reply)}}, core.UserText(next))
	}
	now := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	a := &Agent{est: defaultEstimate}

	a.cacheSeen(req("S", conv), core.Usage{InputTokens: 10000}, now)
	conv = turn(conv, "one", "go on")
	a.cacheSeen(req("S", conv), core.Usage{InputTokens: 300, CacheReadTokens: 10000}, now)
	if c := a.Usage().Cache; !c.Observed || !c.Warm || c.Misses != 0 || c.Rebuilds != 0 {
		t.Fatalf("after a request the cache served: %+v", c)
	}
	conv = turn(conv, "two", "and more")
	a.cacheSeen(req("S", conv), core.Usage{InputTokens: 10400}, now.Add(time.Minute))
	c := a.Usage().Cache
	if c.Warm || c.Misses != 1 || c.MissTokens < 9000 || c.MissTokens > 10400 || !c.LastMiss.Equal(now.Add(time.Minute)) {
		t.Fatalf("after a request that read it all again: %+v", c)
	}

	// A summary in place of the conversation, then a new system prompt:
	// nothing could be served, and neither is a miss.
	a.cacheSeen(req("S", []core.Message{core.UserText(long("summary"))}), core.Usage{InputTokens: 9000}, now)
	a.cacheSeen(req("S2", []core.Message{core.UserText(long("summary"))}), core.Usage{InputTokens: 9100}, now)
	if c := a.Usage().Cache; c.Rebuilds != 2 || c.Misses != 1 {
		t.Fatalf("after a summary and a new system prompt: %+v", c)
	}

	// A server that never says.
	b := &Agent{est: defaultEstimate}
	conv = []core.Message{core.UserText(long("first"))}
	b.cacheSeen(req("S", conv), core.Usage{InputTokens: 10000}, now)
	conv = turn(conv, "one", "go on")
	b.cacheSeen(req("S", conv), core.Usage{InputTokens: 10300}, now)
	if c := b.Usage().Cache; c.Observed || c.Misses != 0 {
		t.Errorf("a server that says nothing of its cache: %+v", c)
	}
}
