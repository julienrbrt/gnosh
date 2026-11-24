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
only when nothing can slide.

This extension gives pi exactly that and nothing else of gnosh.

## What it does in pi

| pi hook | what pi-gnosh does (all of it gnosh's own code, through `gnosh bridge`) |
| :- | :- |
| tools | registers gnosh's `gnovm_run`, `gnovm_new`, `gnovm_list`, `gnovm_drop` (mem is `gnovm_run` with `name: "mem"`; the scratch machine does the maths) |
| `before_agent_start` | adds gnosh's `<memory>` part after pi's system prompt (same bytes every time, so the provider's cache holds) and the mem line after the prompt |
| `context` | sends gnosh's view of the conversation: popped work and slid rounds are gone; pi's own message objects are reused, so the cached prefix stays byte for byte |
| `turn_end` | when over pi's compaction point, asks gnosh to slide; adds gnosh's notes (the slide line, "this todo is large", the mem reminder) as durable hidden messages |
| `session_before_compact` | cancels pi's summary when slides brought the conversation under pi's point; otherwise lets pi summarize, as a gnosh session does with no frame to cut |

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

## Use

Needs a gnosh with `gnosh bridge` (ops `prompt`, `room` and `step`) on `PATH`,
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
