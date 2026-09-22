#!/usr/bin/env bash
# Run a finite, real Osaka evm2 compute pipeline control campaign.
#
# This script deliberately creates twenty distinct workload cases in the one
# diagnostic worker session: ADD plus four KECCAK256 input shapes, each at four
# fixed target-count points. It detects prepared-case lifetime regressions
# without substituting synthetic timings.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
evm2_root="${repo_root}/../evm2"
execution_specs_root="${repo_root}/../execution-specs"
gasfit_root="${repo_root}/../evm-gasfit"
results_dir="${COMPUTE_SMOKE_RESULTS_DIR:-${repo_root}/results/compute-smoke-$(date -u +%Y%m%dT%H%M%SZ)}"
binary="${BENCHMARKOOR_BIN:-${repo_root}/bin/benchmarkoor}"

for required_dir in "${evm2_root}" "${execution_specs_root}" "${gasfit_root}"; do
    if [[ ! -d "${required_dir}" ]]; then
        printf 'compute smoke requires sibling checkout: %s\n' "${required_dir}" >&2
        exit 1
    fi
done

for command in docker make python3; do
    if ! command -v "${command}" >/dev/null 2>&1; then
        printf 'compute smoke requires %s on PATH\n' "${command}" >&2
        exit 1
    fi
done

mkdir -p "${results_dir}"
analysis_config="${results_dir}/smoke-gasfit.yaml"
campaign_config="${results_dir}/smoke-compute.yaml"

cat >"${analysis_config}" <<'YAML'
version: 1
clients: [evm2]
gas_costs:
  fork: osaka
output:
  plots: false
modeling:
  bootstrap_iterations: 20
  random_seed: 20260921
glue_adjustment:
  enabled: false
qualification:
  confidence_level: 0.95
  max_condition_number: 1.0e8
  max_residual_curvature_r2: 1.0
  max_relative_uncertainty: 100.0
  max_holdout_error: 100.0
  min_sessions: 2
  enforce_fit_quality: false
  block_unqualified: true
campaign:
  eligible_phases: [qualification]
  eligible_statuses: [executed]
  require_correctness_passed: true
models:
  custom:
    - test_name: test_add_workload_witness
      target_operation: ADD
      overhead_baseline_param: overhead_baseline
      overhead_baseline_match_params: []
      model_params:
        target_coef: OPCODE_ADD
    - test_name: test_keccak_workload_witness
      target_operation: KECCAK256
      overhead_baseline_param: overhead_baseline
      overhead_baseline_match_params: [input_length]
      fixture_params:
        input_words:
          source: input_length
          transform: bytes_to_words
      model_params:
        target_coef: OPCODE_KECCAK256_BASE
        input_words: OPCODE_KECCAK256_PER_WORD
YAML

cat >"${campaign_config}" <<YAML
compute:
  id: osaka-add-keccak-smoke
  results_dir: ${results_dir}
  container_runtime: docker
  worker_image: benchmarkoor-compute-worker:smoke
  generator:
    image: benchmarkoor-compute-generator:smoke
    tests:
      - tests/benchmark/compute/instruction/test_arithmetic.py
      - tests/benchmark/compute/instruction/test_keccak.py
    filter: test_add_workload_witness or test_keccak_workload_witness
    fixed_opcode_count: [0, 0.25, 0.5, 1]
    families: [arithmetic, keccak]
    tx_gas_cap: 15000000
  analyzer:
    image: benchmarkoor-compute-analyzer:smoke
    config: ${analysis_config}
  seed: 20260921
  sessions: 2
  pilot_repetitions: 1
  warmup_repetitions: 1
  repetitions: 2
  timeout: 20m
  source_paths:
    benchmarkoor: ${repo_root}
    evm2: ${evm2_root}
    execution_specs: ${execution_specs_root}
    evm_gasfit: ${gasfit_root}
YAML

printf 'Building pinned local compute images.\n'
docker build -f "${repo_root}/Dockerfile.compute-worker" -t benchmarkoor-compute-worker:smoke "${evm2_root}"
docker build --build-context execution-specs="${execution_specs_root}" -f "${repo_root}/Dockerfile.compute-generator" -t benchmarkoor-compute-generator:smoke "${repo_root}"
docker build --build-context evm-gasfit="${gasfit_root}" -f "${repo_root}/Dockerfile.compute-analyzer" -t benchmarkoor-compute-analyzer:smoke "${repo_root}"

if [[ ! -x "${binary}" ]]; then
    make -C "${repo_root}" build-core
fi

"${binary}" run --config "${campaign_config}"

python3 - "${results_dir}" <<'PY'
import json
import sys
from pathlib import Path

results_root = Path(sys.argv[1])
runs = sorted(path for path in (results_root / "runs").iterdir() if path.is_dir())
if len(runs) != 1:
    raise SystemExit(f"expected one smoke run under {results_root / 'runs'}, found {len(runs)}")
run_dir = runs[0]
workload = json.loads((run_dir / "workload.json").read_text())
cases = workload.get("cases", [])
if len(cases) != 20:
    raise SystemExit(f"expected 20 distinct ADD/KECCAK256 smoke cases, found {len(cases)}")
operations = {case["id"]: case["target_operation"] for case in cases}
if set(operations.values()) != {"ADD", "KECCAK256"}:
    raise SystemExit(f"unexpected smoke operations: {sorted(set(operations.values()))}")

samples = [json.loads(line) for line in (run_dir / "samples.jsonl").read_text().splitlines()]
diagnostic = [
    sample
    for sample in samples
    if sample["phase"] == "diagnostic"
    and sample["status"] == "executed"
    and sample["correctness_passed"]
]
if {sample["case_id"] for sample in diagnostic} != set(operations):
    raise SystemExit("every smoke case must complete a correct diagnostic execution")
for sample in diagnostic:
    target = operations[sample["case_id"]]
    counts = sample.get("opcode_counts") or {}
    if sample.get("target_count") != counts.get(target):
        raise SystemExit(
            f"diagnostic target_count disagrees with {target} counter for {sample['case_id']}"
        )

performance = [
    sample
    for sample in samples
    if sample["phase"] in {"pilot", "warmup", "qualification"}
]
if not performance or not all(
    sample["status"] == "executed"
    and sample["correctness_passed"]
    and isinstance(sample.get("execution_duration_ns"), int)
    and sample["execution_duration_ns"] > 0
    for sample in performance
):
    raise SystemExit("performance samples must contain correct, measured evm2 executions")

summary = json.loads((run_dir / "config.json").read_text())
analysis = summary["compute"]["analysis"]
if analysis["status"] not in {"succeeded", "inconclusive"} or not analysis.get("attempt_id"):
    raise SystemExit(f"evm-gasfit analysis did not complete: {analysis}")
attempt_dir = run_dir / "analysis" / analysis["attempt_id"]
for path in (attempt_dir / "runtimes.csv", attempt_dir / "opcounts.json", attempt_dir / "reports" / "analysis_status.json"):
    if not path.is_file():
        raise SystemExit(f"missing real analysis artifact: {path}")

print(f"compute smoke passed: {run_dir}")
PY
