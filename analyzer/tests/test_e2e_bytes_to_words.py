"""End-to-end: the ``bytes_to_words`` fixture-param transform.

Pins the per-spec derived-column transform:

- A spec may declare ``fixture_params: {<derived>: {source: <raw>, transform:
  bytes_to_words}}`` to convert a byte-sized raw param into a per-word value
  (``ceil(x / 32)``) before it is fed to the regression.
- The fitted coefficient on the derived column then directly recovers the
  per-word gas slope, no post-hoc division needed.
- ``transform`` and ``values`` are mutually exclusive.
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
    write_standard_inputs,
)


def test_bytes_to_words_transform_recovers_per_word_coefficient(tmp_path: Path) -> None:
    """A size sweep fit with ``transform: bytes_to_words`` recovers the per-word slope.

    Synthetic model: ``runtime = intercept + slope * opcount +
    per_word_coef * opcount * (size / 32)``. With ``size`` always a multiple
    of 32, ``ceil(size/32) == size/32`` and the recovered ``size_words``
    coefficient should match ``per_word_coef`` directly.
    """
    sizes = ("32", "64", "96", "128")
    fixtures = cross_product_fixtures(
        test_file="test_calldatacopy_from_origin",
        test_name="test_calldatacopy_from_origin",
        param_grid={"calldata_size": list(sizes)},
        target_opcode_for="CALLDATACOPY",
        target_opcount_per_million=200_000,
    )
    true_slope = 1.0e-5
    true_per_word_coef = 4.0e-6
    # The synthesizer multiplies `extra_coefs[param] * opcount * float(param_value)`.
    # We want runtime to grow with `per_word_coef * opcount * (size / 32)`,
    # i.e. `(per_word_coef / 32) * opcount * size`.
    models = {
        "geth": ClientModel(
            intercept=80.0,
            slope=true_slope,
            extra_coefs={"calldata_size": true_per_word_coef / 32.0},
        )
    }
    config = base_config(
        models_custom=[
            {
                "test_name": "test_calldatacopy_from_origin",
                "target_operation": "CALLDATACOPY",
                "fixture_params": {
                    "calldata_words": {
                        "source": "calldata_size",
                        "transform": "bytes_to_words",
                    }
                },
                "model_params": {
                    "target_coef": "OPCODE_CALLDATACOPY_BASE",
                    "calldata_words": "OPCODE_CALLDATACOPY_PER_WORD",
                },
            }
        ],
        new_params={"OPCODE_CALLDATACOPY_PER_WORD": None},
    )
    config_yaml, runtimes_csv, opcounts_json, out_dir = write_standard_inputs(
        tmp_path,
        fixtures=fixtures,
        models=models,
        config=config,
        noise_pct=0.002,
        seed=21,
    )
    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    results = pd.read_csv(out_dir / "results.csv")
    assert len(results) == 1, "expected one fit across the whole size sweep"
    row = results.iloc[0]

    assert float(row["target_coef_runtime_ms"]) == pytest.approx(true_slope, rel=0.05)
    assert "calldata_words_runtime_ms" in results.columns
    assert float(row["calldata_words_runtime_ms"]) == pytest.approx(
        true_per_word_coef, rel=0.05
    )

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    proposed = set(new_gas["gas_param"])
    assert {"OPCODE_CALLDATACOPY_BASE", "OPCODE_CALLDATACOPY_PER_WORD"}.issubset(
        proposed
    )


def test_transform_and_values_are_mutually_exclusive(tmp_path: Path) -> None:
    """A spec that declares both ``transform`` and ``values`` is a config error."""
    fixtures = cross_product_fixtures(
        test_file="test_calldatacopy_from_origin",
        test_name="test_calldatacopy_from_origin",
        param_grid={"calldata_size": ["32", "64"]},
        target_opcode_for="CALLDATACOPY",
    )
    models = {"geth": ClientModel(intercept=80.0, slope=1.0e-5)}
    config = base_config(
        models_custom=[
            {
                "test_name": "test_calldatacopy_from_origin",
                "target_operation": "CALLDATACOPY",
                "fixture_params": {
                    "calldata_words": {
                        "source": "calldata_size",
                        "transform": "bytes_to_words",
                        "values": {"32": 1, "64": 2},
                    }
                },
                "model_params": {
                    "target_coef": "OPCODE_CALLDATACOPY_BASE",
                    "calldata_words": "OPCODE_CALLDATACOPY_PER_WORD",
                },
            }
        ],
        new_params={"OPCODE_CALLDATACOPY_PER_WORD": None},
    )
    config_yaml, _, _, _ = write_standard_inputs(
        tmp_path, fixtures=fixtures, models=models, config=config, seed=22
    )

    from evm_gasfit import GasFit
    from evm_gasfit.errors import ConfigError

    with pytest.raises((ConfigError, Exception)) as exc_info:
        GasFit.from_config(config_yaml)
    assert "transform" in str(exc_info.value) and "values" in str(exc_info.value)
