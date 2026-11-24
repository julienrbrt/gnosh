#!/bin/bash
# pi vanilla vs pi + this extension on FrontierHarness tasks, Harbor + Docker.
# Same pi version, model, effort, window and compaction settings in both arms; only the extension differs.
# Needs: harbor (0.26), docker, an OpenAI-compatible endpoint for the model in OPENAI_BASE_URL/OPENAI_API_KEY
# (the runs here went through OpenRouter), and for ARM=gnosh a linux gnosh in GNOSH_BIN:
#   (cd ../../.. && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /tmp/gnosh .) && export GNOSH_BIN=/tmp/gnosh
# Usage: ARM=vanilla|gnosh [TASKS=tasks.txt] [ATTEMPTS=3] [CONC=3] [WINDOW=131072] [RESERVE=32768] [KEEP=20000]
#        [MAXOUT=32768] [TMULT=1.0] [JOBS=./jobs] [JOBNAME=..] ./run.sh
# Window 128K with a 32K reserve: pi compacts (or gnosh slides) at 96K, and at that point pi still asks for
# about 28K of answer (pi-ai caps the answer at window - input - margin), above the largest reply seen in
# round 1 (21K). Round 1 ran 64K with a 16K reserve (point 48K); see BENCHMARK.md.
set -euo pipefail
cd "$(dirname "$0")"
ARM=${ARM:?}; TASKS=${TASKS:-tasks.txt}; ATTEMPTS=${ATTEMPTS:-3}; CONC=${CONC:-3}; WINDOW=${WINDOW:-131072}; JOBS=${JOBS:-$PWD/jobs}
RESERVE=${RESERVE:-32768}; KEEP=${KEEP:-20000}; MAXOUT=${MAXOUT:-32768}; TMULT=${TMULT:-1.0}
MODEL=${MODEL:-deepseek/deepseek-v4.1-flash-1017}
: "${OPENAI_BASE_URL:?}" "${OPENAI_API_KEY:?}"
inc=(); while read -r t; do [[ -z $t || $t == \#* ]] || inc+=(-i "*/$t"); done < "$TASKS"
g=false; [ "$ARM" = gnosh ] && g=true
PYTHONPATH="$PWD" harbor run -d frontier-harness-eval/frontier-harness-eval@v1.1.0 -e docker -m "openai/$MODEL" \
  -k "$ATTEMPTS" -n "$CONC" -r 2 --timeout-multiplier "$TMULT" --retry-exclude AgentTimeoutError --retry-exclude VerifierTimeoutError "${inc[@]}" \
  -o "$JOBS" --job-name "${JOBNAME:-pi-$ARM-$(date +%m%d-%H%M)}" \
  -a pi_harbor.agent:PiArm --ak version=1.6.0 --ak model_api=openai-completions --ak reasoning_effort=max \
  --ak gnosh=$g --ak context_window="$WINDOW" --ak reserve_tokens="$RESERVE" --ak keep_recent_tokens="$KEEP" --ak max_output="$MAXOUT"
# Resume after an interruption (finished trials are kept): PYTHONPATH=$PWD harbor jobs resume -p "$JOBS/<jobname>"
# Table: python3 analyze.py "$JOBS"/<vanilla job> "$JOBS"/<gnosh job>
