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
| window (round 1) | 65,536 tokens in pi's `models.json` for both arms (pi's default 128K made compaction rare), max output 32,768. Round 2 used 128K; see the settings table below. |
| compaction | `reserveTokens` 16,384 (point 49,152), `keepRecentTokens` 20,000 |
| pi-gnosh | slides at the same point; pi's summary only when slides cannot bring the context under it |

Round 3 is reported below the round-2 settings. The extension arm is complete (21/21); one plain-pi trial is still running.

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
the bridge, was the plan for round 2. The estimate beforehand was about $20
to $32 and 6–8 hours. Round 2 ran those settings and is reported below. It
was cut short by the API key's own $50 limit, so nine trials never got a
model reply.


## Results (round 2, 2026-11-24, 128K window, up to three trials)

Both arms used the settings in the table below, on the same seven tasks, with
gnosh's close review and turn-end judge in the bridge, slides only when mem
held a record of the work, and the fix for pi's answer cap after a cut. The
run started at 21:59 UTC+1 and the last scored trial finished at 23:58 UTC+1.

The OpenRouter key's own limit is $50. That is separate from the $75 ceiling
that had been approved for this round, and it cannot be raised from the
runner. The local proxy was therefore capped at $47.25 so the key would not
start refusing mid-reply. The proxy began returning 429 at 23:54 UTC+1
(spent $47.10). In-flight replies pushed spend to $47.32. Key usage ended at
$49.66 of $50.

Nine trials that Harbor still scored as reward 0 were not real attempts: the
agent ran for one or two seconds, the first completion came back with
`stopReason: error` and no tokens, and the verifier then marked the untouched
workspace as a fail. They are listed at the end and are excluded from the
counts and means below. A raw 9/21 against 3/21 would count those as fails.

| task | pi | pi + pi-gnosh |
| :- | :- | :- |
| dna-assembly | **2/3**, mean 16.1m, $0.22, 5 compactions | 0/3, mean 12.8m, $0.19, 0 compactions, 0 slides, 20 / 20 |
| dna-insert | **2/2**, mean 8.7m, $0.09, 0 compactions (1 unrun) | **1/2**, mean 7.3m, $0.10, 0 slides, 12 / 12 (1 unrun) |
| gcode-to-text | **2/3**, mean 10.2m, $0.14, 2 compactions | 0/2, mean 12.0m, $0.14, 1 slide, 8 / 6; one 15m timeout (1 unrun) |
| intrastat-meldung | 0/2, mean 38.2m, $0.77, 16 compactions (1 unrun) | 0/2, mean 15.0m, $0.33, 3 slides, 19 / 18 (1 unrun) |
| mteb-leaderboard | **2/3**, mean 14.3m, $0.18, 5 compactions | **2/3**, mean 18.2m, $0.14, 2 slides, 15 / 12 |
| vpp-loss-divergence | **1/2**, mean 48.5m, $0.71, 17 compactions (1 unrun) | 0/2, mean 77.0m, $1.31, 50 slides, 23 / 16 (1 unrun) |
| wal-recovery-ordering | 0/2, mean 10.7m, $0.15, 1 compaction (1 unrun) | 0/2, mean 10.1m, $0.15, 0 slides, 14 / 14 (1 unrun) |
| **total (scored)** | **9/17**, 334m, 2.15M out, $5.03, 46 compactions | **3/16**, 336m, 1.62M out, $5.09, 3 compactions, 56 slides, 111 pushes / 98 pops |

Costs are again from each session's own usage at the same BaseTen list
prices, side requests included. The gnosh arm's close reviews, judges and
namers cost $0.42 of its $5.09. Key spend for the round was about $10.35.
`python3 bench/analyze.py` still prints every Harbor trial, including the
nine unrun ones; drop trials with no tokens before you take a mean.

What it shows, and what it does not:

- **On the trials that actually ran, pi-gnosh still lost.** 3/16 against
  9/17. It matched plain pi only on mteb-leaderboard (2/3 each) and did not
  beat it on any task. That is not explained by the nine unrun trials, by
  cut-off replies (there were no length stops), or by a missing close check
  in the bridge.
- **Early stopping is no longer the whole story.** Several short tasks had
  similar run times on both arms. On vpp-loss-divergence the gnosh arm ran
  longer and spent more (99.2m + 54.9m, $1.94 + $0.69) than plain pi
  (47.7m + 49.3m, $0.74 + $0.67) and still failed both. One of those gnosh
  runs slid 44 times, never popped a frame, and never reached the turn-end
  judge.
- **Some losses were not from slides.** All three dna-assembly trials on
  the gnosh arm failed with zero slides; peak context was 42–89K, under or
  around the 96K compaction point. The model closed its frames (20 pops)
  and stopped. wal-recovery-ordering was the same pattern: 0/2 each, no
  slides on the gnosh arm, similar time and cost.
- **The mem stack did not fully replace compaction.** The gnosh arm still
  let pi write a summary three times, when a slide was refused or could not
  bring the context under the point (four slides were refused because mem
  held no record). 56 slides went ahead, most of them on vpp-loss-divergence.
- **The turn-end judge never sent the model back** (0 send-backs). It ran
  on 12 of the 16 scored trials; five of those 12 replies were cut off at
  300 output tokens, because reasoning used most of the small cap, and
  those trials still ended. Four scored trials never reached a turn-end
  judge at all: the 15-minute gcode-to-text timeout, one mteb-leaderboard
  fail that had 11 mid-run API errors, and both vpp-loss-divergence fails.
  Close reviews mostly completed (90 of 91). Almost every frame-namer call
  also hit the length cap; that is a setup problem for names, and it is
  not counted as a send-back.
- **The Gno VM was barely used for maths** (20 scratch calls). The rest of
  the gnovm calls are mem. This round still tests the memory stack more
  than the VM.
- **The 15-minute gcode-to-text timeout** is the task's own limit. One
  gnosh trial hit it; plain pi's three trials all finished under it.

### Trials that did not run

All nine started after the proxy was already returning 429 (from 23:54
UTC+1). Each agent's first model call failed; wall time was under two
minutes, most of it setup.

| arm | trial | started (UTC+1) |
| :- | :- | :- |
| pi | dna-insert__KVGN39S | 23:55:55 |
| pi | wal-recovery-ordering__nG4JMf4 | 23:56:04 |
| pi | vpp-loss-divergence__LB6zJY5 | 23:56:38 |
| pi | intrastat-meldung__gzHtx4d | 23:57:06 |
| pi + pi-gnosh | gcode-to-text__mrXWi2F | 23:56:12 |
| pi + pi-gnosh | dna-insert__zdp7sh3 | 23:56:51 |
| pi + pi-gnosh | vpp-loss-divergence__ftM9Usa | 23:57:03 |
| pi + pi-gnosh | wal-recovery-ordering__EeYrivV | 23:57:02 |
| pi + pi-gnosh | intrastat-meldung__iPr8oR3 | 23:57:33 |

No other trial was left unrun. The one Harbor exception among the scored
trials is gcode-to-text__F3rG7nZ on the gnosh arm (`AgentTimeoutError` at
15 minutes); that is a real attempt and is counted in 0/2 above.

To finish the planned 3×7×2, the OpenRouter key limit has to be raised
(it has $0.34 left of $50). Only these nine trials should be rerun, at the
same settings. That is likely about $4 to $8 more, most of it one more
vpp-loss-divergence and one more intrastat-meldung on each arm. The 33
scored trials should be kept.

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

## Settings used in round 2, and why

| setting | round 1 | round 2 | why |
| :- | :- | :- | :- |
| window | 64K | 128K | Both arms use the same window. The model's own window is 1M, so the window is artificial on purpose: with no compaction there is nothing to compare. At 64K, plain pi compacted 83 times on six tasks (43 on vpp-loss-divergence alone, a 157-minute run). The gnosh arm carried about 2.8K more fixed prefix (4.4–5.1K against 1.6–2.3K) and slid inside todos 19 times. 128K is still small enough that the long tasks compact (vpp-loss-divergence, intrastat-meldung, wal-recovery-ordering, dna-assembly), and it is the regime gnosh's frames are designed for (`frames.md` §8.8: "for the intended regime run 32K or more"). |
| reserve | 16K | 32K | pi caps each answer at window − input − margin (pi-ai `clampMaxTokensToContext`). In round 1 that cap cut replies to one token: vanilla's 400 errors "max_tokens must be at least 2" and the length stops that ended both vpp runs. A 32K reserve leaves about 28K of answer at the point, above the largest reply seen (21K). |
| keep recent | 20K | 20K | pi's default |
| max output | 32K | 32K | no reply came near it (largest 21K) |
| timeouts | ×1 | ×1 | the tasks' own; `TMULT` changes it for both arms |
| trials | 1 | 3 per task and arm, 7 tasks | |

The gnosh arm's own fix for the one-token cap is in the extension (`staleUsage`). After a pop or slide, pi sized requests from a reply's usage that still counted the removed work.

## Results (round 3, 2026-11-26, 128K window, three trials)

Same seven tasks and the same settings as round 2 (128K window, 32K reserve,
20K keep recent, 32K max output). The gnosh arm is the lean build of
`e789bc9` (PR #12): lean mode on by default, check requests with reasoning
off, a verify-after-pop note, a per-frame slide cap of 6, and kept facts
shown at the root after a pop. A 5-cent smoke test first confirmed that, with
reasoning off, the judge returns a complete JSON verdict (`finish_reason`
stop, 53 tokens, no reasoning) and the namer returns usable names (reasoning
tokens 0).

The run started at 09:32 UTC+1. A box replacement at about 11:50 UTC+1
stopped the in-flight trials; finished results were kept. The job was resumed
at 12:36 UTC+1 on a new OpenRouter key (limit $50, starting from $0) with
`harbor jobs resume`. The gnosh arm finished all 21 trials. One plain-pi
trial, `intrastat-meldung__cHAhLeY`, is still running as of 15:59 UTC+1 and is
excluded from the pi totals below. This section will be updated when it lands.

| task | pi | pi + pi-gnosh |
| :- | :- | :- |
| dna-assembly | 0/3, mean 5.8m, $0.08, 0 compactions | 0/3, mean 9.5m, $0.16, 0 compactions, 1 slide |
| dna-insert | 1/3, mean 4.8m, $0.06, 0 compactions | **2/3**, mean 8.5m, $0.16, 3 compactions, 0 slides |
| gcode-to-text | **2/3**, mean 7.8m, $0.11, 3 compactions; one 15m timeout | **2/3**, mean 12.7m, $0.19, 0 compactions, 3 slides; one 15m timeout that still passed |
| intrastat-meldung | 0/2, mean 32.0m, $0.76, 15 compactions (1 running) | 0/3, mean 34.2m, $0.82, 10 compactions, 16 slides, 8 refused, 2 send-backs |
| mteb-leaderboard | **3/3**, mean 23.8m, $0.16, 4 compactions | **2/3**, mean 29.8m, $0.14, 2 compactions, 1 slide; one 60m timeout |
| vpp-loss-divergence | **1/3**, mean 112.8m, $1.63, 57 compactions | **3/3**, mean 96.5m, $1.45, 42 compactions, 12 slides |
| wal-recovery-ordering | **2/3**, mean 10.2m, $0.17, 3 compactions | **2/3**, mean 8.1m, $0.14, 0 compactions, 0 slides |
| **total** | **9/20**, 559m, 3.41M out, $8.16, 82 compactions (1 running) | **11/21**, 598m, 3.68M out, $9.16, 57 compactions, 33 slides, 89 pops |

Costs are from each session's own usage at the same BaseTen list prices, side
requests included. The gnosh arm's close reviews, judges and namers cost
$0.33 of its $9.16. Proxy spend for the whole of round 3, including the one
trial still running, is about $23.58.

What it shows, and what it does not:

- **The extension is ahead on the finished trials, 11/21 against 9/20.** In
  round 2 the scored trials were 3/16 against plain pi's 9/17. The remaining
  plain-pi `intrastat-meldung` cannot overtake that lead on its own (plain pi
  would finish 9/21 or 10/21).
- **The gain is on the long task that used to be a loss.** All three
  `vpp-loss-divergence` trials passed on the gnosh arm (3/3, mean 96.5m,
  12 slides, 42 compactions), against 1/3 on plain pi (mean 112.8m, 57
  compactions). In round 2 the gnosh arm was 0/2 on this task, including a
  44-slide runaway that never popped. The slide cap of 6 held: the busiest
  finished frame path on vpp was 7 slides of a trial that also compacted,
  and that trial passed. One of the three vpp wins used no frames at all
  and fell through to pi's summaries 23 times (lean mode, no pushes).
- **On the short tasks the arms now match or the extension is slightly
  ahead:** dna-insert 2/3 vs 1/3, gcode-to-text 2/3 each, wal-recovery-ordering
  2/3 each. mteb-leaderboard is 2/3 against plain pi's 3/3.
  **dna-assembly is still 0/3 on both arms** (plain pi was 2/3 in round 2).
  Kept facts did fire: the push addendum on 18 of 21 gnosh trials, the
  kept-facts list 30 times across 13 trials, and the verify-after-pop note
  51 times across 16 trials. That did not produce a passing primer set.
- **The namer still hits its 24-token cap** (62 of 93 calls ended with
  `finish_reason` length; 31 finished with `stop`). Reasoning is not the
  cause (0 reasoning tokens, as in the smoke test). For a single text the
  namer's budget is 24 tokens (`internal/memjoin/namer.go`); the model often
  writes more than one candidate name, or a name plus extra lines, and is
  cut off. When several texts are named together the budget is 16 per line,
  and complete answers often use 28–38 tokens. Raising the single-name cap
  from 24 to 48 or 64, and tightening the prompt to one name on one line,
  would stop most of these cut-offs. `readNames` already takes the first
  line, so the names used in this run were usually still usable.
- **The turn-end judge ran to completion** (18/18 goal-judge calls and 86/86
  close reviews finished with `stop`). It sent the model back twice, both
  on `intrastat-meldung__zVEXhkU`, when it found the close not met (portal
  rejected, then a mismatch between the hub XML and the archived XML). On
  every other finished trial the `end` op returned `action: end`. That is
  not a wiring failure: on the failed dna-assembly, wal, and gcode trials
  the judge said "Task done", taking the agent's visible files and
  self-reported checks as proof, while the hidden verifier later failed.
  A send-back only happens when the judge says a closed todo is not met.
  Tightening the judge so it does not treat the agent's own summary as
  sufficient evidence is the follow-up, not another connection fix. The
  two send-backs on intrastat did not turn that trial into a pass.

This table will be updated when `intrastat-meldung__cHAhLeY` on the pi arm
finishes.
