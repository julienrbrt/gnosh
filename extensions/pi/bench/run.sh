#!/bin/bash
# pi vanilla vs pi + this extension on FrontierHarness tasks, Harbor + Docker.
# Same pi version, model, effort, window and compaction settings in both arms; only the extension differs.
# Needs: harbor (0.26), docker, an OpenAI-compatible endpoint for the model in OPENAI_BASE_URL/OPENAI_API_KEY
# (the runs here went through OpenRouter), and for ARM=gnosh a linux gnosh in GNOSH_BIN:
#   (cd ../../.. && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /tmp/gnosh .) && export GNOSH_BIN=/tmp/gnosh
# Usage: ARM=vanilla|gnosh [TASKS=tasks.txt] [ATTEMPTS=1] [CONC=3] [WINDOW=65536] [JOBS=./jobs] [JOBNAME=..] ./run.sh
set -euo pipefail
cd "$(dirname "$0")"
ARM=${ARM:?}; TASKS=${TASKS:-tasks.txt}; ATTEMPTS=${ATTEMPTS:-1}; CONC=${CONC:-3}; WINDOW=${WINDOW:-65536}; JOBS=${JOBS:-$PWD/jobs}
MODEL=${MODEL:-deepseek/deepseek-v4.1-flash-1017}
: "${OPENAI_BASE_URL:?}" "${OPENAI_API_KEY:?}"
inc=(); while read -r t; do [[ -z $t || $t == \#* ]] || inc+=(-i "*/$t"); done < "$TASKS"
g=false; [ "$ARM" = gnosh ] && g=true
PYTHONPATH="$PWD" harbor run -d frontier-harness-eval/frontier-harness-eval@v1.1.0 -e docker -m "openai/$MODEL" \
  -k "$ATTEMPTS" -n "$CONC" -r 2 --retry-exclude AgentTimeoutError --retry-exclude VerifierTimeoutError "${inc[@]}" \
  -o "$JOBS" --job-name "${JOBNAME:-pi-$ARM-$(date +%m%d-%H%M)}" \
  -a pi_harbor.agent:PiArm --ak version=1.6.0 --ak model_api=openai-completions --ak reasoning_effort=max \
  --ak gnosh=$g --ak context_window="$WINDOW" --ak reserve_tokens=16384 --ak keep_recent_tokens=20000
# Resume after an interruption (finished trials are kept): PYTHONPATH=$PWD harbor jobs resume -p "$JOBS/<jobname>"
# Table: python3 analyze.py "$JOBS"/<vanilla job> "$JOBS"/<gnosh job>
