#!/usr/bin/env bash
# Runs the NewL1 Osaka gas-budget compute campaign end to end in one run home.
# Rerunning the same command resumes: finished stages are verified and skipped,
# and nothing that already completed is timed again.
#
# Required environment:
#   RUN_HOME              run directory (created if missing)
#   GEN, WORK, ANA        local sha256 image IDs: generator, NewL1 worker, analyzer
#   NEWL1_ROOT            bnbchain-newL1 checkout the worker was built from
#   EXECUTION_SPECS_ROOT  execution-specs checkout the generator was built from
# Optional environment:
#   BUDGETS  block gas budgets in Mgas (default 120,180,240,300,360; glue
#            adjustment needs at least 5)
#   SELECT   pytest -k expression for the target variants (default: EIP-7904 set)
#   JOBS     parallel generation jobs, 1..16 (default 16)
#   MEMORY   memory limit per worker container (default 24g)
set -euo pipefail

: "${RUN_HOME:?}" "${GEN:?}" "${WORK:?}" "${ANA:?}" "${NEWL1_ROOT:?}" "${EXECUTION_SPECS_ROOT:?}"
BUDGETS=${BUDGETS:-120,180,240,300,360}
SELECT=${SELECT:-'(test_arithmetic and (opcode_DIV- or opcode_SDIV-)) or mod_bits or test_keccak_diff_mem_msg_sizes or test_ecrecover or test_blake2f_benchmark or test_blake2f_uncachable or (test_alt_bn128 and (bn128_add or bn128_double)) or (test_alt_bn128_uncachable and ec_add) or test_alt_bn128_benchmark or test_ec_pairing or (test_bls12_381 and (bls12_g1add or bls12_g2add) and not uncachable) or (test_p256verify and not modular_comp and not wrong_endianness) or test_point_evaluation'}
JOBS=${JOBS:-16}
MEMORY=${MEMORY:-24g}

B=$(git -C "$(dirname -- "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
PIN=$(git -C "$B" rev-parse HEAD)
S=$B/scripts/compute/pricing_campaign.py
CTL=$B/bin/benchmarkoor
. "$B/scripts/compute/host_facts.sh"
mkdir -p "$RUN_HOME"
RUN_HOME=$(cd "$RUN_HOME" && pwd)
COMMON=(--run-home "$RUN_HOME" --generator-image "$GEN" --worker-image "$WORK" --engine newl1
        --workload-mode gas_budget --gas-budgets "$BUDGETS" --select "$SELECT" --fixture-format engine)

stamp() { echo "=== $(date -u +%FT%TZ) $*"; }
fail() { stamp "FAILED: $*"; exit 1; }
pinned() {
  [[ "$(git -C "$B" rev-parse HEAD)" == "$PIN" && -z "$(git -C "$B" status --porcelain --untracked-files=no)" ]] \
    || fail "benchmarkoor checkout moved or has local changes; the operator script archives itself, so stop"
}

stamp "preflight"
built=$([[ -x "$CTL" ]] && "$CTL" version | awk '/commit:/ {print $2}' || true)
[[ -n "$built" && "$PIN" == "$built"* ]] || fail "bin/benchmarkoor is missing or not built from $PIN; run make build-core"
for image in "$GEN" "$WORK" "$ANA"; do docker image inspect "$image" >/dev/null || fail "image $image is not loaded"; done
for dir in "$NEWL1_ROOT" "$EXECUTION_SPECS_ROOT"; do git -C "$dir" rev-parse HEAD >/dev/null || fail "$dir is not a git checkout"; done

if [[ ! -f "$RUN_HOME/host/facts.done" ]]; then
  record_host_facts "$RUN_HOME/host"
  {
    echo "benchmarkoor $PIN"
    echo "newl1 $(git -C "$NEWL1_ROOT" rev-parse HEAD)"
    echo "execution-specs $(git -C "$EXECUTION_SPECS_ROOT" rev-parse HEAD)"
    echo "generator $GEN"; echo "worker $WORK"; echo "analyzer $ANA"
    echo "budgets $BUDGETS"; echo "select $SELECT"
  } >"$RUN_HOME/host/provenance.txt"
  touch "$RUN_HOME/host/facts.done"
fi

cd "$B"
for stage in inventory grids calibration assemble; do
  pinned
  stamp "stage $stage"
  python3 "$S" "$stage" "${COMMON[@]}" --generation-jobs "$JOBS" --continue
done

stamp create-config
[[ -f "$RUN_HOME/analysis-gasfit.yaml" ]] || docker run --rm --network none --user "$(id -u):$(id -g)" \
  -e MPLCONFIGDIR=/tmp -v "$RUN_HOME:/work" --entrypoint python "$ANA" -m evm_gasfit.recommendations \
  create-config --client newl1 --workload /work/corpus/workload.json --out /work/analysis-gasfit.yaml

pinned
stamp "stage config"
python3 "$S" config "${COMMON[@]}" --analyzer-image "$ANA" --controller "$CTL" \
  --benchmarkoor-root "$B" --newl1-root "$NEWL1_ROOT" --execution-specs-root "$EXECUTION_SPECS_ROOT" \
  --memory "$MEMORY" --continue

if [[ ! -f "$RUN_HOME/smoke.done" ]]; then
  pinned
  stamp "smoke capture"
  "$CTL" run --config "$RUN_HOME/config/smoke-compute.yaml" >"$RUN_HOME/smoke.log" 2>&1 \
    || fail "smoke capture or its analysis failed; see $RUN_HOME/smoke.log"
  SMOKE_RUN=$(grep -o 'run_dir=[^ ]*' "$RUN_HOME/smoke.log" | tail -1 | cut -d= -f2 || true)
  python3 - "$SMOKE_RUN" <<'PY' || fail "smoke glue detection check failed"
import csv, glob, sys
attempt = sorted(glob.glob(sys.argv[1] + "/analysis/*/reports/glue_detection_coverage.csv"))[-1]
rows = list(csv.DictReader(open(attempt)))
short = [r["test_name"] for r in rows if r["detection_status"] == "insufficient_fixture_points"]
print(f"smoke glue detection: {len(rows)} models, {len(short)} with too few block sizes")
sys.exit(1 if short else 0)
PY
  echo "$SMOKE_RUN" >"$RUN_HOME/smoke.done"
fi

if [[ ! -f "$RUN_HOME/full-capture.done" ]]; then
  pinned
  stamp "full capture"
  "$CTL" run --config "$RUN_HOME/config/compute.yaml" 2>&1 | tee "$RUN_HOME/full-capture.log" \
    || stamp "controller exited non-zero; checking whether the capture was retained"
  RUN=$(grep -o 'run_dir=[^ ]*' "$RUN_HOME/full-capture.log" | tail -1 | cut -d= -f2 || true)
  [[ -n "$RUN" && -f "$RUN/samples.jsonl" ]] || fail "full capture left no run directory"
  echo "$RUN" >"$RUN_HOME/full-capture.done"
fi
RUN=$(cat "$RUN_HOME/full-capture.done")
ATT=$(ls -td "$RUN"/analysis/*/ | head -1)

stamp "recommendations $RUN $ATT"
docker run --rm --network none --user "$(id -u):$(id -g)" -e MPLCONFIGDIR=/tmp -v "$RUN_HOME:/work" --entrypoint python "$ANA" \
  -m evm_gasfit.recommendations build --workload /work/corpus/workload.json \
  --analysis "/work/${ATT#"$RUN_HOME"/}reports" --diagnostics "/work/${RUN#"$RUN_HOME"/}/sessions/diagnostic-00/samples.jsonl" \
  --out /work/recommendations

stamp "audit"
python3 "$S" audit "${COMMON[@]}" --run "$RUN" --recommendations "$RUN_HOME/recommendations"

stamp "block times"
python3 "$B/scripts/compute/campaign_block_times.py" "$RUN" "$RUN_HOME/block-times.csv"

stamp "campaign complete: $RUN"
