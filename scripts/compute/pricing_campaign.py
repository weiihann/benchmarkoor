#!/usr/bin/env python3
"""Staged operator CLI for the Osaka compute pricing campaign at 600M gas/s.

One experiment home holds every stage output. Stages run once, in order, and
every command, log, artifact hash, and terminal status is retained. A nonzero
stage exit preserves the partial directory and must be investigated. Generation
stages accept an explicit, fully audited --continue that preserves failed
attempts in renamed directories and only fills missing shards; measurements are
never resumed or retried implicitly.

Stages:
  inventory    Fresh one-count target inventory across the unchanged source
               allowlist plus a worker diagnostic (variant and gas evidence).
  grids        Target count-grid shards derived from that fresh gas evidence,
               generated in bounded parallel jobs before any timed capture.
  calibration  Explicit calibration-only supporting-glue export from the same
               generator identity, plus its own worker preflight.
  assemble     Mixed target+calibration union workload, lane validation, and a
               complete diagnostic preflight; freezes expected accounting.
  config       Frozen 8-session campaign config plus a glue-enabled 4-session
               mixed-lane smoke config with the full pricing contract (anchor
               600M, zero margin, 1000 bootstrap, strict gates); validates the
               analysis-config contract verbatim.
  audit        Read-only integrity audit of a finished run: exact sample
               accounting per lane, raw qualification completeness, hash, gas
               and semantic-count checks, exit/OOM records, model decisions,
               and calibration-excluded-from-pricing evidence.

The target source allowlist, families, transaction gas cap, and protocol v2
contract are fixed across campaigns. Calibration cases never widen the
target inventory, never gain pricing models, and carry the string parameter
campaign_role so the exported runtimes CSV exposes param_campaign_role for
both lanes.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import queue
import threading
import re
import shlex
import subprocess
import sys
import traceback
from collections import Counter, defaultdict
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from typing import Any

GAS_CAP = 16_777_216
SCREEN_GAS = 10_000_000
EXPECTED_VARIANTS = 437
SEED = 20260922
FAMILIES = ("arithmetic", "bitwise", "comparison", "stack", "control_flow", "keccak", "precompile")
INSTRUCTIONS = [f"tests/benchmark/compute/instruction/test_{family}.py" for family in FAMILIES[:-1]]
PRECOMPILES = "tests/benchmark/compute/precompile"
ORDINARY_GRID = [250, 500, 1000, 2000, 4000]
KECCAK_GRID = [125, 250, 500, 750, 1000]
SPECIAL_TEST = "test_keccak_max_permutations"
CALIBRATION_MODULE = "tests/benchmark/compute/calibration/test_glue.py"
MANDATORY_DRIVERS = ("test_calldatasize", "test_memory_access", "test_ext_account_query_warm")
CALIBRATION_ORDINARY_GRID = [250, 500, 1000, 2000, 4000]
CALIBRATION_CALL_GRID = [250, 500, 1000, 1500, 2000]
CALIBRATION_PUSH32_GRID = [125, 250, 500, 700]
CALIBRATION_POP_GRID = [32, 64, 128, 256, 512]
EXPECTED_CALIBRATION_TESTS = (
    "test_calldatasize", "test_memory_access", "test_ext_account_query_warm",
    "test_calldatacopy_from_origin", "test_calldataload", "test_pop",
    "test_call_warm", "test_iszero_straight", "test_jumpdests_straight",
    "test_swap_straight", "test_push_straight", "test_dup_straight",
    "test_gas_op_straight",
)
EXPECTED_CALIBRATION_VARIANTS = 19
# Terminal work-size token of a case ID: fixed-count exports end in
# ``-opcount_<thousands>K]``, gas-budget (EIP-7904 block layout) exports in
# ``-benchmark-gas-value_<Mgas>M]``. A point's count is in the case's own unit.
WORK_SUFFIX = re.compile(
    r"-(?:opcount_(?P<count>[0-9]+(?:\.[0-9]+)?)K|benchmark-gas-value_(?P<budget>[0-9]+)M)\]$")
WORKLOAD_MODES = ("fixed_count", "gas_budget")
# EIP-7904 swept block gas budgets of 100-300 Mgas in 20 Mgas steps.
DEFAULT_GAS_BUDGETS = list(range(100, 301, 20))
GAS_BUDGET_UNIT = 1_000_000
# Target opcodes the Osaka pre-block system calls execute once per block: the
# EIP-4788 beacon-roots and EIP-2935 history contracts each take one MOD for
# their ring-buffer index. Fill counts include them; worker counts do not.
SYSTEM_CALL_OPCODES = {"MOD": 2}
IMAGE_ID = re.compile(r"sha256:[0-9a-f]{64}\Z")
CPU_LIST = re.compile(r"[0-9]+(?:-[0-9]+)?(?:,[0-9]+(?:-[0-9]+)?)*\Z")
MEMORY_SPEC = re.compile(r"[1-9][0-9]*(?:[bkmgBKMG])?\Z")
HASH64 = re.compile(r"(?:0x)?[0-9a-fA-F]{64}\Z")
BOUNDARIES = {"newl1": "newl1_block_execution"}
ROLES = ("target", "calibration")
SESSIONS = 8
PILOT_REPS = 1
WARMUP_REPS = 1
QUAL_REPS = 5
RECORDS_PER_READY_CASE = 1 + SESSIONS * (PILOT_REPS + WARMUP_REPS + QUAL_REPS)
QUALIFICATIONS_PER_READY_CASE = SESSIONS * QUAL_REPS
ANCHOR_RATE = 600_000_000
SMOKE_SESSIONS = 4
SMOKE_QUAL_REPS = 2
SMOKE_RECORDS_PER_READY_CASE = 1 + SMOKE_SESSIONS * (1 + 1 + SMOKE_QUAL_REPS)
# Target-corpus driver tests the calibration lane does not supply; the smoke
# subset keeps one variant of each so the enabled glue fit sees every driver
# available to the full campaign (RETURNDATASIZE/SELFBALANCE have no driver
# anywhere and are expected to surface as blocked coverage downstream).
CORPUS_DRIVER_TESTS = (
    "test_iszero", "test_jumpdests", "test_swap", "test_dup", "test_gas_op",
    "test_push", "test_arithmetic", "test_bitwise", "test_comparison",
    "test_jumpi_fallthrough", "test_pc_op", "test_jump_benchmark",
    "test_keccak_diff_mem_msg_sizes",
)
KNOWN_UNSUPPORTED = {"test_clz_diff", "test_p256verify_uncachable",
                     "mod_even_1024b_exp_1024", "mod_odd_1024b_exp_1024"}
STRICT_GATES = {
    "confidence_level": 0.95, "max_condition_number": 1e8,
    "max_residual_curvature_r2": 0.1, "max_relative_uncertainty": 0.5,
    "max_holdout_error": 0.25, "min_sessions": 4,
    "enforce_fit_quality": True, "block_unqualified": True,
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def load(path: Path) -> Any:
    with path.open(encoding="utf-8") as stream:
        return json.load(stream)


def save(path: Path, value: Any) -> None:
    with path.open("x", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True, allow_nan=False)
        stream.write("\n")


def save_or_verify(path: Path, value: Any) -> None:
    """Write once; on an audited continuation the prior plan must be identical."""
    if path.is_file():
        require(load(path) == value, f"Stage plan differs from the existing {path}")
        return
    save(path, value)


def save_replace(path: Path, value: Any) -> None:
    with path.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True, allow_nan=False)
        stream.write("\n")


def append_record(path: Path, value: Any) -> None:
    with path.open("a", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True, allow_nan=False)
        stream.write("\n")


def digest(path: Path) -> str:
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(chunk)
    return result.hexdigest()


def artifact(path: Path, home: Path) -> dict[str, Any]:
    return {"path": str(path.relative_to(home)), "sha256": digest(path), "bytes": path.stat().st_size}


def identity(case_id: str) -> tuple[str, int]:
    match = WORK_SUFFIX.search(case_id)
    require(match is not None, f"Case ID lacks a work-size suffix: {case_id}")
    if match.group("budget") is not None:
        return case_id[:match.start()] + "]", int(match.group("budget"))
    count = Decimal(match.group("count")) * 1000
    require(count == count.to_integral_value(), f"Fractional count: {case_id}")
    return case_id[:match.start()] + "]", int(count)


def case_identity(case: dict[str, Any]) -> tuple[str, int]:
    return identity(case["id"])


def test_name(case_id: str) -> str:
    return case_id.split("::")[-1].split("[", 1)[0]


def case_role(case: dict[str, Any]) -> str:
    return case.get("parameters", {}).get("campaign_role")


def require_role(case: dict[str, Any], expected: str) -> None:
    role = case_role(case)
    require(role == expected,
            f"Case is not campaign_role={expected}: {case['id']} ({role!r})")


def identity_settings(args: argparse.Namespace) -> dict[str, Any]:
    """Settings that define corpus identity and must never drift between stages."""
    return {
        "generator_image": args.generator_image,
        "worker_image": args.worker_image,
        "seed": SEED,
        "tx_gas_cap": GAS_CAP,
        "families": list(FAMILIES),
        "calibration_module": CALIBRATION_MODULE,
        "fixture_format": args.fixture_format,
        "engine": args.engine,
        "workload_mode": args.workload_mode,
        "gas_budgets": args.gas_budgets if args.workload_mode == "gas_budget" else None,
        "select": args.select,
    }


def settings(args: argparse.Namespace) -> dict[str, Any]:
    return {**identity_settings(args),
            "generation_cpuset": args.generation_cpuset,
            "generation_memory": args.generation_memory,
            "generation_jobs": args.generation_jobs,
            "network": "none"}


# ---------------------------------------------------------------- commands ---


def start_command(directory: Path, label: str, argv: list[str]) -> tuple[Path, subprocess.Popen]:
    save(directory / f"{label}.command.json", {"argv": argv, "started": now()})
    with (directory / f"{label}.command.txt").open("x", encoding="utf-8") as stream:
        stream.write(shlex.join(argv) + "\n")
    log_path = directory / f"{label}.log"
    print(f"{directory.name}: {label} started (log: {log_path})", flush=True)
    return log_path, subprocess.Popen(
        argv, stdout=log_path.open("xb"), stderr=subprocess.STDOUT, start_new_session=True)


def finish_command(directory: Path, label: str, started: tuple[Path, subprocess.Popen]) -> int:
    _log_path, process = started
    try:
        returncode = process.wait()
    except BaseException as error:
        process.kill()
        process.wait()
        save(directory / f"{label}.result.json", {"finished": now(), "error": repr(error)})
        raise
    save(directory / f"{label}.result.json", {"finished": now(), "returncode": returncode})
    return returncode


def command(directory: Path, label: str, argv: list[str]) -> None:
    """Run once to completion, keeping argv, output, and terminal status.

    On interruption the named container is killed best-effort; killing the
    docker CLI alone would leave the container running.
    """
    try:
        returncode = finish_command(directory, label, start_command(directory, label, argv))
    except BaseException:
        kill_container(container_name(directory, label))
        raise
    require(returncode == 0,
            f"{label} exited {returncode}; inspect {directory / (label + '.log')}. "
            "Nothing was retried or excluded.")


def merged_selection(selection: list[str], format_term: str) -> list[str]:
    """Combine a job's -k expression with the fixture-format term.

    pytest honors only the last -k, so two separate flags would silently drop
    the job's own selection. One parenthesized conjunction is used instead.
    """
    if "-k" in selection:
        index = selection.index("-k")
        return (selection[:index]
                + ["-k", f"({selection[index + 1]}) and {format_term}"]
                + selection[index + 2:])
    return selection + ["-k", format_term]


def container_name(directory: Path, label: str) -> str:
    return f"pricing-campaign-{os.getpid()}-{directory.name}-{label}"


def kill_container(name: str) -> None:
    """Best-effort container stop; killing the docker CLI alone is not enough."""
    subprocess.run(["docker", "kill", name], check=False, capture_output=True)


def docker_generation(args: argparse.Namespace, directory: Path, mount: str, image: str,
                      label: str) -> list[str]:
    return [
        "docker", "run", "--rm", "--network=none", "--pull=never",
        f"--name={container_name(directory, label)}", *generation_cpuset_flag(args),
        f"--memory={args.generation_memory}",
        f"--memory-swap={args.generation_memory}", "--user", f"{os.getuid()}:{os.getgid()}",
        "--env", "PYTHONDONTWRITEBYTECODE=1", "--env", f"HOME={mount}/home",
        "--volume", f"{directory}:{mount}", image,
    ]


def generation_cpuset_flag(args: argparse.Namespace) -> list[str]:
    """Docker's CPU pin for generation; unpinned unless --generation-cpuset is set."""
    return [f"--cpuset-cpus={args.generation_cpuset}"] if args.generation_cpuset else []


# ------------------------------------------------------------- generation ---


def generation_argv(args: argparse.Namespace, directory: Path, counts: list[int],
                    modules: list[str], selection: list[str], role: str,
                    families: tuple[str, ...], gas_budgets: bool = False,
                    workers: int = 1) -> list[str]:
    """Fill argv; ``gas_budgets`` reads ``counts`` as block budgets in Mgas.

    ``workers`` spreads one fill over xdist processes; the exporter drops the
    xdist group tag, so case identities do not depend on it.
    """
    work_argument = (
        "--gas-benchmark-values=" + ",".join(str(count) for count in counts) if gas_budgets
        else "--fixed-opcode-count="
        + ",".join(format(Decimal(count) / 1000, "f") for count in counts))
    argv = docker_generation(args, directory, "/out", args.generator_image, "generator") + [
        "--fork", "Osaka", "--benchmark-workload-export=/out/workload.json",
        f"--benchmark-workload-seed={SEED}",
        f"--benchmark-workload-revision={args.generator_image}",
        f"--benchmark-workload-tx-gas-cap={GAS_CAP}",
        f"--benchmark-workload-role={role}",
        "--benchmark-workload-families=" + ",".join(families),
        work_argument, "--output=/out/fixtures",
        "--no-html", "--skip-index", "--junitxml=/out/junit.xml", "--log-to=/out/logs",
        "-o", "cache_dir=/out/pytest-cache", "-q", "-ra",
        *(["-n", str(workers)] if workers > 1 else []), *selection, *modules]
    return argv


def checked_workload(path: Path, args: argparse.Namespace,
                     expected_role: str = "target") -> dict[str, Any]:
    workload = load(path)
    require(set(workload) == {"schema_version", "fork", "generator", "cases"},
            f"Unexpected workload envelope in {path}")
    require(workload["schema_version"] == 2 and workload["fork"] == "Osaka",
            f"Mixed fork/schema in {path}")
    require(workload["generator"] == {"revision": args.generator_image, "seed": SEED},
            f"Mixed generator provenance in {path}")
    require(bool(workload["cases"]), f"Empty workload: {path}")
    ids: set[str] = set()
    for case in workload["cases"]:
        case_id = case["id"]
        require(case_id not in ids, f"Duplicate case ID in {path}: {case_id}")
        ids.add(case_id)
        parameters = case["parameters"]
        mode = parameters.get("workload_mode")
        require(mode in WORKLOAD_MODES, f"Unknown workload mode: {case_id}")
        # Calibration drivers stay fixed-count single-transaction work; only the
        # target lane follows the campaign's workload mode.
        require(mode == ("fixed_count" if case_role(case) == "calibration"
                         else args.workload_mode), f"Wrong workload mode: {case_id}")
        require(isinstance(parameters.get("source_parameters"), dict),
                f"Missing source parameters: {case_id}")
        require(case["status"] in {"ready", "unsupported"}, f"Unknown status: {case_id}")
        module = case_id.split("::", 1)[0]
        if expected_role == "calibration" or module == CALIBRATION_MODULE:
            check_calibration_case(case, module)
        else:
            check_target_case(case, module)
    return workload


def check_target_case(case: dict[str, Any], module: str) -> None:
    case_id = case["id"]
    family = case["family"]
    allowed = module in INSTRUCTIONS or (module.startswith(PRECOMPILES + "/") and module.endswith(".py"))
    require(allowed and family in FAMILIES, f"Outside source allowlist: {case_id}")
    expected_family = "precompile" if module.startswith(PRECOMPILES + "/") else (
        Path(module).stem.removeprefix("test_"))
    require(family == expected_family, f"Wrong family for source: {case_id}")
    require_role(case, "target")
    if case["status"] == "unsupported":
        require(bool(case.get("reason")), f"Unsupported case lacks reason: {case_id}")
        return
    require(not case.get("reason"), f"Ready case has unsupported reason: {case_id}")
    check_ready_case(case, case_identity(case)[1])


def check_calibration_case(case: dict[str, Any], module: str) -> None:
    case_id = case["id"]
    require(module == CALIBRATION_MODULE, f"Calibration case outside calibration module: {case_id}")
    require_role(case, "calibration")
    require(case["status"] == "ready",
            f"Calibration case is not ready: {case_id}: {case.get('reason')}")
    require(not case.get("reason"), f"Ready calibration case carries a reason: {case_id}")
    require(case["family"] == "calibration",
            f"Calibration case must use the calibration family: {case_id}: {case['family']!r}")
    check_ready_case(case, case_identity(case)[1])


def check_ready_case(case: dict[str, Any], count: int) -> None:
    case_id = case["id"]
    parameters = case["parameters"]
    transactions = case["transactions"]
    if parameters["workload_mode"] == "gas_budget":
        budget = count * GAS_BUDGET_UNIT
        require(parameters.get("gas_budget") == budget,
                f"Budget metadata disagrees with ID: {case_id}")
        # EEST splits the budget at the EIP-7825 cap: full slices, then the
        # remainder. Uncachable variants give each slice its own sender.
        limits = [tx["gas_limit"] for tx in transactions]
        require(sum(limits) == budget and len(limits) == -(-budget // GAS_CAP)
                and all(limit == GAS_CAP for limit in limits[:-1])
                and 0 < limits[-1] <= GAS_CAP
                and parameters.get("tx_count") == len(limits),
                f"Budget is not split at the transaction cap: {case_id}")
    else:
        require(parameters.get("requested_opcode_count") == count,
                f"Count metadata disagrees with ID: {case_id}")
        require(len(transactions) == 1 and parameters.get("tx_count") == 1,
                f"Expected single fixed-work transaction: {case_id}")
        require(all(tx["gas_limit"] == GAS_CAP for tx in transactions),
                f"Inconsistent transaction allowance: {case_id}")
    require(len(case["expected"]["receipts"]) == len(transactions),
            f"Oracle transaction count mismatch: {case_id}")
    require(bool(case["target_operation"]) and case["target_operation"] != "SHA3",
            f"Missing/noncanonical target: {case_id}")


# ------------------------------------------------------------ diagnostics ---


def diagnose(args: argparse.Namespace, directory: Path, workload: dict[str, Any],
             session: str) -> dict[str, Any]:
    request = {
        "schema_version": 2, "session_id": session, "mode": "diagnostic",
        "workload_path": "/campaign/workload.json",
        "samples": [{"sample_id": f"{session}-{index:05d}", "case_id": case["id"],
                     "repetition": 0, "phase": "diagnostic"}
                    for index, case in enumerate(workload["cases"])],
    }
    save(directory / "request.json", request)
    argv = ["docker", "run", "--rm", "--network=none", "--pull=never",
            f"--name={container_name(directory, 'worker')}",
            *generation_cpuset_flag(args),
            f"--memory={args.generation_memory}",
            f"--memory-swap={args.generation_memory}",
            "--user", f"{os.getuid()}:{os.getgid()}",
            "--env", "PYTHONDONTWRITEBYTECODE=1",
            "--volume", f"{directory}:/campaign", args.worker_image,
            "--request", "/campaign/request.json", "--output", "/campaign/diagnostic.jsonl"]
    started = start_command(directory, "worker", argv)
    try:
        returncode = finish_command(directory, "worker", started)
    except BaseException:
        kill_container(container_name(directory, "worker"))
        raise
    require(returncode == 0,
            f"worker exited {returncode}; inspect {directory / 'worker.log'}. "
            "Nothing was retried or excluded.")
    result = validate_diagnostics(workload, request, directory / "diagnostic.jsonl",
                                  BOUNDARIES[args.engine])
    save(directory / "diagnostic-validation.json",
         {key: value for key, value in result.items() if key != "rows_by_case"})
    require(not result["errors"],
            f"Diagnostic preflight rejected; see {directory / 'diagnostic-validation.json'}")
    return result


def declared_gas(case: dict[str, Any]) -> int:
    return sum(tx["gas_limit"] for tx in case["transactions"])


def validate_diagnostics(workload: dict[str, Any], request: dict[str, Any], path: Path,
                         boundary: str) -> dict[str, Any]:
    samples = {sample["sample_id"]: sample for sample in request["samples"]}
    cases = {case["id"]: case for case in workload["cases"]}
    observed: dict[str, Any] = {}
    errors: list[str] = []
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        try:
            row = json.loads(line)
            sample_id = row["sample_id"]
            require(sample_id in samples and sample_id not in observed,
                    f"Unexpected/duplicate sample {sample_id}")
            observed[sample_id] = row
            sample = samples[sample_id]
            case = cases[sample["case_id"]]
            for key in ("sample_id", "case_id", "repetition", "phase"):
                require(row[key] == sample[key], f"Wrong {key}: {sample_id}")
            require(row["schema_version"] == 2 and row["session_id"] == request["session_id"],
                    f"Wrong schema/session: {sample_id}")
            require(row["execution_boundary"] == boundary, f"Wrong boundary: {sample_id}")
            if case["status"] == "unsupported":
                require(row["status"] == "unsupported" and not row["correctness_passed"],
                        f"Unsupported case did not remain unsupported: {case['id']}")
                require(row.get("error") == {"stage": "workload", "message": case["reason"]},
                        f"Unsupported reason mismatch: {case['id']}")
                continue
            require(row["status"] == "executed" and row["correctness_passed"] is True
                    and row.get("error") is None,
                    f"Diagnostic failed for {case['id']}: {row.get('error')}")
            for key in ("baseline_hash", "prepared_hash", "commitment_hash"):
                require(re.fullmatch(r"(?:0x)?[0-9a-fA-F]{64}", row.get(key) or "") is not None,
                        f"Invalid {key}: {case['id']}")
            declared = declared_gas(case)
            require(row["declared_gas"] == declared,
                    f"Diagnostic allowance mismatch: {case['id']}")
            require(type(row["charged_gas"]) is int and 0 < row["charged_gas"] <= declared,
                    f"Invalid charged gas: {case['id']}")
            parameters = case["parameters"]
            key = parameters.get("target_count_key", case["target_operation"])
            count = row["target_count"]
            require(type(count) is int and count > 0
                    and row["opcode_counts"].get(key, 0) == count,
                    f"Missing/incorrect positive semantic count {key}: {case['id']}")
            # The fill's count is block-wide, so it includes the Osaka pre-block
            # system calls; workers count only the case's own transactions.
            observed_ref = parameters.get("fill_observed_opcode_counts", {}).get(key)
            expected = (None if observed_ref is None
                        else observed_ref - SYSTEM_CALL_OPCODES.get(key, 0))
            if (case.get("family") == "precompile"
                    and parameters["workload_mode"] == "fixed_count"):
                expected = parameters["requested_opcode_count"]
            if expected is not None:
                require(count == expected,
                        f"Semantic target count {count} != reference {expected}: {case['id']}")
        except (ValueError, KeyError, TypeError, AttributeError) as error:
            errors.append(f"line {line_number}: {error}")
    missing = set(samples) - set(observed)
    if missing:
        errors.append(f"Missing terminal samples: {sorted(missing)}")
    return {"samples": len(observed), "expected_samples": len(samples), "errors": errors,
            "rows_by_case": {row["case_id"]: row for row in observed.values() if "case_id" in row}}


def inventory_point(args: argparse.Namespace) -> int:
    """The single work size the inventory exports per variant."""
    return args.gas_budgets[0] if args.workload_mode == "gas_budget" else 1


def selection_argv(args: argparse.Namespace) -> list[str]:
    """The target selection as a fill ``-k`` term; empty for the full corpus."""
    return ["-k", args.select] if args.select is not None else []


def inventory_cases(workload: dict[str, Any], point: int) -> dict[str, dict[str, Any]]:
    variants: dict[str, dict[str, Any]] = {}
    for case in workload["cases"]:
        variant, count = case_identity(case)
        require(count == point and variant not in variants,
                f"Inventory is not one work size per variant: {variant}")
        variants[variant] = case
    return variants


def unsupported_label(case: dict[str, Any]) -> str:
    name = test_name(case["id"])
    if name in {"test_clz_diff", "test_p256verify_uncachable"}:
        return name
    for value in case["parameters"].get("source_parameters", {}).values():
        if isinstance(value, str) and value in {"mod_even_1024b_exp_1024", "mod_odd_1024b_exp_1024"}:
            return value
    for token in case["id"].split("[")[-1].split("-"):
        if token in {"mod_even_1024b_exp_1024", "mod_odd_1024b_exp_1024"}:
            return token
    return name


def coverage(workload: dict[str, Any], args: argparse.Namespace) -> dict[str, Any]:
    variants = inventory_cases(workload, inventory_point(args))
    unsupported = [case for case in variants.values() if case["status"] == "unsupported"]
    labels = {unsupported_label(case) for case in unsupported}
    errors = []
    if args.select is not None:
        # A selected campaign pins its variant set by the recorded inventory,
        # which every later stage checks shards and the union against.
        if not variants:
            errors.append(f"Selection matched no variants: {args.select!r}")
    elif len(variants) != EXPECTED_VARIANTS:
        errors.append(f"Expected {EXPECTED_VARIANTS} variants, found {len(variants)}; "
                      "investigate source collection, do not drop variants")
    # The pinned limitations are fixed-count ones. Gas-budget export runs the
    # generator-less variants and skips exact-count witnesses instead, so its
    # unsupported set is the inventory's own record, carried to assembly.
    if args.workload_mode == "fixed_count" and (
            len(unsupported) != 4 or labels != KNOWN_UNSUPPORTED):
        errors.append("Unsupported inventory differs from the four pinned limitations; "
                      "investigate every discrepancy")
    for case in unsupported if args.workload_mode == "fixed_count" else ():
        expected_fragment = ("exceeding the forwardable transaction allowance"
                             if unsupported_label(case) not in {"test_clz_diff",
                                                                "test_p256verify_uncachable"}
                             else "without a code generator")
        if expected_fragment not in case["reason"]:
            errors.append(f"Unexpected unsupported reason for {case['id']}: {case['reason']}")
    return {
        "variant_count": len(variants), "ready": len(variants) - len(unsupported),
        "families": dict(Counter(case["family"] for case in variants.values())),
        "unsupported": [{"id": case["id"], "reason": case["reason"]} for case in unsupported],
        "errors": errors,
    }


# ------------------------------------------------------------- stage flow ---


def matching_settings(directory: Path, args: argparse.Namespace) -> None:
    require((directory / "stage.json").is_file(),
            f"Prerequisite stage has not run: {directory}; run stages in order")
    stage = load(directory / "stage.json")
    require(all(stage["settings"].get(key) == value
                for key, value in identity_settings(args).items()),
            f"Corpus identity settings differ from {directory / 'stage.json'}")
    require((directory / "complete.json").is_file(), f"Stage is incomplete: {directory}")
    for item in load(directory / "complete.json")["artifacts"]:
        require(digest(args.run_home / item["path"]) == item["sha256"],
                f"Changed stage artifact: {item['path']}")


def finish(directory: Path, args: argparse.Namespace, paths: list[Path]) -> None:
    artifacts = [artifact(path, args.run_home) for path in paths]
    marker = directory / "complete.json"
    if marker.is_file():
        require(load(marker).get("artifacts") == artifacts,
                f"Already-complete stage artifacts changed: {marker}")
        return
    save(marker, {"finished": now(), "artifacts": artifacts})


WHOLE_DIRECTORY_STAGES = {"inventory", "corpus"}


def prepare_stage(args: argparse.Namespace, name: str, replace: bool = False) -> Path:
    directory = args.run_home / name
    continuing = getattr(args, "continue_stage", False) and directory.is_dir()
    require(continuing or replace or not directory.exists(),
            f"Stage directory already exists: {directory}")
    # Only an unfinished attempt restarts; a completed stage is verified by main.
    if (continuing and name in WHOLE_DIRECTORY_STAGES and (directory / "stage.json").is_file()
            and not (directory / "complete.json").is_file()):
        stamp = now().replace(":", "")
        previous = args.run_home / f"{name}.attempt-{stamp}"
        directory.rename(previous)
        append_record(args.run_home / "continuation.jsonl",
                      {"stage": name, "preserved_attempt": previous.name, "restarted": now()})
    directory.mkdir(exist_ok=True)
    if not (directory / "stage.json").is_file():
        save(directory / "stage.json", {"stage": name, "started": now(), "settings": settings(args),
                                        "invocation": sys.argv,
                                        "script_sha256": digest(Path(__file__).resolve())})
    else:
        stage = load(directory / "stage.json")
        require(all(stage["settings"].get(key) == value
                    for key, value in identity_settings(args).items()),
                "Stage identity options must stay fixed across invocations")
    return directory


def run_inventory(args: argparse.Namespace, directory: Path) -> None:
    # Both fixture formats: variant discovery must not depend on the format audit.
    budgets = args.workload_mode == "gas_budget"
    # Gas-budget blocks go through the pure-Python reference tool (a 120 Mgas
    # BLAKE2F block takes tens of minutes), so the one inventory fill uses the
    # whole generation pool.
    argv = generation_argv(args, directory, [inventory_point(args)],
                           [*INSTRUCTIONS, PRECOMPILES], selection_argv(args), role="target",
                           families=FAMILIES, gas_budgets=budgets,
                           workers=args.generation_jobs if budgets else 1)
    command(directory, "generator", argv)
    workload = checked_workload(directory / "workload.json", args)
    report = coverage(workload, args)
    save(directory / "coverage.json", report)
    diagnose(args, directory, workload, "inventory-diagnostic")
    require(not report["errors"], f"Inventory divergence; inspect {directory / 'coverage.json'}")
    finish(directory, args, [directory / name for name in (
        "workload.json", "request.json", "diagnostic.jsonl", "coverage.json",
        "diagnostic-validation.json")])


def exact_variant_regex(variant: str, fixture_format: str,
                        work_token: str = r"opcount_[0-9]+(?:\.[0-9]+)?K") -> str:
    """Restore the format token removed by the exporter; anchor the node ID."""
    require(variant.endswith("]") and "[fork_Osaka-" in variant,
            f"Unexpected pytest variant layout: {variant}")
    prefix, parameters = variant[:-1].split("[", 1)
    parts = parameters.split("-")
    require(parts[0] == "fork_Osaka" and len(parts) >= 2 and parts[1] == "",
            f"Expected empty fixture-format slot after fork: {variant}")
    pieces = [re.escape(part) for part in parts]
    pieces[1] = {"engine": "blockchain_test_engine",
                 "both": r"(?:blockchain_test_engine|blockchain_test)"}[fixture_format]
    return re.escape(prefix) + r"\[" + "-".join(pieces) + "-" + work_token + r"\]"


def make_jobs(args: argparse.Namespace, variants: dict[str, dict[str, Any]],
              diagnostics: dict[str, Any]) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    if args.workload_mode == "gas_budget":
        # EIP-7904 layout: every variant at every block budget, one generation
        # job per (variant, budget). The reference transition tool is pure
        # Python, so a single large block (a 360 Mgas pairing block) takes tens
        # of minutes; per-point jobs let the bounded pool spread that work
        # instead of serialising a whole budget in one process. Largest budgets
        # go first so the slowest jobs do not form the tail.
        return [{"name": f"budget-{budget:04d}M-{index:03d}", "counts": [budget],
                 "modules": [variant.split("::", 1)[0]],
                 "selection": ["--regex", "^" + exact_variant_regex(
                     variant, args.fixture_format, rf"benchmark-gas-value_{budget}M") + "$"],
                 "variants": [variant], "gas_budgets": True}
                for budget in sorted(args.gas_budgets, reverse=True)
                for index, variant in enumerate(sorted(variants))], {}
    fixture_format = args.fixture_format
    ordinary = sorted(variant for variant, case in variants.items()
                      if case["family"] != "precompile" and test_name(variant) != SPECIAL_TEST)
    special = sorted(variant for variant, case in variants.items() if test_name(variant) == SPECIAL_TEST)
    require(bool(ordinary) and len(special) == 1, "Missing ordinary/special instruction coverage")
    jobs = [
        {"name": "instructions", "counts": ORDINARY_GRID, "modules": INSTRUCTIONS,
         "selection": ["-k", f"not {SPECIAL_TEST}"], "variants": ordinary},
        {"name": "keccak-max-permutations", "counts": KECCAK_GRID,
         "modules": [INSTRUCTIONS[-1]], "selection": ["-k", SPECIAL_TEST], "variants": special},
    ]
    grouped: dict[tuple[int, ...], list[str]] = defaultdict(list)
    evidence: dict[str, Any] = {}
    for variant, case in sorted(variants.items()):
        if case["family"] != "precompile" or case["status"] != "ready":
            continue
        row = diagnostics[case["id"]]
        require(row["target_count"] == 1, f"Not a single-call inventory diagnostic: {variant}")
        charged = row["charged_gas"]
        feasible = [2 ** exponent for exponent in range(11) if 2 ** exponent * charged <= SCREEN_GAS]
        counts = feasible[-5:] if feasible else [1]
        grouped[tuple(counts)].append(variant)
        evidence[variant] = {"inventory_case_id": case["id"], "single_call_charged_gas": charged,
                             "screen_feasible_counts": feasible, "selected_counts": counts,
                             "single_call_fallback": not feasible}
    for index, (counts, selected) in enumerate(sorted(grouped.items())):
        regex = r"^(?:" + "|".join(exact_variant_regex(variant, fixture_format)
                                   for variant in selected) + r")$"
        jobs.append({"name": f"precompile-{index:02d}", "counts": list(counts),
                     "modules": sorted({variant.split("::", 1)[0] for variant in selected}),
                     "selection": ["--regex", regex], "variants": selected})
    return jobs, evidence


def comparable_parameter(value: Any) -> Any:
    if isinstance(value, str):
        return re.sub(r"^<function (.+) at 0x[0-9a-fA-F]+>$", r"<function \1>", value)
    if isinstance(value, dict):
        return {key: comparable_parameter(item) for key, item in value.items()}
    if isinstance(value, list):
        return [comparable_parameter(item) for item in value]
    return value


def same_variant(case: dict[str, Any], original: dict[str, Any]) -> None:
    require(case["status"] == original["status"], f"Status changed across counts: {case['id']}")
    for key in ("family", "target_operation"):
        require(case.get(key) == original.get(key), f"Inconsistent {key}: {case['id']}")
    # A gas-budget block's tx_count scales with its budget; check_ready_case
    # already pins it to the budget's split, so it is not variant identity.
    keys = ["source_parameters", "target_variant", "target_count_key", "precompile_address",
            "workload_mode", "overhead_baseline"]
    if case["parameters"].get("workload_mode") != "gas_budget":
        keys.append("tx_count")
    for key in keys:
        require(comparable_parameter(case["parameters"].get(key))
                == comparable_parameter(original["parameters"].get(key)),
                f"Inconsistent variant parameter {key}: {case['id']}")
    require(case_role(case) == case_role(original),
            f"campaign_role changed across counts: {case['id']}")


def check_shard(workload: dict[str, Any], job: dict[str, Any],
                variants: dict[str, dict[str, Any]]) -> None:
    expected = {(variant, count) for variant in job["variants"] for count in job["counts"]}
    actual = set()
    for case in workload["cases"]:
        point = case_identity(case)
        require(point in expected and point not in actual,
                f"Unexpected/duplicate point in {job['name']}: {case['id']}")
        same_variant(case, variants[point[0]])
        actual.add(point)
    require(actual == expected,
            f"Missing variants/counts in {job['name']}: {sorted(expected - actual)}")


def get_inventory(args: argparse.Namespace) -> tuple[dict[str, Any], dict[str, Any]]:
    directory = args.run_home / "inventory"
    matching_settings(directory, args)
    workload = checked_workload(directory / "workload.json", args)
    require(not coverage(workload, args)["errors"],
            "Inventory coverage no longer matches pinned source")
    diagnostics = validate_diagnostics(workload, load(directory / "request.json"),
                                       directory / "diagnostic.jsonl", BOUNDARIES[args.engine])
    require(not diagnostics["errors"],
            f"Inventory diagnostics no longer pass: {diagnostics['errors']}")
    return workload, diagnostics["rows_by_case"]


def run_generation_jobs(args: argparse.Namespace, directory: Path,
                        jobs: list[dict[str, Any]], role: str,
                        families: tuple[str, ...]) -> None:
    """Bounded parallel fresh generation before any timed capture.

    Completion-order drain: one waiter thread per started job blocks in
    Popen.wait() and enqueues its exit, so free slots refill as soon as any
    job finishes instead of waiting on the first-launched one. Failed attempts
    are preserved in renamed directories; --continue restarts only missing
    shards with fresh output directories, recorded in continuation.jsonl.
    Nothing is retried implicitly. On interruption or error, still-running
    generation containers are killed best-effort.
    """
    completions: queue.Queue = queue.Queue()
    pending: dict[str, tuple[dict[str, Any], subprocess.Popen]] = {}
    work = [dict(job) for job in jobs]
    failures: dict[str, int] = {}

    def wait_and_report(name: str, process: subprocess.Popen) -> None:
        completions.put((name, process.wait()))

    try:
        while work or pending:
            while work and len(pending) < args.generation_jobs and not failures:
                job = work.pop(0)
                shard = directory / job["name"]
                if (shard / "complete.json").is_file() and (shard / "workload.json").is_file():
                    print(f"generation job already complete: {job['name']}", flush=True)
                    continue
                if shard.exists():
                    stamp = now().replace(":", "")
                    previous = directory / f"{job['name']}.attempt-{stamp}"
                    shard.rename(previous)
                    append_record(directory / "continuation.jsonl",
                                  {"job": job["name"], "preserved_attempt": previous.name,
                                   "previous_result": (load(previous / "generator.result.json")
                                                       if (previous / "generator.result.json").is_file()
                                                       else None),
                                   "restarted": now()})
                shard.mkdir(parents=True)
                selection = list(job["selection"])
                if role == "target" and args.fixture_format == "engine":
                    # One audited fixture format in a single merged -k term;
                    # shard checks compare against the dual-format inventory's
                    # exact variant set.
                    selection = merged_selection(selection, "blockchain_test_engine")
                _log_path, process = start_command(
                    shard, "generator",
                    generation_argv(args, shard, job["counts"], job["modules"],
                                    selection, role, families,
                                    gas_budgets=job.get("gas_budgets", False)))
                pending[job["name"]] = (job, process)
                threading.Thread(target=wait_and_report, args=(job["name"], process),
                                 daemon=True).start()
            if not pending:
                break
            name, returncode = completions.get()
            job, process = pending.pop(name)
            save(directory / job["name"] / "generator.result.json",
                 {"finished": now(), "returncode": returncode})
            if returncode == 0:
                save(directory / job["name"] / "complete.json",
                     {"finished": now(), "job": job["name"]})
                print(f"generation job complete: {name}", flush=True)
            else:
                failures[name] = returncode
    except BaseException:
        for name, (_job, process) in list(pending.items()):
            if process.poll() is None:
                kill_container(container_name(directory / name, "generator"))
                process.kill()
        raise
    finally:
        for _name, (_job, process) in list(pending.items()):
            process.wait()
    require(not failures,
            f"Generation jobs failed (attempts preserved, nothing retried): {failures}; "
            f"investigate logs under {directory} and rerun with --continue")


def run_grids(args: argparse.Namespace, directory: Path) -> None:
    inventory, diagnostics = get_inventory(args)
    variants = inventory_cases(inventory, inventory_point(args))
    jobs, evidence = make_jobs(args, variants, diagnostics)
    plan = {"workload_mode": args.workload_mode, "gas_budgets": identity_settings(args)["gas_budgets"],
            "screen_gas": SCREEN_GAS, "maximum_precompile_calls": 1024,
            "maximum_precompile_points": 5, "ordinary_grid": ORDINARY_GRID,
            "keccak_grid": KECCAK_GRID, "fixture_format": args.fixture_format,
            "parallel_jobs": args.generation_jobs, "jobs": jobs,
            "precompile_evidence": evidence}
    save_or_verify(directory / "plan.json", plan)
    run_generation_jobs(args, directory, jobs, role="target", families=FAMILIES)
    for job in jobs:
        shard = checked_workload(directory / job["name"] / "workload.json", args)
        check_shard(shard, job, variants)
    finish(directory, args, [directory / "plan.json"]
           + [directory / job["name"] / "workload.json" for job in jobs])


def calibration_jobs(args: argparse.Namespace) -> list[dict[str, Any]]:
    """Per-driver count grids from the exporter contract, grouped by count set."""
    # POP keeps a dedicated grid: its driver pairs a fixed 512xPUSH0 seed with
    # POP*N, N <= 512, and no stack cleanup (a nonempty final stack is legal),
    # so its count ceiling differs from every other driver.
    groups = [
        {"name": "calibration-call", "counts": list(CALIBRATION_CALL_GRID),
         "selection": ["-k", "test_call_warm or test_ext_account_query_warm"]},
        {"name": "calibration-push32", "counts": list(CALIBRATION_PUSH32_GRID),
         "selection": ["-k", "opcode_PUSH32"]},
        {"name": "calibration-pop", "counts": list(CALIBRATION_POP_GRID),
         "selection": ["-k", "test_pop"]},
        {"name": "calibration-ordinary", "counts": list(CALIBRATION_ORDINARY_GRID),
         "selection": ["-k", "not (opcode_PUSH32 or test_call_warm "
                              "or test_ext_account_query_warm or test_pop)"]},
    ]
    return [{**group, "modules": [CALIBRATION_MODULE], "role": "calibration"}
            for group in groups]


def run_calibration(args: argparse.Namespace, directory: Path) -> None:
    # Same new generator identity, explicit calibration role and family, single
    # module; count grids follow the exporter's per-driver contract.
    jobs = calibration_jobs(args)
    save_or_verify(directory / "plan.json", {
        "role": "calibration", "module": CALIBRATION_MODULE,
        "families": ["calibration"], "jobs": jobs,
        "mandatory_drivers": sorted(MANDATORY_DRIVERS),
        "expected_tests": sorted(EXPECTED_CALIBRATION_TESTS),
        "note": "calibration lane never widens the target inventory and is never priced"})
    run_generation_jobs(args, directory, jobs, role="calibration",
                        families=("calibration",))
    cases: list[dict[str, Any]] = []
    envelope: dict[str, Any] | None = None
    for job in jobs:
        shard = checked_workload(directory / job["name"] / "workload.json", args,
                                 expected_role="calibration")
        envelope = envelope or {key: value for key, value in shard.items() if key != "cases"}
        cases.extend(shard["cases"])
    require(envelope is not None and cases, "Calibration lane is empty")
    require(len({case["id"] for case in cases}) == len(cases),
            "Duplicate calibration case IDs across count-grid jobs")
    workload = {**envelope, "cases": sorted(cases, key=lambda case: case["id"])}
    save(directory / "workload.json", workload)
    checked_workload(directory / "workload.json", args, expected_role="calibration")

    drivers: dict[str, list[tuple[str, int]]] = defaultdict(list)
    for case in workload["cases"]:
        drivers[test_name(case["id"])].append(case_identity(case))
    missing = [name for name in MANDATORY_DRIVERS if name not in drivers]
    require(not missing, f"Mandatory glue drivers absent from calibration lane: {missing}")
    unexpected = sorted(set(drivers) - set(EXPECTED_CALIBRATION_TESTS))
    require(not unexpected,
            f"Unexpected calibration driver tests (investigate source): {unexpected}")
    variant_count = len({variant for points in drivers.values() for variant, _ in points})
    require(variant_count == EXPECTED_CALIBRATION_VARIANTS,
            f"Expected {EXPECTED_CALIBRATION_VARIANTS} calibration driver variants, "
            f"found {variant_count}; investigate the calibration module")
    save(directory / "driver-coverage.json", {
        "mandatory_drivers": sorted(MANDATORY_DRIVERS),
        "driver_variants": {name: sorted(variant for variant, _ in points)
                            for name, points in sorted(drivers.items())},
        "count_points": {name: sorted(count for _, count in points)
                         for name, points in sorted(drivers.items())},
        "variant_count": variant_count,
        "calibration_case_count": len(workload["cases"]),
        "never_priced": True,
    })
    diagnose(args, directory, workload, "calibration-preflight")
    finish(directory, args, [directory / name for name in (
        "plan.json", "workload.json", "driver-coverage.json")]
        + [directory / job["name"] / "workload.json" for job in jobs])


def archive_recipe(args: argparse.Namespace) -> Path:
    """Archive this operator script into the experiment home for replay.

    The archived copy must stay byte-identical for the experiment's lifetime;
    a mid-experiment script change is surfaced, not silently accepted.
    """
    recipe = args.run_home / "recipe" / "pricing_campaign.py"
    script = Path(__file__).resolve()
    if recipe.is_file():
        require(digest(recipe) == digest(script),
                f"Operator script changed since first archived at {recipe}; "
                "investigate before continuing this experiment")
    else:
        recipe.parent.mkdir(parents=True, exist_ok=True)
        recipe.write_bytes(script.read_bytes())
    return recipe


def run_assemble(args: argparse.Namespace, directory: Path) -> None:
    inventory, diagnostics = get_inventory(args)
    variants = inventory_cases(inventory, inventory_point(args))
    grids = args.run_home / "grids"
    matching_settings(grids, args)
    calibration = args.run_home / "calibration"
    matching_settings(calibration, args)
    jobs, evidence = make_jobs(args, variants, diagnostics)
    saved_plan = load(grids / "plan.json")
    require(saved_plan["jobs"] == jobs and saved_plan["precompile_evidence"] == evidence,
            "Grid plan differs from fresh inventory-derived policy")

    selected = [case for case in inventory["cases"] if case["status"] == "unsupported"]
    contributions = [{**artifact(args.run_home / "inventory/workload.json", args.run_home),
                      "lane": "target", "role": "unsupported",
                      "selected_case_ids": [case["id"] for case in selected]}]
    for job in jobs:
        path = grids / job["name"] / "workload.json"
        shard = checked_workload(path, args)
        check_shard(shard, job, variants)
        ready = [case for case in shard["cases"] if case["status"] == "ready"]
        selected.extend(ready)
        contributions.append({**artifact(path, args.run_home), "lane": "target", "role": "ready",
                              "selected_case_ids": [case["id"] for case in ready]})
    calibration_workload = checked_workload(calibration / "workload.json", args,
                                            expected_role="calibration")
    calibration_cases = list(calibration_workload["cases"])
    selected.extend(calibration_cases)
    contributions.append({**artifact(calibration / "workload.json", args.run_home),
                          "lane": "calibration", "role": "ready",
                          "selected_case_ids": [case["id"] for case in calibration_cases]})

    require(len({case["id"] for case in selected}) == len(selected), "Duplicate IDs in final union")
    target_variants = {case_identity(case)[0] for case in selected if case_role(case) == "target"}
    require(target_variants == set(variants), "Missing/unexpected final target variants")
    calibration_variants = {case_identity(case)[0] for case in calibration_cases}
    require(not (calibration_variants & target_variants),
            "Calibration variant collides with target inventory")
    workload = {key: value for key, value in inventory.items() if key != "cases"}
    workload["cases"] = sorted(selected, key=lambda case: case["id"])
    save(directory / "workload.json", workload)
    checked_workload(directory / "workload.json", args, expected_role="mixed")

    ready_target = sum(case["status"] == "ready" and case_role(case) == "target"
                       for case in selected)
    ready_calibration = len(calibration_cases)
    unsupported = len(selected) - ready_target - ready_calibration
    inventory_unsupported = sum(case["status"] == "unsupported" for case in inventory["cases"])
    require(unsupported == inventory_unsupported and (
            args.workload_mode == "gas_budget" or unsupported == 4),
            f"Unsupported target cases {unsupported} differ from the inventory's "
            f"{inventory_unsupported} (fixed-count also pins exactly four)")
    save(directory / "freeze.json", {
        "sessions": SESSIONS, "pilot_repetitions": PILOT_REPS,
        "warmup_repetitions": WARMUP_REPS, "qualification_repetitions": QUAL_REPS,
        "records_per_ready_case": RECORDS_PER_READY_CASE,
        "ready_target_cases": ready_target, "ready_calibration_cases": ready_calibration,
        "unsupported_cases": unsupported,
        "expected_qualification_records":
            QUALIFICATIONS_PER_READY_CASE * (ready_target + ready_calibration),
        "expected_total_records":
            RECORDS_PER_READY_CASE * (ready_target + ready_calibration) + unsupported,
        "target_variant_count": len(variants),
        "anchor_rate_gas_per_second": ANCHOR_RATE,
    })
    diagnose(args, directory, workload, "corpus-preflight")

    recipe = archive_recipe(args)
    command_paths = sorted(
        path for stage in ("inventory", "grids", "calibration", "corpus")
        for path in (args.run_home / stage).rglob("*.command.json"))
    commands = [{**artifact(path, args.run_home), **load(path),
                 "result": load(path.with_name(path.name.replace(".command.json", ".result.json"))),
                 "log": artifact(path.with_name(path.name.replace(".command.json", ".log")),
                                 args.run_home)}
                for path in command_paths]
    save(directory / "build.json", {
        "schema_version": 2, "fork": "Osaka", "created": now(),
        "generator": workload["generator"], "image_reference": args.generator_image,
        "settings": settings(args), "workload": artifact(directory / "workload.json", args.run_home),
        "recipe": {**artifact(recipe, args.run_home),
                   "source": str(Path(__file__).resolve()),
                   "source_sha256": digest(Path(__file__).resolve())},
        "target_variant_count": len(variants), "case_count": len(selected),
        "ready_target_count": ready_target, "ready_calibration_count": ready_calibration,
        "unsupported_count": unsupported,
        "scope": "mixed target+calibration pricing campaign; calibration lane never priced",
        "inventory_diagnostic": artifact(args.run_home / "inventory/diagnostic.jsonl", args.run_home),
        "grid_plan": artifact(grids / "plan.json", args.run_home),
        "driver_coverage": artifact(calibration / "driver-coverage.json", args.run_home),
        "contributions": contributions, "commands": commands,
        "preflight": {"status": "passed",
                      "request": artifact(directory / "request.json", args.run_home),
                      "diagnostic": artifact(directory / "diagnostic.jsonl", args.run_home),
                      "validation": artifact(directory / "diagnostic-validation.json", args.run_home)},
    })
    finish(directory, args, [directory / name for name in (
        "workload.json", "freeze.json", "build.json", "request.json",
        "diagnostic.jsonl", "diagnostic-validation.json")])


# ------------------------------------------------------------------ config ---


def resolve_image(reference: str) -> dict[str, str]:
    try:
        result = subprocess.run(
            ["docker", "image", "inspect", "--format", "{{.Id}}", reference],
            check=True, text=True, capture_output=True)
    except subprocess.CalledProcessError as error:
        raise ValueError(f"Cannot resolve image {reference!r}: {error.stderr.strip()}")
    image_id = result.stdout.strip()
    require(IMAGE_ID.fullmatch(image_id) is not None, f"Not a local image ID: {image_id}")
    return {"reference": reference, "image_id": image_id}


def load_policy(args: argparse.Namespace) -> dict[str, Any]:
    path = args.run_home / "policy.json"
    return load(path) if path.is_file() else {}


def validate_analysis_config(path: Path, policy: dict[str, Any], engine: str) -> None:
    config = load(path)
    require(config.get("version") == 1 and config.get("clients") == [engine],
            f"Analysis config must be version 1 with the {engine} client")
    require(config.get("gas_costs", {}).get("fork") == "osaka", "Analysis fork must be osaka")
    modeling = config.get("modeling", {})
    require(modeling.get("bootstrap_iterations") == 1000,
            "Analysis config must freeze 1000 bootstrap iterations")
    require(modeling.get("random_seed") == SEED, f"Analysis config must freeze seed {SEED}")
    qualification = config.get("qualification", {})
    for key, value in STRICT_GATES.items():
        actual = qualification.get(key)
        equal = (float(actual) == value if isinstance(value, float)
                 and isinstance(actual, (int, float)) else actual == value)
        require(equal, f"Gate {key} must be {value}, got {actual}")
    campaign = config.get("campaign", {})
    require(campaign.get("eligible_phases") == ["qualification"],
            "Campaign eligibility must be qualification-only")
    require(campaign.get("eligible_statuses") == ["executed"],
            "Campaign eligibility must be executed-only")
    require(campaign.get("require_correctness_passed") is True,
            "Campaign must require correctness")
    glue_enabled = config.get("glue_adjustment", {}).get("enabled")
    scenarios = config.get("pricing_scenarios")
    require(isinstance(scenarios, list) and len(scenarios) == 1
            and scenarios[0].get("name") == "osaka-600m"
            and scenarios[0].get("anchor_rate") == ANCHOR_RATE
            and scenarios[0].get("margin_pct") == 0.0,
            "Pricing scenario must be osaka-600m at 600000000 gas/s with zero margin")
    require(config.get("anchor_rate") == ANCHOR_RATE,
            f"Top-level anchor_rate must be {ANCHOR_RATE}")
    require(glue_enabled is True,
            "Glue adjustment must be enabled (smoke included; it exercises "
            "adjustment and rejection semantics)")
    if policy:
        require(policy.get("anchor_rate") in (None, ANCHOR_RATE),
                "policy.json anchor disagrees with the frozen 600M gas/s anchor")


def validate_models_target_only(config: dict[str, Any], workload: dict[str, Any]) -> None:
    roles = {case["id"]: case_role(case) for case in workload["cases"]}
    calibration_ids = {case_id for case_id, role in roles.items() if role == "calibration"}
    require(calibration_ids, "Assembled workload has no calibration lane to discriminate")
    models = config.get("models", {}).get("custom", [])
    require(models, "Analysis config has no custom models")
    for model in models:
        for selector in model.get("filter_by", []):
            matched = [case_id for case_id in calibration_ids if selector in case_id]
            require(not matched,
                    f"Pricing model {model.get('test_name')} selector {selector!r} "
                    f"matches calibration cases: {sorted(matched)[:3]}")


def validate_controller_config(config: dict[str, Any], workload: Path, analysis: Path,
                               sessions: int, repetitions: int) -> None:
    compute = config["compute"]
    require(compute["sessions"] == sessions and compute["repetitions"] == repetitions
            and compute["pilot_repetitions"] >= 1 and compute["warmup_repetitions"] >= 1,
            "Controller rejects the session schedule")
    require(compute["container_runtime"] in ("docker", "podman"), "Invalid runtime")
    require(IMAGE_ID.fullmatch(compute["worker_image"]) is not None,
            "Worker image must be an exact local ID")
    require(IMAGE_ID.fullmatch(compute["analyzer"]["image"]) is not None,
            "Analyzer image must be an exact local ID")
    require(workload.is_file() and analysis.is_file(), "Workload or analysis config missing")
    limits = compute["resource_limits"]
    require("cpuset" not in limits and "cpuset_count" not in limits,
            "Capture uses the upstream default: no CPU pinning")
    require(limits["swap_disabled"] is True, "Swap must be disabled for capture")
    require(re.fullmatch(r"[1-9][0-9]*[smh]", compute["timeout"]) is not None, "Invalid timeout")


def make_smoke_workload(corpus: Path, selected_campaign: bool) -> dict[str, Any]:
    """Mixed-lane smoke subset for the glue-enabled path.

    Every calibration count-point is kept, plus ALL count-points of one target
    variant of each corpus-side driver test family. Fewer points would make
    the curvature and leave-one-point-out holdout gates non-identifiable by
    construction, and the smoke could never demonstrate a qualified path;
    deliberate rejection behavior is exercised separately. A selected campaign
    uses one variant of each target test it contains instead of the full-corpus
    driver families. Smoke prices are still not deployable output.
    """
    workload = load(corpus / "workload.json")
    ready = [case for case in workload["cases"] if case["status"] == "ready"]
    points: dict[tuple[str, int], dict[str, Any]] = {case_identity(case): case
                                                     for case in ready}
    selected: list[dict[str, Any]] = [
        case for case in ready
        if case_role(case) == "calibration"]
    target_count = sum(case_role(case) == "target" for case in selected)
    require(not target_count and selected, "Calibration lane missing from corpus")
    drivers = (sorted({test_name(variant) for (variant, count) in points
                       if case_role(points[(variant, count)]) == "target"})
               if selected_campaign else CORPUS_DRIVER_TESTS)
    for driver in drivers:
        variants = sorted({variant for (variant, _count) in points
                           if test_name(variant) == driver})
        require(variants, f"Corpus driver test absent from assembled corpus: {driver}")
        counts = sorted(count for (variant, count) in points if variant == variants[0])
        require(len(counts) >= 3,
                f"Smoke driver variant lacks enough count points for the holdout "
                f"and curvature gates: {driver}")
        selected.extend(points[(variants[0], count)] for count in counts)
    return {**{key: value for key, value in workload.items() if key != "cases"},
            "cases": sorted(selected, key=lambda case: case["id"])}


def make_smoke_analysis(smoke_workload: dict[str, Any], engine: str) -> dict[str, Any]:
    models = []
    new_params: dict[str, Any] = {}
    for case in sorted(smoke_workload["cases"], key=lambda case: case["id"]):
        if case_role(case) != "target":
            continue
        variant = case_identity(case)[0]
        if any(model["filter_by"] == [variant[:-1] + "-"] for model in models):
            continue
        suffix = hashlib.sha256(variant.encode()).hexdigest()[:12]
        param = f"WORKLOAD_{case['target_operation']}_{suffix}"
        new_params[param] = None
        model = {"test_name": test_name(case["id"]),
                 "target_operation": case["target_operation"],
                 "filter_by": [variant[:-1] + "-"],
                 "model_params": {"target_coef": param}}
        # Precompile counts are keyed PRECOMPILE_<address>, as create-config does.
        count_key = case["parameters"].get("target_count_key")
        if count_key:
            model["target_operation_count_source"] = count_key
        models.append(model)
    return {
        "version": 1, "clients": [engine], "gas_costs": {"fork": "osaka"},
        "output": {"plots": False},
        "modeling": {"bootstrap_iterations": 1000, "random_seed": SEED},
        "glue_adjustment": {"enabled": True},
        "qualification": dict(STRICT_GATES),
        "campaign": {"eligible_phases": ["qualification"], "eligible_statuses": ["executed"],
                     "require_correctness_passed": True},
        "anchor_rate": ANCHOR_RATE,
        "pricing_scenarios": [{"name": "osaka-600m", "anchor_rate": ANCHOR_RATE,
                               "margin_pct": 0.0}],
        "models": {"custom": models}, "new_params": new_params,
    }


def run_config(args: argparse.Namespace, directory: Path) -> None:
    corpus = args.run_home / "corpus"
    matching_settings(corpus, args)
    freeze = load(corpus / "freeze.json")
    require(freeze["sessions"] == SESSIONS
            and freeze["anchor_rate_gas_per_second"] == ANCHOR_RATE,
            "Corpus freeze does not match the frozen campaign policy")
    policy = load_policy(args)

    analysis_config = (args.run_home / "analysis-gasfit.yaml").resolve(strict=True)
    validate_analysis_config(analysis_config, policy, args.engine)
    validate_models_target_only(load(analysis_config), load(corpus / "workload.json"))

    images = {name: resolve_image(reference) for name, reference in (
        ("generator", args.generator_image), ("worker", args.worker_image),
        ("analyzer", args.analyzer_image))}
    require(len({entry["image_id"] for entry in images.values()}) == 3,
            "Generator, worker, and analyzer must be distinct images")
    controller = Path(args.controller).resolve(strict=True)
    require(controller.is_file() and os.access(controller, os.X_OK),
            f"Controller is not an executable file: {controller}")
    for name, path in (("benchmarkoor", args.benchmarkoor_root), ("newl1", args.newl1_root),
                       ("execution_specs", args.execution_specs_root)):
        require(path.is_dir(), f"Missing source path for provenance: {name}: {path}")

    workload_path = (corpus / "workload.json").resolve(strict=True)
    config = {"compute": {
        "id": args.run_home.name, "engine": args.engine, "workload": str(workload_path),
        "results_dir": str(args.run_home), "container_runtime": "docker",
        "worker_image": images["worker"]["image_id"],
        "analyzer": {"image": images["analyzer"]["image_id"], "config": str(analysis_config)},
        "seed": SEED, "sessions": SESSIONS,
        "pilot_repetitions": PILOT_REPS, "warmup_repetitions": WARMUP_REPS,
        "repetitions": QUAL_REPS, "timeout": args.timeout,
        "resource_limits": {"memory": args.memory, "swap_disabled": True},
        "source_paths": {"benchmarkoor": str(args.benchmarkoor_root),
                         "newl1": str(args.newl1_root),
                         "execution_specs": str(args.execution_specs_root)},
    }}
    validate_controller_config(config, workload_path, analysis_config, SESSIONS, QUAL_REPS)
    save(directory / "compute.yaml", config)

    smoke_workload = make_smoke_workload(corpus, args.select is not None)
    save(directory / "smoke-workload.json", smoke_workload)
    smoke_analysis = make_smoke_analysis(smoke_workload, args.engine)
    save(directory / "smoke-gasfit.yaml", smoke_analysis)
    validate_analysis_config(directory / "smoke-gasfit.yaml", policy, args.engine)
    validate_models_target_only(smoke_analysis, smoke_workload)
    smoke_compute = dict(config["compute"])
    smoke_compute.update({
        "id": args.run_home.name + "-smoke",
        "workload": str((directory / "smoke-workload.json").resolve()),
        "sessions": SMOKE_SESSIONS, "repetitions": SMOKE_QUAL_REPS, "timeout": "2h",
        "analyzer": {"image": images["analyzer"]["image_id"],
                     "config": str((directory / "smoke-gasfit.yaml").resolve())}})
    smoke_config = {"compute": smoke_compute}
    validate_controller_config(smoke_config, (directory / "smoke-workload.json").resolve(),
                               (directory / "smoke-gasfit.yaml").resolve(),
                               SMOKE_SESSIONS, SMOKE_QUAL_REPS)
    save(directory / "smoke-compute.yaml", smoke_config)
    smoke_ready = sum(case["status"] == "ready" for case in smoke_workload["cases"])
    save(directory / "images.json", {
        "resolved": images,
        "controller": {"path": str(controller), "sha256": digest(controller)},
        "analysis_config": artifact(analysis_config, args.run_home),
        "frozen_schedule": {"sessions": SESSIONS, "pilot": PILOT_REPS, "warmup": WARMUP_REPS,
                            "qualification": QUAL_REPS, "seed": SEED,
                            "anchor_rate": ANCHOR_RATE},
        "smoke_schedule": {"sessions": SMOKE_SESSIONS,
                           "pilot_repetitions": 1, "warmup_repetitions": 1,
                           "qualification_repetitions": SMOKE_QUAL_REPS,
                           "glue_enabled": True, "anchor_rate": ANCHOR_RATE,
                           "records_per_ready_case": SMOKE_RECORDS_PER_READY_CASE,
                           "expected_total_records": SMOKE_RECORDS_PER_READY_CASE * smoke_ready}})
    finish(directory, args, [directory / name for name in (
        "compute.yaml", "smoke-compute.yaml", "smoke-workload.json",
        "smoke-gasfit.yaml", "images.json")])


# ------------------------------------------------------------------ audit ---


class Audit:
    def __init__(self, run: Path) -> None:
        self.run = run
        self.errors: dict[str, dict[str, Any]] = {}

    def check(self, condition: Any, code: str, detail: Any) -> bool:
        if condition:
            return True
        entry = self.errors.setdefault(code, {"count": 0, "examples": []})
        entry["count"] += 1
        if len(entry["examples"]) < 12:
            entry["examples"].append(detail)
        return False


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line]


def latest_analysis_attempt(run: Path) -> Path | None:
    attempts = sorted((run / "analysis").glob("*/status.json"), key=lambda path: path.stat().st_mtime)
    return attempts[-1].parent if attempts else None


def run_audit(args: argparse.Namespace, directory: Path) -> None:
    run = Path(args.run).resolve(strict=True)
    require(run.is_dir() and run.name.startswith("compute-"), f"Not a run directory: {run}")
    audit = Audit(run)
    config_dir = args.run_home / "config"
    matching_settings(config_dir, args)
    corpus = args.run_home / "corpus"
    matching_settings(corpus, args)
    freeze = load(corpus / "freeze.json")
    frozen_images = load(config_dir / "images.json")["resolved"]

    workload = load(run / "workload.json")
    roles = {case["id"]: case_role(case) for case in workload["cases"]}
    statuses = {case["id"]: case["status"] for case in workload["cases"]}
    cases = {case["id"]: case for case in workload["cases"]}
    audit.check(digest(run / "workload.json") == digest(corpus / "workload.json"),
                "workload_identity", "run workload differs from the frozen corpus")

    manifest = load(run / "manifest.json")
    campaign = load(run / "campaign.json")
    audit.check(manifest["software"]["images"]["worker"] == campaign["worker_image"]
                == frozen_images["worker"]["image_id"],
                "worker_image_identity", "worker image differs from frozen config")
    audit.check(campaign["sessions"] == SESSIONS and campaign["seed"] == SEED,
                "frozen_schedule", "campaign.json is not the frozen 8-session schedule")
    audit.check("cpuset" not in campaign["resource_limits"]
                and "cpuset_count" not in campaign["resource_limits"],
                "unpinned_capture", "capture pins CPUs; the frozen policy is unpinned")

    preflight: dict[str, dict[str, Any]] = {}
    for row in load_jsonl(corpus / "diagnostic.jsonl"):
        if row.get("status") == "executed" and "case_id" in row:
            preflight[row["case_id"]] = row

    requested = {row["sample_id"]: row for row in load_jsonl(run / "requested-samples.jsonl")}
    results = load_jsonl(run / "samples.jsonl")
    observed: dict[str, dict[str, Any]] = {}
    charged_by_case: dict[str, set[int]] = defaultdict(set)
    for row in results:
        audit.check(row["sample_id"] in requested and row["sample_id"] not in observed,
                    "sample_accounting", f"unexpected/duplicate {row['sample_id']}")
        if row["sample_id"] in requested:
            observed.setdefault(row["sample_id"], row)
    audit.check(set(requested) == set(observed), "missing_terminal_records",
                sorted(set(requested) - set(observed))[:12])
    audit.check(len(requested) == freeze["expected_total_records"] == len(results),
                "record_count", f"requested {len(requested)} results {len(results)} "
                                f"expected {freeze['expected_total_records']}")

    phase_role: Counter = Counter()
    per_case: dict[str, Counter] = defaultdict(Counter)
    hash_state: dict[str, dict[str, str]] = defaultdict(dict)
    for sample_id, request in requested.items():
        row = observed.get(sample_id)
        if row is None:
            continue
        case_id = request["case_id"]
        role = roles[case_id]
        phase_role[(request["phase"], row["status"], role)] += 1
        per_case[case_id][(request["phase"], row["status"])] += 1
        audit.check(all(row[key] == request[key]
                        for key in ("case_id", "repetition", "phase", "session_id")),
                    "identity_mismatch", sample_id)
        audit.check(row.get("schema_version") == 2
                    and row.get("execution_boundary") == BOUNDARIES[args.engine],
                    "record_contract", sample_id)
        if row["status"] == "executed":
            audit.check(row.get("correctness_passed") is True, "correctness_failed", sample_id)
            hashes = {key: row.get(key)
                      for key in ("baseline_hash", "prepared_hash", "commitment_hash")}
            audit.check(all(HASH64.fullmatch(value or "") for value in hashes.values()),
                        "invalid_hashes", sample_id)
            for key, value in hashes.items():
                previous = hash_state[case_id].setdefault(key, value)
                audit.check(previous == value, "unstable_hash", (case_id, key))
            declared = declared_gas(cases[case_id])
            audit.check(row.get("declared_gas") == declared, "declared_gas", sample_id)
            audit.check(isinstance(row.get("charged_gas"), int)
                        and 0 < row["charged_gas"] <= declared, "charged_gas", sample_id)
            charged_by_case[case_id].add(row["charged_gas"])
            frozen = preflight.get(case_id)
            audit.check(frozen is not None, "frozen_preflight_row_missing", case_id)
            if frozen is not None:
                audit.check(row["charged_gas"] == frozen["charged_gas"],
                            "frozen_gas_mismatch", (case_id, row["charged_gas"],
                                                    frozen["charged_gas"]))
                for hash_key, value in hashes.items():
                    audit.check(value == frozen.get(hash_key),
                                "frozen_hash_mismatch", (case_id, hash_key))
            if request["phase"] == "diagnostic":
                case = cases[case_id]
                key = case["parameters"].get("target_count_key", case["target_operation"])
                count = row.get("target_count")
                audit.check(isinstance(count, int) and count > 0
                            and (row.get("opcode_counts") or {}).get(key, 0) == count,
                            "semantic_count", (case_id, key))
                if frozen is not None:
                    audit.check(count == frozen.get("target_count"),
                                "frozen_count_mismatch",
                                (case_id, count, frozen.get("target_count")))
            else:
                audit.check(isinstance(row.get("execution_duration_ns"), int)
                            and row["execution_duration_ns"] > 0,
                            "nonpositive_duration", sample_id)
                audit.check(not row.get("opcode_counts"), "timed_row_counters", sample_id)
                audit.check(row.get("target_count") is None, "timed_row_target_count", sample_id)
        else:
            audit.check(row["status"] == "unsupported"
                        and statuses[case_id] == "unsupported"
                        and (row.get("error") or {}).get("stage") == "workload",
                        "unexpected_nonexecuted", sample_id)

    for case_id, charged in charged_by_case.items():
        audit.check(len(charged) == 1,
                    "charged_gas_varies_across_phases", (case_id, sorted(charged)))

    for case_id, state in statuses.items():
        if state == "ready":
            expected_phases = {"diagnostic": 1, "pilot": SESSIONS, "warmup": SESSIONS,
                               "qualification": SESSIONS * QUAL_REPS}
        else:
            expected_phases = {"diagnostic": 1}
            state = "unsupported"
        for phase, expected in expected_phases.items():
            wanted = "unsupported" if state == "unsupported" else "executed"
            audit.check(per_case[case_id][(phase, wanted)] == expected,
                        "case_phase_accounting", (case_id, phase, dict(per_case[case_id])))

    audit.check(phase_role[("qualification", "executed", "target")]
                + phase_role[("qualification", "executed", "calibration")]
                == freeze["expected_qualification_records"],
                "qualification_total", dict(phase_role))
    audit.check(phase_role[("qualification", "executed", "calibration")] > 0,
                "calibration_lane_missing", "no calibration qualification rows")

    sessions_dir = sorted(path for path in (run / "sessions").iterdir() if path.is_dir())
    # Warmup samples run inside each qualification-NN process (planCampaign).
    expected_sessions = (["diagnostic-00"]
                         + [f"pilot-{index:02d}" for index in range(SESSIONS)]
                         + [f"qualification-{index:02d}" for index in range(SESSIONS)])
    audit.check(sorted(path.name for path in sessions_dir) == sorted(expected_sessions),
                "session_layout", [sorted(expected_sessions),
                                   sorted(path.name for path in sessions_dir)])
    for path in sessions_dir:
        exit_record = load(path / "exit.json")
        audit.check(exit_record.get("exit_code") == 0 and exit_record.get("oom_killed") is False,
                    "worker_exit", path.name)
        audit.check((path / "request.json").is_file() and (path / "samples.jsonl").is_file(),
                    "session_artifacts", path.name)

    analysis_dir = latest_analysis_attempt(run)
    audit.check(analysis_dir is not None, "missing_analysis", "no analysis attempt recorded")
    calibration_test_names = {test_name(case_id) for case_id, role in roles.items()
                              if role == "calibration"}
    calibration_priced: list[Any] = []
    analyzer_exit: dict[str, Any] | None = None
    planned_count = 0
    adjusted_missing = 0
    glue_enabled = None
    csv_roles: Counter = Counter()
    if analysis_dir is not None:
        analyzer_status = load(analysis_dir / "status.json")
        analyzer_exit = {"exit_code": analyzer_status.get("exit_code"),
                         "oom_killed": analyzer_status.get("oom_killed")}
        audit.check(analyzer_exit["exit_code"] == 0 and analyzer_exit["oom_killed"] is False,
                    "analyzer_exit", analyzer_status.get("attempt_id"))
        audit.check(analyzer_status.get("analyzer_image")
                    == frozen_images["analyzer"]["image_id"],
                    "analyzer_image_identity", analyzer_status.get("analyzer_image"))
        analysis_config = load(analysis_dir / "config.yaml")
        glue_enabled = analysis_config.get("glue_adjustment", {}).get("enabled")
        with (analysis_dir / "runtimes.csv").open(encoding="utf-8", newline="") as stream:
            runtimes = list(csv.DictReader(stream))
        audit.check(runtimes and "param_campaign_role" in runtimes[0],
                    "role_column_missing", list(runtimes[0])[:24] if runtimes else "empty csv")
        if runtimes and "param_campaign_role" in runtimes[0]:
            for row in runtimes:
                csv_roles[row["param_campaign_role"]] += 1
        audit.check(set(csv_roles) == set(ROLES), "csv_role_values", dict(csv_roles))
        analysis_status = load(analysis_dir / "reports/analysis_status.json")
        planned = analysis_status.get("planned_models", [])
        custom_models = analysis_config.get("models", {}).get("custom", [])
        expected_labels = ["models.custom[" + str(index) + "]"
                           for index in range(len(custom_models))]
        actual_labels = sorted(model.get("source_label") for model in planned)
        audit.check(actual_labels == sorted(expected_labels),
                    "planned_model_accounting",
                    {"expected": len(expected_labels), "planned": len(planned),
                     "missing": sorted(set(expected_labels) - set(actual_labels))[:6],
                     "unexpected": sorted(set(actual_labels) - set(expected_labels))[:6]})
        audit.check(isinstance(planned, list) and bool(planned), "no_planned_models",
                    "analysis_status has no planned model list")
        planned_count = len(planned)
        for index, model in enumerate(planned):
            source_label = model.get("source_label", f"planned_models[{index}]")
            audit.check(bool(model.get("status")), "model_decisions_missing",
                        (source_label, "status"))
            if model.get("test_name") in calibration_test_names:
                calibration_priced.append((source_label, model.get("test_name")))
            if glue_enabled:
                audit.check(bool(model.get("adjusted_estimate_status")),
                            "adjusted_decisions_missing", (source_label, model.get("status")))
                if not model.get("adjusted_estimate_status"):
                    adjusted_missing += 1
        audit.check(not calibration_priced, "calibration_priced", calibration_priced[:12])

    recommendations_summary: dict[str, Any] = {"status": "not_provided"}
    if args.recommendations:
        recommendations_path = (Path(args.recommendations) / "recommendations.json").resolve(strict=True)
        recommendations = load(recommendations_path)
        rows = (recommendations if isinstance(recommendations, list)
                else recommendations.get("variants"))
        audit.check(isinstance(rows, list) and bool(rows), "recommendations_missing", str(recommendations_path))
        if rows:
            variant_ids = [row.get("variant_id") for row in rows]
            audit.check(all(isinstance(row_id, str) and row_id
                            for row_id in variant_ids),
                        "recommendation_identity_missing",
                        [index for index, row_id in enumerate(variant_ids)
                         if not isinstance(row_id, str) or not row_id][:6])
            target_variants = {case_identity(case)[0] for case in workload["cases"]
                               if roles[case["id"]] == "target"}
            unexpected = sorted({row_id for row_id in set(variant_ids)
                                 if isinstance(row_id, str)} - target_variants)
            audit.check(set(variant_ids) == target_variants
                        and len(variant_ids) == len(set(variant_ids)),
                        "recommendation_coverage",
                        {"missing": sorted(target_variants - set(variant_ids))[:6],
                         "unexpected": unexpected[:6]})
            audit.check(all(row.get("policy_decision") for row in rows),
                        "recommendation_decisions_missing",
                        [row.get("variant_id") for row in rows if not row.get("policy_decision")][:12])
            audit.check(all(row.get("missing_coverage") is not None for row in rows),
                        "recommendation_coverage_notes_missing",
                        [row.get("variant_id") for row in rows
                         if row.get("missing_coverage") is None][:12])
            audit.check(not [row for row in rows
                             if row.get("variant_id") and any(
                                 name in str(row["variant_id"]) for name in calibration_test_names)],
                        "calibration_in_recommendations", "calibration rows leaked into pricing")
            recommendations_summary = {
                "status": "checked", "variant_rows": len(rows),
                "policy_decisions": dict(Counter(row.get("policy_decision") for row in rows)),
            }

    exclusions = load_jsonl(run / "exclusions.jsonl")
    summary = {
        "run": str(run), "audited": now(),
        "integrity_passed": not audit.errors,
        "records": {"requested": len(requested), "terminal": len(results),
                    "expected": freeze["expected_total_records"]},
        "phase_status_role_counts": {f"{phase}|{state}|{role}": count
                                     for (phase, state, role), count in sorted(phase_role.items())},
        "ready_cases": {"target": sum(roles[c] == "target" for c in statuses
                                      if statuses[c] == "ready"),
                        "calibration": sum(roles[c] == "calibration" for c in statuses
                                           if statuses[c] == "ready")},
        "unsupported_cases": freeze["unsupported_cases"],
        "worker_sessions": {path.name: load(path / "exit.json") for path in sessions_dir},
        "analyzer": analyzer_exit,
        "analysis": {"planned_models": planned_count, "glue_enabled": glue_enabled,
                     "adjusted_decisions_missing": adjusted_missing,
                     "csv_role_rows": dict(csv_roles)},
        "calibration_never_priced": not calibration_priced,
        "calibration_role_note": "calibration rows feed glue estimation only and never "
                                 "produce pricing recommendations",
        "recommendations": recommendations_summary,
        "exclusions": len(exclusions),
        "other_runs_preserved": sorted(path.name for path in run.parent.iterdir()
                                       if path.is_dir() and path.name.startswith("compute-")
                                       and path != run),
        "errors": audit.errors,
    }
    save_replace(directory / "audit.json", summary)
    save_replace(args.run_home / "campaign-summary.json", summary)
    require(not audit.errors,
            f"Run audit failed with {sum(entry['count'] for entry in audit.errors.values())} "
            f"finding(s); see {directory / 'audit.json'}")


# ------------------------------------------------------------------- main ---


def expand_cpuset(spec: str) -> set[int]:
    cpus: set[int] = set()
    for piece in spec.split(","):
        if "-" in piece:
            start, end = piece.split("-")
            cpus.update(range(int(start), int(end) + 1))
        else:
            cpus.add(int(piece))
    return cpus


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""Stage recipes for the frozen experiment home:

  ROOT=/server/weihan
  HOME_DIR=$ROOT/benchmarkoor/results/<experiment-home>
  GEN=$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-generator:pricing)
  WORK=$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-worker-newl1:pricing)
  ANA=$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-analyzer:pricing)
  MODE="--engine newl1 --workload-mode gas_budget --gas-budgets 120,240,360"
  COMMON="--run-home $HOME_DIR --generator-image $GEN --worker-image $WORK \
      $MODE --fixture-format engine"

  python3 scripts/compute/pricing_campaign.py inventory $COMMON
  python3 scripts/compute/pricing_campaign.py grids $COMMON --generation-jobs 4
  python3 scripts/compute/pricing_campaign.py calibration $COMMON
  python3 scripts/compute/pricing_campaign.py assemble $COMMON
  uv run --project analyzer python -m evm_gasfit.recommendations create-config --client newl1 \
      --workload $HOME_DIR/corpus/workload.json --out $HOME_DIR/analysis-gasfit.yaml
  python3 scripts/compute/pricing_campaign.py config --run-home $HOME_DIR $MODE \
      --generator-image $GEN --worker-image $WORK --analyzer-image $ANA \
      --controller $ROOT/benchmarkoor/bin/benchmarkoor \
      --benchmarkoor-root $ROOT/benchmarkoor --newl1-root $ROOT/bnbchain-newL1 \
      --execution-specs-root $ROOT/execution-specs \
      --memory 24g
  # glue-enabled 4-session smoke first (adjustment + rejection semantics;
  # sparse/inconclusive smoke prices are expected), then the frozen capture
  # (unpinned like upstream; keep the host otherwise idle):
  $ROOT/benchmarkoor/bin/benchmarkoor run --config $HOME_DIR/config/smoke-compute.yaml
  $ROOT/benchmarkoor/bin/benchmarkoor run --config $HOME_DIR/config/compute.yaml
  uv run --project analyzer python -m evm_gasfit.recommendations build \
      --workload $HOME_DIR/corpus/workload.json \
      --analysis $HOME_DIR/runs/<RUN-UUID>/analysis/<ATTEMPT> --out $HOME_DIR/recommendations
  python3 scripts/compute/pricing_campaign.py audit --run-home $HOME_DIR $MODE \
      --run $HOME_DIR/runs/<RUN-UUID> --recommendations $HOME_DIR/recommendations \
      --generator-image $GEN --worker-image $WORK --fixture-format engine

Generation runs in bounded parallel jobs on CPUs excluding 14 and 30, strictly
before timed capture. A failed generation job preserves its attempt directory;
rerun the same stage command with --continue to fill only the missing shards.
Timed capture uses CPU 14 only. Failed measurements are never retried; audit
every run and keep earlier attempts.

Workload modes: gas_budget (EIP-7904 block layout) makes each target case one
block whose budget EEST splits into 2^24-gas transactions, at the Mgas budgets
listed by --gas-budgets. fixed_count (the default) makes each target case one
fixed-work transaction. The calibration lane stays fixed-count in both modes.""")
    parser.add_argument("stage", choices=("inventory", "grids", "calibration", "assemble",
                                          "config", "audit"))
    parser.add_argument("--run-home", type=Path, required=True)
    parser.add_argument("--generator-image", required=True, help="Local sha256 generator image ID")
    parser.add_argument("--worker-image", required=True, help="Local sha256 worker image ID")
    parser.add_argument("--engine", required=True, choices=sorted(BOUNDARIES),
                        help="Engine the worker image executes; fixes the timed boundary")
    parser.add_argument("--workload-mode", choices=WORKLOAD_MODES, default="fixed_count",
                        help="fixed_count: one fixed-work transaction per case; "
                             "gas_budget: one EIP-7904 gas-budget block per case")
    parser.add_argument("--gas-budgets", default=",".join(map(str, DEFAULT_GAS_BUDGETS)),
                        help="Block gas budgets in Mgas for --workload-mode gas_budget")
    parser.add_argument("--select", default=None,
                        help="pytest -k expression restricting the target variants "
                             "(gas_budget mode only; the inventory pins the set)")
    parser.add_argument("--analyzer-image", help="Analyzer image reference or ID (config stage)")
    parser.add_argument("--controller", default="bin/benchmarkoor", help="Controller binary path")
    parser.add_argument("--generation-cpuset", default=None,
                        help="Optional CPU affinity for generation/diagnostics; unpinned by default")
    parser.add_argument("--generation-memory", default="24g")
    parser.add_argument("--generation-jobs", type=int, default=4,
                        help="Bound on parallel fresh generation jobs (1..16)")
    parser.add_argument("--fixture-format", choices=("engine", "both"), default="engine",
                        help="Grid fixture format; engine is audited against the "
                             "dual-format inventory variant set")
    parser.add_argument("--memory", default="24g", help="Timed capture memory limit")
    parser.add_argument("--timeout", default="8h", help="Frozen campaign timeout")
    parser.add_argument("--benchmarkoor-root", type=Path,
                        default=Path(__file__).resolve().parents[2])
    parser.add_argument("--newl1-root", type=Path,
                        default=Path(__file__).resolve().parents[2] / "../bnbchain-newL1")
    parser.add_argument("--execution-specs-root", type=Path,
                        default=Path(__file__).resolve().parents[2] / "../execution-specs")
    parser.add_argument("--run", help="Run directory to audit (audit stage)")
    parser.add_argument("--recommendations", help="Recommendations output dir (audit stage)")
    parser.add_argument("--continue", dest="continue_stage", action="store_true",
                        help="Explicitly finish remaining generation shards; "
                             "failed attempts are preserved and recorded")
    args = parser.parse_args(argv)

    args.run_home = args.run_home.resolve(strict=True)
    args.benchmarkoor_root = args.benchmarkoor_root.resolve()
    args.newl1_root = args.newl1_root.resolve()
    require(re.fullmatch(r"[1-9][0-9]*(?:,[1-9][0-9]*)+", args.gas_budgets) is not None,
            f"--gas-budgets must list at least two positive Mgas values: {args.gas_budgets}")
    args.gas_budgets = [int(value) for value in args.gas_budgets.split(",")]
    require(args.gas_budgets == sorted(set(args.gas_budgets)),
            "--gas-budgets must be strictly increasing")
    require(args.select is None or args.workload_mode == "gas_budget",
            "--select restricts gas-budget campaigns only; fixed-count grids plan "
            "per-variant counts over the pinned full corpus")
    args.execution_specs_root = args.execution_specs_root.resolve()
    for image in (args.generator_image, args.worker_image):
        require(IMAGE_ID.fullmatch(image) is not None,
                f"Image must be a local immutable sha256 ID: {image}")
    if args.generation_cpuset is not None:
        require(CPU_LIST.fullmatch(args.generation_cpuset) is not None,
                f"Invalid generation CPU affinity: {args.generation_cpuset}")
        require(expand_cpuset(args.generation_cpuset) <= set(os.sched_getaffinity(0)),
                f"Generation CPUs unavailable to this process: {args.generation_cpuset}")
    for limit in (args.generation_memory, args.memory):
        require(MEMORY_SPEC.fullmatch(limit) is not None, f"Invalid memory limit: {limit}")
    require(1 <= args.generation_jobs <= 16, "Parallel generation jobs must be 1..16")
    if args.stage == "config":
        require(args.analyzer_image is not None, "--analyzer-image is required for config")
    if args.stage == "audit":
        require(args.run is not None, "--run is required for audit")
    return args


STAGE_DIRECTORIES = {"inventory": "inventory", "grids": "grids", "calibration": "calibration",
                     "assemble": "corpus", "config": "config", "audit": "audit"}
STAGE_RUNNERS = {"inventory": run_inventory, "grids": run_grids, "calibration": run_calibration,
                 "assemble": run_assemble, "config": run_config, "audit": run_audit}


def main(argv: list[str] | None = None) -> None:
    try:
        args = parse_args(argv)
    except ValueError as error:
        print(f"error: {error}", file=sys.stderr)
        raise SystemExit(2)
    name = STAGE_DIRECTORIES[args.stage]
    directory = (args.run_home / "audit" / Path(args.run).resolve().name
                 if args.stage == "audit" else args.run_home / name)
    if args.stage == "audit":
        require(args.run, "--run is required for audit")
        directory.mkdir(parents=True, exist_ok=True)
        if not (directory / "stage.json").is_file():
            save(directory / "stage.json",
                 {"stage": "audit", "started": now(), "settings": settings(args),
                  "invocation": sys.argv, "script_sha256": digest(Path(__file__).resolve())})
    else:
        directory = prepare_stage(args, name)
        try:
            if (directory / "complete.json").is_file():
                matching_settings(directory, args)
                print(f"{args.stage} already complete (artifacts verified): {directory}",
                      flush=True)
                return
        except BaseException as error:
            save(directory / "failure.json",
                 {"failed": now(), "error": str(error),
                  "traceback": traceback.format_exc()})
            raise
    try:
        STAGE_RUNNERS[args.stage](args, directory)
    except BaseException as error:
        save(directory / "failure.json",
             {"failed": now(), "error": str(error), "traceback": traceback.format_exc()})
        raise
    print(f"{args.stage} complete: {directory}", flush=True)


if __name__ == "__main__":
    main()
