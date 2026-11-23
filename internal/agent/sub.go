package agent

import (
	"errors"
	"slices"
	"sync"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// SubOptions is what makes a sub-agent different from the session that
// starts it.
type SubOptions struct {
	// CallID is the id of the tool call that starts the sub-agent. Every
	// event of the sub-agent carries it as Meta.Parent.
	CallID string
	// ID names the sub-agent. Empty lets the loop make an id.
	ID string
	// Type is the kind of sub-agent, such as "Explore". Hooks see it, and
	// the sub-agent's SessionStart moment is given it as the source.
	Type string

	// Log is the sub-agent's own transcript. It is required: a sub-agent's
	// conversation is never its parent's.
	Log Log
	// Tools and System are the sub-agent's own.
	Tools  []core.Tool
	System []string
	// Model, when not nil, is the sub-agent's model. Nil means the
	// parent's.
	Model *Model
	// Gate, when not nil, replaces the parent's gate. Nil means the
	// sub-agent stands where its parent stands: what the parent may do
	// without asking, it may too.
	Gate Gate
	// Moments are the sub-agent's own. PromptSubmit and PromptLines are
	// normally left out; SessionStart is where a SubagentStart hook adds
	// context to the first prompt. When AfterTool is nil the parent's is
	// used, so that the person's hooks see a sub-agent's tool calls.
	Moments Moments
	// Drivers are asked whether the sub-agent may finish: a SubagentStop
	// hook is one.
	Drivers []Driver
	// Cwd and Roots default to the parent's.
	Cwd   string
	Roots []string
	// Tree is the git work tree of its own the sub-agent works in, when
	// it has one: its tools are given it (core.ToolEnv.Tree), and the
	// gate keeps its changes in it.
	Tree *core.WorkTree
	// Store, when not nil, is the Store in which the sub-agent's tools keep
	// their own state (core.ToolEnv.Store). Nil makes a new one. It is
	// never the parent's: what a sub-agent has read, its parent has not.
	Store *sync.Map
}

// Spawn makes a sub-agent: a session of its own, run by this same loop,
// with its own log, tools and instructions. Its events go to the parent's
// consumer marked with the sub-agent's id and the call that started it,
// its questions are put to the person through the parent, and what it
// costs is added to the parent's totals when it is closed. Its tokens are
// never counted as the parent's context.
//
// A tool that starts a sub-agent gets its own session from the context
// (FromContext), spawns, runs the task with Run, and closes the sub-agent:
//
//	child, err := agent.FromContext(ctx).Spawn(agent.SubOptions{CallID: call.ID, …})
//	…
//	defer child.Close()
//	res, err := child.Run(ctx, agent.Prompt{Text: task})
//
// Run stops the sub-agent when ctx ends, which it does when the person
// stops the parent's turn. The sub-agent's answer is res.Text. A result
// whose Reason is EndHalted means something ended the sub-agent for good,
// and the tool then ends the parent's turn too by putting MetaHalt in its
// own result.
func (a *Agent) Spawn(sub SubOptions) (*Agent, error) {
	if sub.Log == nil {
		return nil, errors.New("agent: a sub-agent needs a Log of its own")
	}
	su := a.setup()
	opts := a.opts
	opts.Log = sub.Log
	opts.Tools = sub.Tools
	opts.System = sub.System
	opts.Model = su.model
	if sub.Model != nil {
		opts.Model = *sub.Model
	}
	opts.Small = su.small
	if sub.Gate != nil {
		opts.Gate = sub.Gate
	}
	opts.Moments = sub.Moments
	if opts.Moments.AfterTool == nil {
		opts.Moments.AfterTool = a.opts.Moments.AfterTool
	}
	if opts.Moments.AfterRound == nil {
		opts.Moments.AfterRound = a.opts.Moments.AfterRound
	}
	opts.Drivers = sub.Drivers
	opts.OnEvent = nil
	opts.Store = sub.Store
	opts.StartSource = sub.Type
	opts.CompactAt = su.compactAt
	opts.NoAutoCompact = su.noAuto
	a.mu.Lock()
	opts.Cwd, opts.Roots = a.cwd, slices.Clone(a.roots)
	a.mu.Unlock()
	if sub.Cwd != "" {
		opts.Cwd = sub.Cwd
	}
	if sub.Roots != nil {
		opts.Roots = sub.Roots
	}
	return newAgent(opts, a, sub)
}

// adopt adds a closed sub-agent's cost to this session's totals.
func (a *Agent) adopt(child *Agent) {
	sub := child.Usage()
	a.mu.Lock()
	a.totals.merge(sub)
	a.mu.Unlock()
}
