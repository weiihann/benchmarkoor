#!/usr/bin/env bash
# Tears down one verification run: the controller it started, the analyzer
# containers of its attempts, and its scratch copy. Evidence is kept.
# Usage: cleanup.sh <run-id>
set -uo pipefail
root="$(git -C "$(dirname -- "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
run_id="${1:?run id (verify-...) required}"
[[ "$run_id" =~ ^verify-[0-9]{8}T[0-9]{6}Z$ ]] || { echo "invalid run id: $run_id" >&2; exit 2; }
scratch="/tmp/benchmarkoor-verify/$run_id"
evidence="$root/results/verify/$run_id"

if [[ -f "$scratch/controller.pid" ]]; then
    pid="$(cat "$scratch/controller.pid")"
    if kill -0 "$pid" 2>/dev/null && grep -q benchmarkoor "/proc/$pid/cmdline" 2>/dev/null; then
        kill -TERM "$pid"
        for _ in $(seq 1 30); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
        printf 'stopped controller %s\n' "$pid"
    fi
fi

for attempt in "$scratch"/run/analysis/*/; do
    [[ -d "$attempt" ]] || continue
    name="benchmarkoor-compute-analyze-$(basename "$attempt")"
    if docker inspect "$name" >/dev/null 2>&1; then
        docker rm -f "$name" >/dev/null && printf 'removed container %s\n' "$name"
    fi
done

if [[ -d "$scratch" ]]; then
    rm -r -- "$scratch"
    printf 'removed scratch %s\n' "$scratch"
fi

if [[ -f "$evidence/summary.json" ]]; then
    printf 'evidence kept: %s\n' "$evidence"
else
    printf 'no evidence at %s\n' "$evidence"
fi
