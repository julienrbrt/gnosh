package agent

import (
	"encoding/binary"
	"hash/fnv"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// This file is how the model server's prompt cache served the session's
// main requests: Claude Code's prompt cache figures (its /usage line and
// the status line's prompt_cache). Beside how much of what was read came
// from the cache, it counts the requests that read again what the request
// before them had sent the same way. On a model server of the person's
// own that is where a slow turn's minutes go: another request took the
// cache's place, the server started again, or its chat template wrote the
// earlier turns anew.

// CacheTotals is how the prompt cache served the main requests.
type CacheTotals struct {
	// Observed is set once a reply has said how much of its prompt the
	// cache served or took in: a server that never says leaves it unset,
	// and then nothing is known of misses.
	Observed bool
	// Warm says the last main reply read some of its prompt from the
	// cache.
	Warm bool
	// Misses counts the main requests that began with what the request
	// before them sent, and read less of it back from the cache than it
	// was (by more than 5% and 2,000 tokens); MissTokens is about how many
	// tokens they read again, and LastMiss when the last one was.
	Misses     int
	MissTokens int
	LastMiss   time.Time
	// Rebuilds counts the main requests that began otherwise than the one
	// before, which no cache can serve: after a summary, a slide, mem
	// putting a frame away, a rewind, a new system prompt or tools.
	Rebuilds int
}

// cachePrint is a main request as the cache sees it, part by part: the
// system prompt and the tools, then each message. sums are the parts'
// fingerprints, ests their estimated tokens, and tokens what the provider
// counted for the whole prompt.
type cachePrint struct {
	sums   []uint64
	ests   []int
	tokens int
}

// missFloor and missShare are how short of the expected a cache read must
// fall to be a miss: by 2,000 tokens, and by more than one part in 20.
const (
	missFloor = 2000
	missShare = 20
	// rebuildShare: a request that keeps less than 9 parts in 10 of the one
	// before, as estimated, began anew.
	rebuildShare = 10
)

// printRequest is the cache's view of req, with estimates by a's guess.
func (a *Agent) printRequest(req *core.Request) cachePrint {
	h := fnv.New64a()
	for _, s := range req.System {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	for _, t := range req.Tools {
		h.Write([]byte(t.Name))
		h.Write([]byte{0})
		h.Write([]byte(t.Description))
		h.Write([]byte{0})
		h.Write(t.InputSchema)
		h.Write([]byte{0})
	}
	p := cachePrint{sums: []uint64{h.Sum64()}, ests: []int{a.estFixed(req)}}
	for _, m := range req.Messages {
		h.Reset()
		h.Write([]byte(m.Role))
		hashBlocks(h, m.Blocks)
		p.sums = append(p.sums, h.Sum64())
		p.ests = append(p.ests, a.estMessages([]core.Message{m}, true))
	}
	return p
}

// hashBlocks writes the blocks into h, every field that a request sends.
func hashBlocks(h interface{ Write([]byte) (int, error) }, bs []core.Block) {
	var n [8]byte
	for _, b := range bs {
		for _, s := range []string{string(b.Type), b.Text, b.Signature, b.Opaque, b.ID, b.Name, string(b.Input), b.ToolUseID, b.MediaType} {
			binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
			h.Write(n[:])
			h.Write([]byte(s))
		}
		h.Write(b.Data)
		if b.IsError {
			h.Write([]byte{1})
		}
		binary.LittleEndian.PutUint64(n[:], uint64(len(b.Content)))
		h.Write(n[:])
		hashBlocks(h, b.Content)
	}
}

// cacheSeen takes in a main request that was answered with the counts u:
// whether the cache served what it could have, set against the request
// before.
func (a *Agent) cacheSeen(req *core.Request, u core.Usage, now time.Time) {
	p := a.printRequest(req)
	p.tokens = u.PromptTokens()
	a.mu.Lock()
	defer a.mu.Unlock()
	c := &a.totals.Cache
	if u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
		c.Observed = true
	}
	c.Warm = u.CacheReadTokens > 0
	prev := a.cachePrev
	a.cachePrev = p
	if len(prev.sums) == 0 || prev.tokens <= 0 || p.tokens <= 0 {
		return
	}
	kept, all := 0, 0
	same := true
	for i, est := range prev.ests {
		all += est
		if same && i < len(p.sums) && p.sums[i] == prev.sums[i] {
			kept += est
		} else {
			same = false
		}
	}
	if all <= 0 || kept*rebuildShare < all*(rebuildShare-1) {
		c.Rebuilds++
		return
	}
	if !c.Observed {
		return
	}
	expected := int(int64(prev.tokens) * int64(kept) / int64(all))
	if short := expected - u.CacheReadTokens; short >= missFloor && short*missShare > expected {
		c.Misses++
		c.MissTokens += short
		c.LastMiss = now
	}
}
