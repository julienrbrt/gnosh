# pi with and without pi-gnosh

The question: does gnosh's mem stack and Gno machines, with frames and slides
in place of pi's summary, do better than pi's own compaction?

## Setup

Both arms are the same pi (1.6.0), model, effort and settings; only the
extension differs.

| | |
| :- | :- |
| tasks | FrontierHarness `frontier-harness-eval@v1.1.0` (Harbor 0.26, Docker), the tasks where pi compacted in earlier 128K runs plus two other long ones: dna-assembly, dna-insert, gcode-to-text, intrastat-meldung, mteb-leaderboard, vpp-loss-divergence, wal-recovery-ordering |
| model | `deepseek/deepseek-v4.1-flash-1017` on OpenRouter, provider pinned (BaseTen), reasoning effort max, also for pi's summary calls |
| window | 65,536 tokens in pi's `models.json` for both arms (pi's default 128K made compaction rare), max output 32,768 |
| compaction | `reserveTokens` 16,384 (point 49,152), `keepRecentTokens` 20,000 |
| pi-gnosh | slides at the same point; pi's summary only when slides cannot bring the context under it |

## Results (round 1, 2026-11-23/24, one trial per task and arm)

| task | pi | pi + pi-gnosh |
| :- | :- | :- |
| gcode-to-text (pilot) | **pass**, 13.1m, 78K out, $0.17, 4 compactions | timeout (15m), 71K out, $0.15, 0 compactions, 4 slides, 4 pushes / 2 pops |
| dna-assembly | timeout (30m), 184K out, $0.33, 6 compactions | fail, 13.3m, 88K out, $0.15, 0 compactions, 2 slides, 5 / 5 |
| dna-insert | **pass**, 13.1m, 81K out, $0.14, 2 compactions | fail, 4.7m, 29K out, $0.05, 0 compactions, 0 slides, 6 / 6 |
| intrastat-meldung | fail, 96.3m, 486K out, $1.03, 22 compactions | fail, 10.8m, 63K out, $0.16, 0 compactions, 0 slides, 14 / 14 |
| mteb-leaderboard | **pass**, 10.3m, 43K out, $0.12, 3 compactions | **pass**, 15.6m, 29K out, $0.12, 0 compactions, 4 slides, 4 / 4 |
| vpp-loss-divergence | fail, 157.4m, 1,067K out, $2.11, 43 compactions | fail, 9.5m, 31K out, $0.22, 0 compactions, 12 slides, 2 / 0 |
| wal-recovery-ordering | **pass**, 26.8m, 185K out, $0.36, 7 compactions | fail (2 of 97 hidden tests), 14.2m, 87K out, $0.13, 0 compactions, 1 slide, 6 / 6 |
| **total** | **4/7**, 344m, 2.12M out, $4.26, 87 compactions | **1/7**, 83m, 0.40M out, $0.97, 0 compactions, 23 slides, 39 pops |

Costs are from each session's own usage at BaseTen list prices (input
$0.30, cached $0.007, output $1.20 per M), pi's summary calls included.
`python3 bench/analyze.py` gives the full table (requests, cached tokens,
peak context, Gno calls).

What it shows, and what it does not:

- **pi-gnosh replaced compaction completely.** It never let pi summarize, and
  the context stayed under pi's point (peak 36–58K against 48–61K for plain
  pi, whose summaries sometimes ran over).
- **It lost on the answers, and not because of lost context.** On dna-insert
  and intrastat-meldung nothing slid at all, and on most tasks very little.
  The pi-gnosh runs ended much sooner, with about a fifth of plain pi's
  output: the model popped its plan and stopped. A gnosh session checks every
  top-level todo closed as done before a turn may end, and sends the model
  back when it does not hold (`internal/memjoin/goal.go`). `gnosh bridge` did
  not run that check, so pi stopped on the model's word. vpp-loss-divergence
  even stopped with its frame still open. The extension now has a check
  before stopping (`src/check.ts`); round 1 ran without it.
- **One trial per task is noise.** One rerun of dna-insert with the check
  (below) ran 28 minutes instead of 4.7, before the check ever fired. 4/7
  against 1/7 is not a significant difference either way, and gcode-to-text
  timed out at 15 minutes, which cuts the gnosh arm short.
- The 64K window is smaller than pi's default 128K. It was chosen so that
  compaction happens at all on these tasks.

### With the check before stopping (one trial)

| task | pi + pi-gnosh with the check |
| :- | :- |
| dna-insert | fail, 28.0m, 192K out, $0.35, 0 compactions, 7 slides, 8 / 8. The check fired once, after the top todo's pop. The model re-checked with its tools and kept the same wrong primer split (Tm difference 7.4 °C by the verifier's annealing region, against the 5 °C allowed). |

The check works as intended: the run is sent back once, and the model
verifies against the files instead of its mem results. It did not change
this answer, and a single trial cannot say whether it closes the gap.

### What a meaningful comparison needs

Three trials per task and arm on these seven tasks, with the check: about
$20 at the prices above (plain pi about $13, most of it on
vpp-loss-divergence and intrastat-meldung, and pi-gnosh about $3 to $7
depending on how much longer the check makes its runs), and 5–6 hours of
wall time at 3 trials at once.

## Rerun

Everything is in [`bench/`](bench): `pi_harbor/agent.py` (`PiArm`, Harbor's
pi agent with the window and compaction settings set, and for the gnosh arm
this extension and a gnosh binary uploaded and gnosh put on `PATH`),
`run.sh`, `tasks.txt`, and `analyze.py` for the table.

    cd bench
    (cd ../../.. && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /tmp/gnosh .)
    export GNOSH_BIN=/tmp/gnosh OPENAI_BASE_URL=https://openrouter.ai/api/v1 OPENAI_API_KEY=...
    ARM=vanilla JOBNAME=r1-vanilla ./run.sh
    ARM=gnosh   JOBNAME=r1-gnosh   ./run.sh
    python3 analyze.py jobs/r1-vanilla jobs/r1-gnosh

The runs here also pinned the provider and set max effort on pi's summary
calls through a small local proxy; without it OpenRouter may route to
different providers per request.
