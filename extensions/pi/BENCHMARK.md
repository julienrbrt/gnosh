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
| **total** | **4/7**, 347m, 2.12M out, $4.26, 87 compactions | **1/7**, 83m, 0.40M out, $0.97, 0 compactions, 23 slides, 37 pops |

Costs are from each session's own usage at BaseTen list prices (input
$0.30, cached $0.007, output $1.20 per M), pi's summary calls included.
`python3 bench/analyze.py` gives the full table (requests, cached tokens,
peak context, Gno calls).

What it shows, and what it does not:

- **pi-gnosh replaced compaction completely in these runs.** It never let
  pi summarize. Peak context was 36–58K against 48–61K for plain pi;
  both sometimes exceeded the 49,152-token compaction point.
- **It lost on the answers. Early stopping is a hypothesis, not a proven
  cause of those failures.** On dna-insert and intrastat-meldung no rounds
  slid, but frame pops still removed work from context. Thus zero slides
  does not rule out information loss. The pi-gnosh runs ended much sooner,
  with about a fifth of plain pi's output: the model popped its plan and
  stopped. A gnosh session checks every
  top-level todo closed as done before a turn may end, and sends the model
  back when it does not hold (`internal/memjoin/goal.go`). `gnosh bridge` did
  not run that check, so pi stopped on the model's word. vpp-loss-divergence
  even stopped with its frame still open. Round 1 ran without gnosh's
  checks: neither the close check with its review nor the turn-end judge.
  Both now run in the bridge, as gnosh's own code (see the README).
- **One trial per task is noise.** One rerun of dna-insert with the check
  (below) ran 28 minutes instead of 4.7, before the check ever fired. 4/7
  against 1/7 is not a significant difference either way, and gcode-to-text
  timed out at 15 minutes, which cuts the gnosh arm short.
- The 64K window is smaller than pi's default 128K. It was chosen so that
  compaction happens at all on these tasks.

### With a self-check before stopping (one trial; since replaced)

Between round 1 and the bridge's own checks, the extension sent the same
model back to check its work itself before the run could end. That was not
gnosh's check (no separate judge, no close review), and it has been replaced
by gnosh's own.

| task | pi + pi-gnosh with the self-check |
| :- | :- |
| dna-insert | fail, 28.0m, 192K out, $0.35, 0 compactions, 7 slides, 8 / 8. The check fired once, after the top todo's pop. The model re-checked with its tools and kept the same wrong primer split (Tm difference 7.4 °C by the verifier's annealing region, against the 5 °C allowed). |

The self-check fired as intended: the run was sent back once and the model
attempted verification against the files, but kept a wrong answer. This
is not an independent judge and does not establish correctness. A single
trial cannot say whether it closes the gap.

### What a meaningful comparison needs

Three trials per task and arm on these seven tasks, with gnosh's checks in
the bridge: about $20 to $25 at the prices above (plain pi about $13, most of
it on vpp-loss-divergence and intrastat-meldung; pi-gnosh about $5 to $10,
since the close reviews and judges are model calls of their own, about one
per todo closed, with up to 1,500 tokens out each, and send-backs make runs
longer), and 5–6 hours of wall time at 3 trials at once.

## Rerun

Everything is in [`bench/`](bench): `pi_harbor/agent.py` (`PiArm`, Harbor's
pi agent with the window and compaction settings set, and for the gnosh arm
this extension and a gnosh binary uploaded and gnosh put on `PATH`),
`run.sh`, `tasks.txt`, and `analyze.py` for the table.

    cd bench
    (cd ../../.. && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /tmp/gnosh .)
    export GNOSH_BIN=/tmp/gnosh OPENAI_BASE_URL=https://openrouter.ai/api/v1 OPENAI_API_KEY=...
    ARM=vanilla JOBNAME=r2-vanilla ./run.sh
    ARM=gnosh   JOBNAME=r2-gnosh   ./run.sh
    python3 analyze.py jobs/r2-vanilla jobs/r2-gnosh

The runs here also went through a small local proxy, which:

- pins the provider (BaseTen);
- sets max reasoning effort on the main requests and on pi's summary
  calls;
- sets low effort on gnosh's own check requests (tools-less, at most 2,048
  tokens: the close review, the goal judge and the namer). gnosh asks its
  lowest thinking for those.

Without the proxy, OpenRouter may route each request to a different
provider.

## Settings for the next round, and why

| setting | round 1 | next | why |
| :- | :- | :- | :- |
| window | 64K | 128K | Both arms use the same window. The model's own window is 1M, so the window is artificial on purpose: with no compaction there is nothing to compare. At 64K, plain pi compacted 83 times on six tasks (43 on vpp-loss-divergence alone, a 157-minute run). The gnosh arm carried about 2.8K more fixed prefix (4.4–5.1K against 1.6–2.3K) and slid inside todos 19 times. 128K is still small enough that the long tasks compact (vpp-loss-divergence, intrastat-meldung, wal-recovery-ordering, dna-assembly), and it is the regime gnosh's frames are designed for (`frames.md` §8.8: "for the intended regime run 32K or more"). |
| reserve | 16K | 32K | pi caps each answer at window − input − margin (pi-ai `clampMaxTokensToContext`). In round 1 that cap cut replies to one token: vanilla's 400 errors "max_tokens must be at least 2" and the length stops that ended both vpp runs. A 32K reserve leaves about 28K of answer at the point, above the largest reply seen (21K). |
| keep recent | 20K | 20K | pi's default |
| max output | 32K | 32K | no reply came near it (largest 21K) |
| timeouts | ×1 | ×1 | the tasks' own; `TMULT` changes it for both arms |
| trials | 1 | 3 per task and arm, 7 tasks | |

The gnosh arm's own fix for the one-token cap is in the extension (`staleUsage`). After a pop or slide, pi sized requests from a reply's usage that still counted the removed work.
