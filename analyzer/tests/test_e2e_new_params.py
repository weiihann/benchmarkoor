"""End-to-end: ``new_params`` declaration, validation, and downstream flow.

Each test exercises one concern:

- declaring a new gas-param name silences the typo check on `model_params`
  RHS and the value flows through to `new_gas.csv`;
- a typo in `model_params` RHS that doesn't match any raw fork field or any
  declared new param is a hard config error;
- a declared new param that is never referenced is a hard config error;
- a new-param name that collides with an existing raw fork field is a hard
  config error and the message points at `gas_costs.overrides`.
"""

from __future__ import annotations

from pathlib import Path

import pandas as pd
import pytest
from _data_synth import (
    ClientModel,
    base_config,
    make_block_limit_fixtures,
    run_pipeline,
    write_standard_inputs,
)


def _build_inputs(
    tmp_path: Path,
    *,
    target_coef: str,
    new_params: dict[str, int | None] | None,
) -> tuple[Path, Path, Path, Path]:
    """One ``test_arithmetic`` / ``ADD`` spec writing ``target_coef``."""
    fixtures = make_block_limit_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        target_opcode="ADD",
        params={"opcode": "ADD"},
        target_opcount_per_million=500_000,
    )
    models = {
        "geth": ClientModel(intercept=80.0, slope=2.0e-5),
        "besu": ClientModel(intercept=100.0, slope=2.5e-5),
    }
    config = base_config(
        plots=False,
        new_params=new_params,
        models_custom=[
            {
                "test_name": "test_arithmetic",
                "target_operation": "ADD",
                "model_params": {"target_coef": target_coef},
            }
        ],
    )
    return write_standard_inputs(
        tmp_path,
        fixtures=fixtures,
        models=models,
        config=config,
        noise_pct=0.002,
        seed=9,
    )


def test_new_param_declared_flows_through(tmp_path: Path) -> None:
    """Declaring `MY_NEW_PARAM` in `new_params` lets a model write it without
    a typo error; the fitted value appears in `new_gas.csv`."""
    config_yaml, runtimes_csv, opcounts_json, out_dir = _build_inputs(
        tmp_path,
        target_coef="MY_NEW_PARAM",
        new_params={"MY_NEW_PARAM": None},
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    row = new_gas.loc[new_gas["gas_param"] == "MY_NEW_PARAM"]
    assert not row.empty, "expected MY_NEW_PARAM row in new_gas.csv"
    assert int(row.iloc[0]["new_gas_rounded"]) > 0


def test_undeclared_model_params_rhs_is_load_time_error(tmp_path: Path) -> None:
    """`model_params: {target_coef: MY_NEW_PARAMM}` with `new_params:
    {MY_NEW_PARAM: ...}` declared is a `ConfigError` — the typo doesn't match
    the declared name, and there's no fork field to fall back to."""
    config_yaml, _, _, _ = _build_inputs(
        tmp_path,
        target_coef="MY_NEW_PARAMM",  # typo
        new_params={"MY_NEW_PARAM": None},
    )

    from evm_gasfit import GasFit
    from evm_gasfit.errors import ConfigError

    with pytest.raises(ConfigError, match=r"MY_NEW_PARAMM"):
        GasFit.from_config(config_yaml)


def test_dead_new_param_declaration_is_load_time_error(tmp_path: Path) -> None:
    """Declaring `UNUSED_PARAM` in `new_params` without any reference from
    `model_params` or `derived` is a `ConfigError`."""
    config_yaml, _, _, _ = _build_inputs(
        tmp_path,
        target_coef="OPCODE_ADD",  # raw fork field — model_params is fine
        new_params={"UNUSED_PARAM": None},
    )

    from evm_gasfit import GasFit
    from evm_gasfit.errors import ConfigError

    with pytest.raises(ConfigError, match=r"never referenced"):
        GasFit.from_config(config_yaml)


def test_new_param_collides_with_raw_fork_field(tmp_path: Path) -> None:
    """Declaring an existing raw fork field as a new param is a `ConfigError`
    that points at `gas_costs.overrides`."""
    config_yaml, _, _, _ = _build_inputs(
        tmp_path,
        target_coef="OPCODE_ADD",
        new_params={"COLD_STORAGE_ACCESS": None},
    )

    from evm_gasfit import GasFit
    from evm_gasfit.errors import ConfigError

    with pytest.raises(ConfigError, match=r"gas_costs\.overrides"):
        GasFit.from_config(config_yaml)


def test_new_param_referenced_only_by_derived(tmp_path: Path) -> None:
    """A declared name is considered referenced when it appears as a derived
    alias RHS or formula identifier — no `model_params` reference required."""
    fixtures = make_block_limit_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        target_opcode="ADD",
        params={"opcode": "ADD"},
        target_opcount_per_million=500_000,
    )
    models = {"geth": ClientModel(intercept=80.0, slope=2.0e-5)}
    config = base_config(
        plots=False,
        new_params={"MY_NEW_PARAM": 1234},
        models_custom=[
            {
                "test_name": "test_arithmetic",
                "target_operation": "ADD",
                "model_params": {"target_coef": "OPCODE_ADD"},
            }
        ],
        extra={"derived": {"ALIAS": "MY_NEW_PARAM"}},
    )
    config_yaml, _runtimes_csv, _opcounts_json, _out_dir = write_standard_inputs(
        tmp_path,
        fixtures=fixtures,
        models=models,
        config=config,
        noise_pct=0.002,
        seed=9,
    )

    from evm_gasfit import GasFit

    # Loads without error; the derived alias keeps the new-param declaration alive.
    GasFit.from_config(config_yaml)


# ----- Phase 3 contracts: None propagation through fits and derived -------


def _build_inputs_with_missing_fit(
    tmp_path: Path,
    *,
    new_params: dict[str, int | None],
    derived: dict | None = None,
) -> tuple[Path, Path, Path, Path]:
    """One ADD model that fits + one spec referencing a missing test_name.

    The second spec has no matching fixtures, so the modeling layer skips it
    (warning only) and the proposal layer emits a placeholder row for
    `MY_UNFIT`.
    """
    fixtures = make_block_limit_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        target_opcode="ADD",
        params={"opcode": "ADD"},
        target_opcount_per_million=500_000,
    )
    models = {"geth": ClientModel(intercept=80.0, slope=2.0e-5)}
    custom = [
        {
            "test_name": "test_arithmetic",
            "target_operation": "ADD",
            "model_params": {"target_coef": "OPCODE_ADD"},
        },
        {
            "test_name": "test_does_not_exist",
            "target_operation": "ADD",
            "model_params": {"target_coef": "MY_UNFIT"},
        },
    ]
    config = base_config(
        plots=False,
        new_params=new_params,
        models_custom=custom,
        extra={"derived": derived} if derived else None,
    )
    return write_standard_inputs(
        tmp_path,
        fixtures=fixtures,
        models=models,
        config=config,
        noise_pct=0.002,
        seed=11,
    )


def test_missing_fit_emits_unresolved_placeholder_row(tmp_path: Path) -> None:
    """A proposed name with no successful fit gets a placeholder row in
    `new_gas.csv` (empty `new_gas_rounded`) and surfaces under the
    `### Missing parameters` subsection of `## Warnings`."""
    config_yaml, runtimes_csv, opcounts_json, out_dir = _build_inputs_with_missing_fit(
        tmp_path, new_params={"MY_UNFIT": None}
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    unfit = new_gas.loc[new_gas["gas_param"] == "MY_UNFIT"]
    assert not unfit.empty
    assert pd.isna(unfit.iloc[0]["new_gas_rounded"])
    assert pd.isna(unfit.iloc[0]["runtime_ms"])

    proposal = (out_dir / "new_gas_proposal.md").read_text()
    warnings_heading = proposal.find("## Warnings")
    unresolved_heading = proposal.find("### Missing parameters")
    assert warnings_heading >= 0, "Warnings section missing"
    assert unresolved_heading > warnings_heading, (
        "Missing parameters subsection must sit under Warnings"
    )
    after = proposal[unresolved_heading:]
    assert "MY_UNFIT" in after, "MY_UNFIT not listed under Missing parameters"

    # Diff table must NOT contain MY_UNFIT — fitted rows only.
    diff_table = proposal.split("## Proposed gas parameters")[1].split("##", 1)[0]
    assert "MY_UNFIT" not in diff_table


def test_derived_formula_propagates_none(tmp_path: Path) -> None:
    """A derived formula that references an unresolved name evaluates to
    `None` and surfaces in the same `### Missing parameters` subsection."""
    config_yaml, runtimes_csv, opcounts_json, out_dir = _build_inputs_with_missing_fit(
        tmp_path,
        new_params={"MY_UNFIT": None},
        derived={"DOUBLED": {"formula": "MY_UNFIT * 2"}},
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    doubled = new_gas.loc[new_gas["gas_param"] == "DOUBLED"]
    assert not doubled.empty
    assert pd.isna(doubled.iloc[0]["new_gas_rounded"])
    assert pd.isna(doubled.iloc[0]["new_gas_decimal"])

    proposal = (out_dir / "new_gas_proposal.md").read_text()
    after_unresolved = proposal[proposal.find("### Missing parameters") :]
    assert "MY_UNFIT" in after_unresolved
    assert "DOUBLED" in after_unresolved


def test_new_params_integer_value_renders_in_diff_column(tmp_path: Path) -> None:
    """An integer `new_params` value is used as the `current_gas` baseline in
    the proposal diff column, replacing the `no prior default` sentinel."""
    fixtures = make_block_limit_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        target_opcode="ADD",
        params={"opcode": "ADD"},
        target_opcount_per_million=500_000,
    )
    models = {"geth": ClientModel(intercept=80.0, slope=2.0e-5)}
    declared_baseline = 9999
    config = base_config(
        plots=False,
        new_params={"MY_NEW_WITH_VALUE": declared_baseline},
        models_custom=[
            {
                "test_name": "test_arithmetic",
                "target_operation": "ADD",
                "model_params": {"target_coef": "MY_NEW_WITH_VALUE"},
            }
        ],
    )
    config_yaml, runtimes_csv, opcounts_json, out_dir = write_standard_inputs(
        tmp_path,
        fixtures=fixtures,
        models=models,
        config=config,
        noise_pct=0.002,
        seed=23,
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    proposal = (out_dir / "new_gas_proposal.md").read_text()
    # Locate the table row for our param and verify the current_gas cell carries
    # the declared baseline value, not the sentinel.
    row_lines = [line for line in proposal.splitlines() if "MY_NEW_WITH_VALUE" in line]
    assert row_lines, "MY_NEW_WITH_VALUE missing from proposal"
    target_row = next((line for line in row_lines if "|" in line), None)
    assert target_row is not None
    assert str(declared_baseline) in target_row
    assert "no prior default" not in target_row.lower()
