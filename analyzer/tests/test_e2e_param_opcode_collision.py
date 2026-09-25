"""End-to-end regression: a parsed-param key that matches an opcode mnemonic.

Earlier the fixture-name parser emitted a bare ``SSTORE`` column whenever a
token like ``SSTORE_same`` appeared in a fixture name — the EEST key/value
heuristic falls back to partition-on-first-underscore when the value side
starts lowercase, and ``same`` is lowercase. That column then collided with
the ``SSTORE`` opcode column from the opcounts merge, leaving the per-fixture
opcount lookup looking for a missing column.

This test exercises the same shape with a token whose key happens to equal
the target opcode (``ADD_same``) and asserts the pipeline runs to completion
and exposes the param under its ``param_`` prefixed name.
"""

from __future__ import annotations

from pathlib import Path

import pandas as pd
from _data_synth import (
    ClientModel,
    base_config,
    cross_product_fixtures,
    run_pipeline,
    write_standard_inputs,
)


def test_parsed_param_does_not_collide_with_opcode_column(tmp_path: Path) -> None:
    fixtures = cross_product_fixtures(
        test_file="test_arithmetic",
        test_name="test_arithmetic",
        param_grid={"ADD": ["same", "diff"]},
        target_opcode_for="ADD",
    )
    models = {"geth": ClientModel(intercept=80.0, slope=1.2e-5)}
    config = base_config(
        models_custom=[
            {
                "test_name": "test_arithmetic",
                "target_operation": "ADD",
                "model_by": "ADD",
                "model_params": {"target_coef": "OPCODE_GENERIC"},
            }
        ],
        new_params={"OPCODE_GENERIC": None},
    )
    config_yaml, runtimes_csv, opcounts_json, out_dir = write_standard_inputs(
        tmp_path,
        fixtures=fixtures,
        models=models,
        config=config,
        noise_pct=0.003,
        seed=1,
    )

    run_pipeline(config_yaml, runtimes_csv, opcounts_json, out_dir)

    results = pd.read_csv(out_dir / "results.csv")
    assert "param_ADD" in results.columns
    assert set(results["param_ADD"]) == {"same", "diff"}
    assert "ADD_x" not in results.columns
    assert "ADD_y" not in results.columns
