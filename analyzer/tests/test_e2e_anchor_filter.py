"""End-to-end: anchored ``filter_by`` tokens isolate prefix-overlap opcodes.

Several opcodes share a fixture-name prefix with another opcode in the same
test (``ADD`` vs ``ADDMOD``, ``MUL`` vs ``MULMOD``, ``PUSH0`` vs
``PUSH1``…``PUSH32``). ``filter_by=["opcode_<X>"]`` is a plain substring
match, so without a trailing anchor it would silently include the sibling
opcode's fixtures. The catalog fixes this by appending a trailing ``-``
(the token separator in EEST fixture names) to the affected presets.

This test synthesizes fixtures for both the target opcode and its overlap
sibling, drives the pipeline through the corresponding catalog preset, and
asserts the resulting ``results.csv`` row carries the *target* opcode's
slope — i.e. no leakage from the sibling.
"""

from __future__ import annotations

from pathlib import Path

import pandas as pd
import pytest
from _data_synth import (
    ClientModel,
    base_config,
    cross_product_fixtures,
    run_pipeline,
    write_config_yaml,
    write_opcounts_json,
    write_runtimes_csv,
)


@pytest.mark.parametrize(
    ("preset_name", "target_opcode", "sibling_opcode", "test_name", "test_file"),
    [
        ("arithmetic_add", "ADD", "ADDMOD", "test_arithmetic", "test_arithmetic"),
        ("arithmetic_mul", "MUL", "MULMOD", "test_arithmetic", "test_arithmetic"),
        ("stack_push0", "PUSH0", "PUSH1", "test_push", "test_push"),
    ],
    ids=["add_vs_addmod", "mul_vs_mulmod", "push0_vs_push1"],
)
def test_anchored_filter_excludes_prefix_sibling(
    tmp_path: Path,
    preset_name: str,
    target_opcode: str,
    sibling_opcode: str,
    test_name: str,
    test_file: str,
) -> None:
    """The catalog preset's anchored filter_by must not leak sibling fixtures."""
    # Two synthetic sweeps under the SAME test_name, one per opcode.
    target_fixtures = cross_product_fixtures(
        test_file=test_file,
        test_name=test_name,
        param_grid={"opcode": [target_opcode]},
        target_opcode_for=target_opcode,
    )
    sibling_fixtures = cross_product_fixtures(
        test_file=test_file,
        test_name=test_name,
        param_grid={"opcode": [sibling_opcode]},
        target_opcode_for=sibling_opcode,
    )

    target_slope = 1.0e-5
    sibling_slope = 5.0e-5  # deliberately distinct
    intercept = 90.0

    runtimes_csv = tmp_path / "runtimes.csv"
    opcounts_json = tmp_path / "opcounts.json"
    config_yaml = tmp_path / "config.yaml"
    out_dir = tmp_path / "out"

    # Generate runtimes from two different true models so leakage would be visible.
    target_model = {"geth": ClientModel(intercept=intercept, slope=target_slope)}
    sibling_model = {"geth": ClientModel(intercept=intercept, slope=sibling_slope)}
    write_runtimes_csv(
        runtimes_csv.with_suffix(".target.csv"),
        target_fixtures,
        target_model,
        noise_pct=0.002,
        seed=31,
    )
    write_runtimes_csv(
        runtimes_csv.with_suffix(".sibling.csv"),
        sibling_fixtures,
        sibling_model,
        noise_pct=0.002,
        seed=32,
    )
    merged = pd.concat(
        [
            pd.read_csv(runtimes_csv.with_suffix(".target.csv")),
            pd.read_csv(runtimes_csv.with_suffix(".sibling.csv")),
        ],
        ignore_index=True,
    )
    merged.to_csv(runtimes_csv, index=False)

    write_opcounts_json(opcounts_json, target_fixtures + sibling_fixtures)
    config = base_config(
        models_custom=[], models_presets=[preset_name], clients=("geth",)
    )
    write_config_yaml(config_yaml, config)

    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    results = pd.read_csv(out_dir / "results.csv")
    # Only the target opcode should appear — the anchored filter must not
    # admit the sibling's fixtures into the slice.
    assert set(results["target_opcode"]) == {target_opcode}, (
        f"results.csv leaked sibling fixtures: got opcodes {set(results['target_opcode'])}"
    )

    row = results.iloc[0]
    recovered = float(row["target_coef_runtime_ms"])
    assert recovered == pytest.approx(target_slope, rel=0.05), (
        f"{preset_name}: recovered slope {recovered} not ~{target_slope}; "
        f"sibling {sibling_opcode}'s slope was {sibling_slope}"
    )
    # The visible cross-contamination guard: had the sibling leaked, the fit
    # would pull toward the sibling's slope, which is 5x the target's.
    assert recovered < sibling_slope / 2.0


def test_negation_token_excludes_overlapping_sibling(tmp_path: Path) -> None:
    """A ``!``-prefixed filter_by token excludes fixtures whose names contain
    its substring, even when a sibling positive token would otherwise admit
    them. This is the alternative to a trailing-dash anchor when the anchor
    isn't usable — e.g. an opcode whose token has no natural delimiter to
    distinguish it from a longer-named sibling."""
    target_opcode, sibling_opcode = "ADD", "ADDMOD"
    target_fixtures = cross_product_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        param_grid={"opcode": [target_opcode]},
        target_opcode_for=target_opcode,
    )
    sibling_fixtures = cross_product_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        param_grid={"opcode": [sibling_opcode]},
        target_opcode_for=sibling_opcode,
    )

    target_slope = 1.0e-5
    sibling_slope = 5.0e-5
    intercept = 90.0

    runtimes_csv = tmp_path / "runtimes.csv"
    opcounts_json = tmp_path / "opcounts.json"
    config_yaml = tmp_path / "config.yaml"
    out_dir = tmp_path / "out"

    target_model = {"geth": ClientModel(intercept=intercept, slope=target_slope)}
    sibling_model = {"geth": ClientModel(intercept=intercept, slope=sibling_slope)}
    write_runtimes_csv(
        runtimes_csv.with_suffix(".target.csv"),
        target_fixtures,
        target_model,
        noise_pct=0.002,
        seed=41,
    )
    write_runtimes_csv(
        runtimes_csv.with_suffix(".sibling.csv"),
        sibling_fixtures,
        sibling_model,
        noise_pct=0.002,
        seed=42,
    )
    merged = pd.concat(
        [
            pd.read_csv(runtimes_csv.with_suffix(".target.csv")),
            pd.read_csv(runtimes_csv.with_suffix(".sibling.csv")),
        ],
        ignore_index=True,
    )
    merged.to_csv(runtimes_csv, index=False)
    write_opcounts_json(opcounts_json, target_fixtures + sibling_fixtures)

    # `opcode_ADD` (positive) matches both ADD and ADDMOD fixtures; the
    # `!opcode_ADDMOD` negation token must exclude the ADDMOD ones.
    config = base_config(
        models_custom=[
            {
                "test_name": "test_arithmetic",
                "target_operation": target_opcode,
                "filter_by": ["opcode_ADD", "!opcode_ADDMOD"],
                "model_params": {"target_coef": "OPCODE_ADD"},
            }
        ],
        clients=("geth",),
    )
    write_config_yaml(config_yaml, config)

    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    results = pd.read_csv(out_dir / "results.csv")
    assert set(results["target_opcode"]) == {target_opcode}, (
        f"results.csv leaked sibling fixtures: {set(results['target_opcode'])}"
    )
    recovered = float(results.iloc[0]["target_coef_runtime_ms"])
    assert recovered == pytest.approx(target_slope, rel=0.05)
    # Had the negation token been ignored, the fit would pull toward the
    # sibling's slope (5x the target's).
    assert recovered < sibling_slope / 2.0
