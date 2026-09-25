"""End-to-end: determinism contract.

Given identical inputs and the same ``modeling.random_seed``, every CSV and
markdown artifact must be byte-identical across runs and across platforms.
PNG figures are excluded — matplotlib embeds non-semantic metadata that the
contract does not promise to pin.

Three angles covered:

- Two runs with the same seed produce byte-identical CSV/MD outputs (glue off).
- Two runs with different seeds disagree on at least one bootstrap-derived
  column (CIs, p-values). Point coefficients and R² are bootstrap-independent.
- Two runs with the same seed and glue enabled also produce byte-identical
  outputs, covering the glue CSVs and report.
"""

from __future__ import annotations

import re
from pathlib import Path

import pandas as pd
from _data_synth import (
    ALWAYS_ON_ARTIFACTS,
    GLUE_ARTIFACTS,
    ClientModel,
    base_config,
    make_block_limit_fixtures,
    make_glue_driver_fixtures,
    run_pipeline,
    write_config_yaml,
    write_opcounts_json,
    write_runtimes_csv,
)


def _main_fixtures():
    return make_block_limit_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        target_opcode="ADD",
        params={"opcode": "ADD"},
        extra_opcount_per_million={"POP": 500_000},
    )


def _two_client_models():
    return {
        "geth": ClientModel(intercept=100.0, slope=1.0e-5),
        "besu": ClientModel(intercept=120.0, slope=1.5e-5),
    }


# `new_gas_proposal.md` embeds a wall-clock `_Generated YYYY-MM-DD HH:MM:SSZ`
# header so two runs spanning a second boundary diverge in that one line. The
# determinism contract is about *computed* content, not wall-clock metadata,
# so normalize the timestamp before byte-comparing markdown artifacts.
_TIMESTAMP_RE = re.compile(rb"_Generated \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}Z")


def _normalize_for_compare(name: str, data: bytes) -> bytes:
    if name.endswith(".md"):
        return _TIMESTAMP_RE.sub(b"_Generated <TIMESTAMP>", data)
    return data


def _assert_bytes_equal(out_a: Path, out_b: Path, artifacts: tuple[str, ...]) -> None:
    for name in artifacts:
        a = _normalize_for_compare(name, (out_a / name).read_bytes())
        b = _normalize_for_compare(name, (out_b / name).read_bytes())
        assert a == b, f"{name} differs between runs (len {len(a)} vs {len(b)})"


def _run_with_seed(
    tmp_path: Path,
    label: str,
    *,
    seed: int,
    runtimes_csv: Path,
    opcounts_json: Path,
    glue: bool,
    clients: tuple[str, ...] = ("geth", "besu"),
) -> Path:
    out_dir = tmp_path / f"out_{label}"
    config_yaml = tmp_path / f"config_{label}.yaml"
    write_config_yaml(
        config_yaml,
        base_config(seed=seed, glue_enabled=glue, clients=clients),
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir, glue=glue)
    return out_dir


def test_two_runs_with_same_seed_produce_byte_identical_csvs_and_md(
    tmp_path: Path,
) -> None:
    fixtures = _main_fixtures()
    runtimes_csv = tmp_path / "runtimes.csv"
    opcounts_json = tmp_path / "opcounts.json"
    write_runtimes_csv(
        runtimes_csv, fixtures, _two_client_models(), noise_pct=0.005, seed=42
    )
    write_opcounts_json(opcounts_json, fixtures)

    out_a = _run_with_seed(
        tmp_path,
        "a",
        seed=42,
        runtimes_csv=runtimes_csv,
        opcounts_json=opcounts_json,
        glue=False,
    )
    out_b = _run_with_seed(
        tmp_path,
        "b",
        seed=42,
        runtimes_csv=runtimes_csv,
        opcounts_json=opcounts_json,
        glue=False,
    )
    _assert_bytes_equal(out_a, out_b, ALWAYS_ON_ARTIFACTS)


def test_different_seed_produces_different_bootstrap_outputs(tmp_path: Path) -> None:
    fixtures = _main_fixtures()
    runtimes_csv = tmp_path / "runtimes.csv"
    opcounts_json = tmp_path / "opcounts.json"
    write_runtimes_csv(
        runtimes_csv, fixtures, _two_client_models(), noise_pct=0.01, seed=42
    )
    write_opcounts_json(opcounts_json, fixtures)

    out_42 = _run_with_seed(
        tmp_path,
        "42",
        seed=42,
        runtimes_csv=runtimes_csv,
        opcounts_json=opcounts_json,
        glue=False,
    )
    out_99 = _run_with_seed(
        tmp_path,
        "99",
        seed=99,
        runtimes_csv=runtimes_csv,
        opcounts_json=opcounts_json,
        glue=False,
    )

    r42 = (
        pd.read_csv(out_42 / "results.csv")
        .sort_values("client_name")
        .reset_index(drop=True)
    )
    r99 = (
        pd.read_csv(out_99 / "results.csv")
        .sort_values("client_name")
        .reset_index(drop=True)
    )
    assert list(r42["client_name"]) == list(r99["client_name"])

    bootstrap_cols = (
        "target_coef_conf_int_low",
        "target_coef_conf_int_high",
        "target_coef_pvalue",
    )
    differs = any(not r42[col].equals(r99[col]) for col in bootstrap_cols)
    assert differs, (
        f"expected at least one of {bootstrap_cols} to differ between seed=42 and seed=99"
    )


def test_glue_on_pipeline_is_also_deterministic(tmp_path: Path) -> None:
    all_fixtures = _main_fixtures() + make_glue_driver_fixtures()
    runtimes_csv = tmp_path / "runtimes.csv"
    opcounts_json = tmp_path / "opcounts.json"
    write_runtimes_csv(
        runtimes_csv,
        all_fixtures,
        {"geth": ClientModel(intercept=50.0, slope=2.0e-5)},
        noise_pct=0.003,
        seed=42,
    )
    write_opcounts_json(opcounts_json, all_fixtures)

    out_a = _run_with_seed(
        tmp_path,
        "a",
        seed=42,
        runtimes_csv=runtimes_csv,
        opcounts_json=opcounts_json,
        glue=True,
        clients=("geth",),
    )
    out_b = _run_with_seed(
        tmp_path,
        "b",
        seed=42,
        runtimes_csv=runtimes_csv,
        opcounts_json=opcounts_json,
        glue=True,
        clients=("geth",),
    )
    _assert_bytes_equal(out_a, out_b, ALWAYS_ON_ARTIFACTS + GLUE_ARTIFACTS)
