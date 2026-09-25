#!/usr/bin/env bash
# Reanalyzes a copy of an archived compute run with this checkout's controller.
# The source run is never written; evidence lands in results/verify/<run-id>/.
# Usage: reanalyze.sh <archived-run-dir> [analysis-config.yaml]
set -euo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root="$(git -C "$here" rev-parse --show-toplevel)"
source_run="$(realpath "${1:?archived compute run directory required}")"
override="${2:-}"
[[ -f "$source_run/campaign.json" ]] || { echo "not a compute run: $source_run" >&2; exit 2; }

"$here/doctor.sh"

run_id="verify-$(date -u +%Y%m%dT%H%M%SZ)"
scratch="/tmp/benchmarkoor-verify/$run_id"
evidence="$root/results/verify/$run_id"
mkdir -p "$scratch" "$evidence"
rsync -a --exclude analysis/ "$source_run/" "$scratch/run/"
printf '{"run_id": "%s", "source_run": "%s", "scratch": "%s", "evidence": "%s"}\n' \
    "$run_id" "$source_run" "$scratch" "$evidence" > "$evidence/state.json"

args=(analyze --run "$scratch/run")
[[ -n "$override" ]] && args+=(--analysis-config "$(realpath "$override")")
printf '%q ' "$root/bin/benchmarkoor" "${args[@]}" > "$evidence/command.txt"

set +e
"$root/bin/benchmarkoor" "${args[@]}" > "$evidence/controller.log" 2>&1 &
echo $! > "$scratch/controller.pid"
wait "$(cat "$scratch/controller.pid")"
echo $? > "$evidence/controller.exit"
set -e

attempt="$(ls -td "$scratch"/run/analysis/*/ 2>/dev/null | head -1)"
[[ -n "$attempt" ]] || { echo "no analysis attempt created; see $evidence/controller.log" >&2; exit 1; }
cp -r "$attempt" "$evidence/attempt"
python3 - "$evidence" "$(basename "$attempt")" <<'PY'
import csv, json, sys
from pathlib import Path
evidence, attempt_id = Path(sys.argv[1]), sys.argv[2]
attempt = evidence / "attempt"
status = json.loads((attempt / "status.json").read_text())
qualification = attempt / "reports" / "qualification.csv"
models = list(csv.DictReader(qualification.open())) if qualification.exists() else []
reports = attempt / "reports"
summary = {
    "controller_exit": int((evidence / "controller.exit").read_text()),
    "attempt_id": attempt_id,
    "analysis_status": status.get("status"),
    "models": [{k: row.get(k) for k in ("test_name", "client_name", "status", "reasons")} for row in models],
    "reports": sorted(p.name for p in reports.iterdir()) if reports.is_dir() else [],
}
(evidence / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
print(json.dumps(summary, indent=2))
PY
printf 'evidence: %s\ncleanup: %s %s\n' "$evidence" "$here/cleanup.sh" "$run_id"
