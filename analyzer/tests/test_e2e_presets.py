"""End-to-end: model-preset registry behavior.

Pins:

- a preset-only config drives a full pipeline run;
- ``presets`` and ``custom`` concatenate (no merge logic, both contribute);
- unknown preset names are a hard config error;
- both lists empty is a hard config error;
- a ``test_name`` listed in both ``presets`` and ``custom`` is allowed
  (independent fits; the aggregator handles the collision).

The canonical preset under test is ``arithmetic_add`` (test_name
``test_arithmetic``, target opcode ``ADD``, writes ``OPCODE_ADD``).
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
    write_config_yaml,
    write_standard_inputs,
)


def _add_fixtures():
    return make_block_limit_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        target_opcode="ADD",
        params={"opcode": "ADD"},
    )


def test_preset_only_config_runs_pipeline(tmp_path: Path) -> None:
    fixtures = _add_fixtures()
    models = {"geth": ClientModel(intercept=80.0, slope=1.0e-5)}
    config = base_config(models_custom=[], models_presets=["arithmetic_add"])
    config_yaml, runtimes_csv, opcounts_json, out_dir = write_standard_inputs(
        tmp_path, fixtures=fixtures, models=models, config=config, seed=11
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    results = pd.read_csv(out_dir / "results.csv")
    matching = results[
        (results["test_name"] == "test_arithmetic")
        & (results["target_opcode"] == "ADD")
    ]
    assert len(matching) >= 1, "preset arithmetic_add should produce a results.csv row"

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    assert "OPCODE_ADD" in set(new_gas["gas_param"])


def test_preset_plus_custom_concatenate(tmp_path: Path) -> None:
    add_fixtures = _add_fixtures()
    sub_fixtures = make_block_limit_fixtures(
        test_file="test_sub_arithmetic",
        test_name="test_sub_arithmetic",
        target_opcode="SUB",
        params={"opcode": "SUB"},
    )
    models = {"geth": ClientModel(intercept=70.0, slope=1.2e-5)}
    config = base_config(
        models_presets=["arithmetic_add"],
        models_custom=[
            {
                "test_name": "test_sub_arithmetic",
                "target_operation": "SUB",
                "model_params": {"target_coef": "OPCODE_SUB"},
            }
        ],
    )
    config_yaml, runtimes_csv, opcounts_json, out_dir = write_standard_inputs(
        tmp_path,
        fixtures=add_fixtures + sub_fixtures,
        models=models,
        config=config,
        seed=12,
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    gas_params = set(new_gas["gas_param"])
    assert "OPCODE_ADD" in gas_params, "preset contribution missing from new_gas.csv"
    assert "OPCODE_SUB" in gas_params, "custom contribution missing from new_gas.csv"


@pytest.mark.parametrize(
    "models_section",
    [
        {"presets": ["definitely_not_a_real_preset"], "custom": []},
        {"presets": [], "custom": []},
    ],
    ids=["unknown_preset_name", "empty_presets_and_empty_custom"],
)
def test_invalid_models_section_is_config_error(
    tmp_path: Path, models_section: dict
) -> None:
    config_yaml = tmp_path / "config.yaml"
    config = base_config()
    config["models"] = models_section
    write_config_yaml(config_yaml, config)

    from evm_gasfit import GasFit
    from evm_gasfit.errors import ConfigError

    with pytest.raises(ConfigError):
        GasFit.from_config(config_yaml)


def test_duplicate_test_name_across_preset_and_custom_is_allowed(
    tmp_path: Path,
) -> None:
    fixtures = _add_fixtures()
    models = {"geth": ClientModel(intercept=80.0, slope=1.0e-5)}
    config = base_config(
        models_presets=["arithmetic_add"],
        models_custom=[
            {
                "test_name": "test_arithmetic",
                "target_operation": "ADD",
                "model_params": {"target_coef": "OPCODE_ADD_ALT"},
            }
        ],
    )
    config_yaml, runtimes_csv, opcounts_json, out_dir = write_standard_inputs(
        tmp_path, fixtures=fixtures, models=models, config=config, seed=13
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    gas_params = set(new_gas["gas_param"])
    assert "OPCODE_ADD" in gas_params
    assert "OPCODE_ADD_ALT" in gas_params
