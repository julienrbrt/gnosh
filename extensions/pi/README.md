# pi-gnosh

[gnosh](https://gnosh.ai)'s mem stack and Gno machines in
[pi](https://www.npmjs.com/package/@earendil-works/pi-coding-agent), in place
of pi's compaction.

pi makes room the usual way: when the conversation nears the window, it asks
the model for a summary and sends the summary in place of the old messages.
gnosh does not summarize. Its model keeps its plan, results and thoughts in
**mem**, a Gno realm run on a real GnoVM, and works in **frames**: it pushes
a todo, works in it, and pops it with its result. A pop takes the frame's
work out of what is sent; only the result stays. When the window fills anyway,
gnosh **slides**: the oldest rounds leave the context (mem still holds what
they found) with no summary and no extra model call. pi's compaction is used
only when nothing can slide, or when what would slide is work the model
kept no record of in mem.

This extension gives pi exactly that, with the checks gnosh runs around it
(the close check of every todo closed as done, and the judge as a turn would
end), and nothing else of gnosh.

## What it does in pi

| pi hook | what pi-gnosh does (all of it gnosh's own code, through `gnosh bridge`) |
| :- | :- |
| tools | registers gnosh's `gnovm_run`, `gnovm_new`, `gnovm_list`, `gnovm_drop` (mem is `gnovm_run` with `name: "mem"`; the scratch machine does the maths). A call to mem carries the conversation through its reply, so gnosh checks it as it checks a session's: the fresh guard, the close check (the claim check and the model's review of the close), the pop's receipt of what left the context |
| `before_agent_start` | adds gnosh's `<memory>` part after pi's system prompt (same bytes every time, so the provider's cache holds) and the mem line after the prompt |
| `context` | sends gnosh's view of the conversation: popped work and slid rounds are gone; pi's own message objects are reused, so the cached prefix stays byte for byte |
| `turn_end` | when over pi's compaction point, asks gnosh to slide, only what mem holds a record of (a thought, a result, a fact kept during or after that work; bridge.md §10.4), so that work the model kept nowhere is summarized by pi rather than dropped; near the point, gnosh tells the model to keep what it found before it slides; adds gnosh's notes (the slide line, and, unless lean, "this todo is large" and the mem reminder) as durable hidden messages |
| `context`, after a cut | the replies sent after the first message the view left out go without their usage: pi sizes the answer it asks for from the last reply's usage, and a reply from before a pop or slide reports the larger context of then, so pi would cap the answer at a token or two (one round-1 run ended that way with 29K of a 64K window in use) |
| the record | the session keeps, for the record only and never sent to the model, each side request with its usage (`gnosh-side`), each slide refused as uncovered (`gnosh-uncovered`) and each turn-end verdict (`gnosh-end`) |
| `session_before_compact` | cancels pi's summary when slides brought the conversation under pi's point; otherwise lets pi summarize, as a gnosh session does with no frame to cut |
| `turn_end`, no tool call | asks gnosh's turn-end checks (`end`): a turn that would end in a todo whose close was refused is sent back once, and every top-level todo closed as done in the turn, and not already approved by the close review, is judged; one found not met is opened again in mem and the model is sent back with gnosh's words. Every tenth send-back in a row asks the model first whether there is a genuine reason to stop |
| side requests | the close review, the goal judge and the namer ask a model; the bridge asks pi's, through `modelRegistry.streamSimple`, with gnosh's own system prompt, prompt, temperature and token limit, with reasoning off (gnosh asks its lowest thinking; a judge given 300 tokens or a namer given 24 answers nothing if it spends them reasoning) |

### The checks around a close and a turn's end

A gnosh session never takes the model's word that a todo is done. When a
call to mem closes a todo the work was done in, mem keeps nothing of it until
the close is approved: nothing done in the todo, or a result that says a file
changed that nothing in the todo changed or showed, is refused at once, and
otherwise a small model reads what happened in the todo and approves or
refuses it ([`spec/gnosh/close-check.md`](../../spec/gnosh/close-check.md)).
As a turn would end, every top-level todo closed as done that the review did
not already approve is judged against mem and the whole conversation, popped
work included, and one found not met is opened again and the model sent back
([`internal/memjoin/goal.go`](../../internal/memjoin/goal.go)).

Through `gnosh bridge` these are gnosh's own code: each host session gets the
join a gnosh session gets (`internal/memjoin/bridgehost.go`), reading the
conversation the extension sends, and the model requests it makes are
answered by pi's model on the same pipe (bridge.md §11). In the benchmark
gnosh ran with the same model as its small model, so pi's model is the one
it would have asked. The repetition guard and the empty-reply nudge of
gnosh's loop are not brought over: they are the loop's, not mem's, and pi
has neither for either arm.

The extension holds none of gnosh's logic. It starts `gnosh bridge` (JSON
lines over stdio; the protocol is [`spec/gnosh/bridge.md`](../../spec/gnosh/bridge.md)) and maps
pi's messages to gnosh's session entries and back. Frame rules, slide
planning and the wording to the model stay in gnosh, so they improve there.

### Why a bridge and not a port

The frames rule (`internal/transcript`: view, frames, slide) is about 4,800
lines with 10,000 lines of tests and changes daily; the texts the model is
told live in `internal/prompt`. A TypeScript port would be a second copy to
keep in step. The bridge runs the same code a gnosh session runs, in one
static binary, and costs about 0.2 ms per view on a short session (23 ms on a
1.5 MB one, sent whole). An MCP server alone would give the tools without the
cut, and mem's receipts would then tell the model something false ("removed
from your context when you pop it").

## Lean mode

By default the extension runs **lean**: it keeps the system memory text, the
tools, the slide and gnosh's turn-end checks, but leaves out the per-prompt and
per-step nudges (the mem line, the frame line, the "this todo is large" line and
the mem reminder). Those nudges bias the model toward mem and early pops on work
a plain pi session would simply keep in context, which costs tokens and closes
frames too soon on tasks that never reach the compaction point. Lean makes the
extension behave like plain pi until the window actually fills, where the slide
and the judge still apply.

Set `PI_GNOSH_LEAN=0` to restore gnosh's full set of nudges. Native gnosh is not
affected either way: its own agent loop does not go through the bridge.

## Kept facts

A closed todo leaves only its result, so a pop can lock in a conclusion that was
never checked. By default the extension asks the bridge to keep the task's own
requirements, word for word (file paths, numbers, identifiers, and lines that
ask for a check), and to show them again as a short list once the model is back
at the root after a pop, together with anything a closed todo said it left
unchecked. The first push also tells the model that its pop result must carry
those details. The list is capped at about 1.6 KB and shown once per pop.

Set `PI_GNOSH_KEEP=0` to turn it off. See `spec/gnosh/bridge.md` §10.6.

## Fewer summaries

pi still summarizes where gnosh cannot slide. In round 3, 44 of 57 summaries came
while the model stood in no todo (nothing to slide), and 5 more when a slide was
refused (work mem had no record of, or a todo that kept sliding). Two options,
both on by default, address these:

- **Todo line (`PI_GNOSH_NUDGE=0` turns it off).** With no todo open and the
  context at 40% of pi's point, and again at 75%, the model is told that a summary
  is the only way left to make room, and to put the work in todos. A popped todo's
  work leaves the context with no summary.
- **Automatic keep (`PI_GNOSH_AUTOKEEP=0` turns it off).** Where the model kept
  nothing in mem, the bridge writes a digest of the work that would slide (what
  the model said, each call, the head of each result) to mem as a thought, with no
  model call, and slides instead of refusing. The model is shown what was kept.
  If mem refuses the digest (it keeps no secrets), it is written again with long
  values shortened; if it refuses that, pi summarizes as before, so nothing is
  dropped that is kept nowhere. The slide limit (six slides of one todo without a
  pop) still applies.

See `spec/gnosh/bridge.md` §10.7.

## Use

Needs a gnosh with `gnosh bridge` (ops `prompt`, `room`, `step` and `end`) on `PATH`,
or `PI_GNOSH_PROGRAM=/path/to/gnosh`. Build it from this repository's root:

    CGO_ENABLED=0 go build -o ~/bin/gnosh .

Then, from this directory:

    npm install
    pi -e ./src/index.ts

`gnovm_drop` deleting a machine is put to the person with pi's confirm
dialog; without a UI it does not run. pi's own compaction settings
(`compaction.reserveTokens`) set the point where gnosh slides.

## See it work, with no model and no network

    npm install
    GNOSH_BIN=~/bin/gnosh npm run demo   # in extensions/pi

`demo/run.ts` drives pi's own faux provider through the same script twice,
in a 14K window: two large reads, a push, three large reads in the frame, a
sum on a Gno machine (`17 * 23 + 4` → `395`), a pop. It prints every request
pi sends. Plain pi has to summarize (an extra model call, and the reads are
replaced by a summary). With pi-gnosh: no summary; the rounds before the
frame and then the start of the frame slide out, the model is told so and
where mem has the plan; after the pop the frame's work is gone and the
request is smaller than at the peak. `npm test` checks the same (and
mapping, and the prefix staying byte-stable across a pop).

## Develop

    npm install
    npm run check
    GNOSH_BIN=/path/to/gnosh npm test   # the pi tests need a real gnosh

## Benchmark

See [BENCHMARK.md](BENCHMARK.md): pi with and without this extension on the
same FrontierHarness tasks, same model, same window, same settings, and how
to rerun it.

## License

As the rest of this repository: [LICENSE.md](../../LICENSE.md).
