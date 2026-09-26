#!/usr/bin/env bash
# Times a fixed workload of NewL1 blocks in a capture-only compute campaign (no
# analysis) and writes per-case block times. Session layout and seed match the
# pricing campaigns, so the timings compare directly with theirs.
#
# Rerunning after a finished capture only rewrites the block-time table. An
# interrupted capture starts over in a new runs/compute-* directory.
#
# Required environment:
#   RUN_HOME  run directory (created if missing)
#   WORKLOAD  workload JSON to time, e.g. one budget from extract_block_corpus.py
#   WORK      local sha256 image ID of the NewL1 worker
# Optional environment:
#   MEMORY    memory limit per worker container (default 24g)
set -euo pipefail

: "${RUN_HOME:?}" "${WORKLOAD:?}" "${WORK:?}"
MEMORY=${MEMORY:-24g}

B=$(git -C "$(dirname -- "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
PIN=$(git -C "$B" rev-parse HEAD)
CTL=$B/bin/benchmarkoor
. "$B/scripts/compute/host_facts.sh"
mkdir -p "$RUN_HOME"
RUN_HOME=$(cd "$RUN_HOME" && pwd)

stamp() { echo "=== $(date -u +%FT%TZ) $*"; }
fail() { stamp "FAILED: $*"; exit 1; }

stamp preflight
[[ -z "$(git -C "$B" status --porcelain --untracked-files=no)" ]] || fail "benchmarkoor checkout has local changes"
built=$([[ -x "$CTL" ]] && "$CTL" version | awk '/commit:/ {print $2}' || true)
[[ -n "$built" && "$PIN" == "$built"* ]] || fail "bin/benchmarkoor is missing or not built from $PIN; run make build-core"
docker image inspect "$WORK" >/dev/null || fail "worker image $WORK is not loaded"
[[ -f "$WORKLOAD" ]] || fail "workload $WORKLOAD not found"

if [[ -f "$RUN_HOME/workload.json" ]]; then
  cmp -s "$WORKLOAD" "$RUN_HOME/workload.json" || fail "WORKLOAD differs from the one this run home started with"
else
  cp "$WORKLOAD" "$RUN_HOME/workload.json"
fi

if [[ ! -f "$RUN_HOME/host/facts.done" ]]; then
  record_host_facts "$RUN_HOME/host"
  {
    echo "benchmarkoor $PIN"
    echo "worker $WORK"
    echo "workload_sha256 $(sha256sum <"$RUN_HOME/workload.json" | cut -d' ' -f1)"
    echo "memory $MEMORY"
  } >"$RUN_HOME/host/provenance.txt"
  touch "$RUN_HOME/host/facts.done"
fi

[[ -f "$RUN_HOME/compute.yaml" ]] || cat >"$RUN_HOME/compute.yaml" <<EOF
compute:
  id: $(basename "$RUN_HOME")
  engine: newl1
  workload: $RUN_HOME/workload.json
  results_dir: $RUN_HOME
  container_runtime: docker
  worker_image: $WORK
  seed: 20260922
  sessions: 8
  pilot_repetitions: 1
  warmup_repetitions: 1
  repetitions: 5
  timeout: 8h
  resource_limits:
    memory: $MEMORY
    swap_disabled: true
  source_paths:
    benchmarkoor: $B
EOF

if [[ ! -f "$RUN_HOME/capture.done" ]]; then
  stamp "capture $(python3 -c 'import json, sys; print(len(json.load(open(sys.argv[1]))["cases"]))' "$RUN_HOME/workload.json") cases"
  status=0
  "$CTL" run --config "$RUN_HOME/compute.yaml" 2>&1 | tee "$RUN_HOME/capture.log" || status=$?
  RUN=$(grep -o 'run_dir=[^ ]*' "$RUN_HOME/capture.log" | tail -1 | cut -d= -f2 || true)
  [[ -n "$RUN" && -f "$RUN/samples.jsonl" ]] || fail "capture left no samples; see $RUN_HOME/capture.log"
  if [[ $status -ne 0 ]]; then
    python3 "$B/scripts/compute/campaign_block_times.py" "$RUN" "$RUN_HOME/block-times.partial.csv" || true
    fail "controller exited $status; samples kept in $RUN, see $RUN_HOME/capture.log"
  fi
  echo "$RUN" >"$RUN_HOME/capture.done"
fi
RUN=$(cat "$RUN_HOME/capture.done")

stamp "block times"
python3 "$B/scripts/compute/campaign_block_times.py" "$RUN" "$RUN_HOME/block-times.csv"
stamp "complete: $RUN"
