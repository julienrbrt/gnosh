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

## Results

See the results file for the current table; this section is filled in when
the runs finish.

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
