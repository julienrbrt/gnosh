package bridge

import (
	"context"
	"encoding/json"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// Protocol is the version of spec/gnosh/bridge.md this package speaks.
const Protocol = 1

// MaxLine is the longest request line the bridge reads (bridge.md §3).
const MaxLine = 64 << 20

// The requests the bridge answers, in the order hello lists them.
const (
	OpHello = "hello"
	OpTools = "tools"
	OpCall  = "call"
	OpView  = "view"
	// The three below carry what a gnosh session tells its model of mem
	// and makes room with, beside the frames view (bridge.md §10).
	OpPrompt = "prompt"
	OpRoom   = "room"
	OpStep   = "step"
	// end is asked as a host's turn would end: gnosh's turn-end checks
	// (bridge.md §11). side_answer is the host's answer to a side request
	// the bridge wrote while it answered a request (§11.1); it is not
	// listed in ops, since it is never asked on its own.
	OpEnd        = "end"
	OpSideAnswer = "side_answer"
)

// ops is what hello says the bridge answers.
var ops = []string{OpHello, OpTools, OpCall, OpView, OpPrompt, OpRoom, OpStep, OpEnd}

// The error codes of bridge.md §8.
const (
	CodeBadRequest  = "bad_request"
	CodeProtocol    = "protocol"
	CodeUnknownOp   = "unknown_op"
	CodeUnknownTool = "unknown_tool"
	CodeConfirm     = "confirm"
	CodeUnavailable = "unavailable"
	CodeInternal    = "internal"
)

// Request is one line a host sends. Which fields count depends on Op.
type Request struct {
	Op string          `json:"op"`
	ID json.RawMessage `json:"id,omitempty"`

	// hello
	Protocol int    `json:"protocol,omitempty"`
	Client   string `json:"client,omitempty"`
	// Side says the host answers side requests (§11.1).
	Side bool `json:"side,omitempty"`

	// call
	Session   string          `json:"session,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Confirmed bool            `json:"confirmed,omitempty"`

	// view (Session too)
	Entries    []transcript.Entry `json:"entries,omitempty"`
	Budget     int                `json:"budget,omitempty"`
	Room       int                `json:"room,omitempty"`
	KeepRounds int                `json:"keep_rounds,omitempty"`
	Prompt     string             `json:"prompt,omitempty"`

	// prompt (Session too). Text is what the person typed: with it, the
	// prompt starts a turn (§11).
	First bool   `json:"first,omitempty"`
	Text  string `json:"text,omitempty"`

	// room (Session, Entries and Prompt too)
	Need      int      `json:"need,omitempty"`
	EditTools []string `json:"edit_tools,omitempty"`

	// step (Session and Entries too)
	Point int `json:"point,omitempty"`
	Used  int `json:"used,omitempty"`

	// Lean says the host wants only the lines that matter under context
	// pressure: prompt and step then leave out the mem line, the frame
	// line, the large-step line and the mem reminder, the nudges that bias
	// the model toward mem and early pops on work a plain host would keep
	// in context. The system memory text, the tools, the slide (room), the
	// slide-soon line and the turn-end checks are unaffected. It is a host
	// option; gnosh's own agent loop does not go through the bridge.
	Lean bool `json:"lean,omitempty"`

	// Keep, on prompt, says the host wants the kept facts (§10.6): the
	// task's requirements, word for word, shown again once the model is back
	// at the root after a pop, and what a pop's result must carry told with
	// the session's first push. Text is the task.
	Keep bool `json:"keep,omitempty"`

	// room and step: Covered says the host slides only what mem covers
	// (§10.4): room makes no cut that holds work mem got no record of
	// since, and step warns the model before such a cut would be due.
	Covered bool `json:"covered,omitempty"`

	// room: Autokeep says the host lets the bridge keep, in mem, a digest of
	// the work a cut would take that mem has no record of, instead of leaving
	// that cut to the host's summary (§10.7). It needs Covered, and a bridge
	// whose mem can be written to.
	Autokeep bool `json:"autokeep,omitempty"`

	// step: Nudge says the host wants the todo line (§10.7): once the context
	// is filling and the session stands in no todo, so that nothing can slide
	// out of it, the model is told to put the work in todos.
	Nudge bool `json:"nudge,omitempty"`

	// end (Session and Entries too): the reply the turn would end on, and
	// whether the model finished it itself (not cut off at its length).
	Reply    *core.Message `json:"reply,omitempty"`
	Finished bool          `json:"finished,omitempty"`

	// side_answer: the side request's id, and the model's answer, or why
	// there is none. Stop is the answer's stop reason as core names them;
	// max_tokens makes it no answer.
	SideID int    `json:"side_id,omitempty"`
	Answer string `json:"answer,omitempty"`
	Stop   string `json:"stop,omitempty"`
	Failed string `json:"failed,omitempty"`
}

// Reply is one line the bridge writes. Which fields are set depends on the
// request it answers.
type Reply struct {
	OK    bool            `json:"ok"`
	ID    json.RawMessage `json:"id,omitempty"`
	Error *Error          `json:"error,omitempty"`

	// hello
	Protocol int      `json:"protocol,omitempty"`
	Build    string   `json:"build,omitempty"`
	Version  string   `json:"version,omitempty"`
	Project  string   `json:"project,omitempty"`
	Ops      []string `json:"ops,omitempty"`

	// tools
	Tools []ToolInfo `json:"tools,omitempty"`

	// call
	Content []core.Block `json:"content,omitempty"`
	IsError bool         `json:"is_error,omitempty"`
	Frames  []Frame      `json:"frames,omitempty"`

	// view
	Messages   []core.Message     `json:"messages,omitempty"`
	Sources    [][]string         `json:"sources,omitempty"`
	Stack      []StackFrame       `json:"stack,omitempty"`
	Tokens     int                `json:"tokens,omitempty"`
	OverBudget bool               `json:"over_budget,omitempty"`
	NewSlides  []transcript.Entry `json:"new_slides,omitempty"`

	// prompt, room and step
	System string   `json:"system,omitempty"`
	Parts  []string `json:"parts,omitempty"`
	Freed  int      `json:"freed,omitempty"`
	Notice string   `json:"notice,omitempty"`

	// end: "continue" sends the model back with Send, as a hidden prompt
	// of gnosh's own; "end" lets the turn end. Driver and Objection say
	// which check holds the turn open and why, Stopped the model's reason
	// when it stopped at a checkpoint. Notices are lines for the person,
	// for call and end alike.
	Action    string   `json:"action,omitempty"`
	Send      string   `json:"send,omitempty"`
	Driver    string   `json:"driver,omitempty"`
	Objection string   `json:"objection,omitempty"`
	Stopped   string   `json:"stopped,omitempty"`
	Notices   []string `json:"notices,omitempty"`
}

// SideRequest is a request for the host's model that the bridge writes,
// as a line of its own, while it answers a request (§11.1): gnosh's close
// review, goal judge and namer ask one. The host answers it with a
// side_answer request.
type SideRequest struct {
	ID          int      `json:"id"`
	Purpose     string   `json:"purpose"`
	System      string   `json:"system,omitempty"`
	Prompt      string   `json:"prompt"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TimeoutMS   int64    `json:"timeout_ms,omitempty"`
}

// sideLine is how a side request is written.
type sideLine struct {
	Side *SideRequest `json:"side"`
}

// Turns is gnosh's session work for the host's sessions, beside the tools:
// what a call to mem is checked against, and the checks asked as a turn
// would end (§11). memjoin is the one implementation.
type Turns interface {
	// Prompt is a prompt the person typed, which starts a turn.
	Prompt(ctx context.Context, session, text string)
	// Sync is the session's conversation as the host has it, through the
	// reply whose call is about to run.
	Sync(session string, entries []transcript.Entry)
	// Calling and Called come around every call the bridge runs.
	Calling(session string, call core.ToolCall)
	Called(ctx context.Context, session string, call core.ToolCall, res core.ToolResult)
	// End is asked as the turn would end on reply.
	End(ctx context.Context, session string, reply core.Message, finished bool) EndVerdict
	// Notices takes the lines for the person said since it was last asked.
	Notices(session string) []string
}

// EndVerdict is what Turns.End says.
type EndVerdict struct {
	Continue  bool
	Send      string
	Driver    string
	Objection string
	Stopped   string
	Notices   []string
}

// Error is what a reply with ok false says went wrong.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Summary is, for CodeConfirm, the one line saying what the call
	// would do.
	Summary string `json:"summary,omitempty"`
	// Protocols is, for CodeProtocol, the versions the bridge speaks.
	Protocols []int `json:"protocols,omitempty"`
}

// ToolInfo is one tool as tools lists it.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Confirm says some calls of the tool need a person's approval first.
	Confirm bool `json:"confirm,omitempty"`
}

// Frame is one frame a call pushed or popped, as mem kept it.
type Frame struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Purpose  string `json:"purpose,omitempty"`
	Result   string `json:"result,omitempty"`
	Thoughts int    `json:"thoughts,omitempty"`
	Session  string `json:"session"`
}

// StackFrame is one frame a session stands in, as view reports it.
type StackFrame struct {
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	CallID string `json:"call_id,omitempty"`
	Slid   bool   `json:"slid,omitempty"`
}
