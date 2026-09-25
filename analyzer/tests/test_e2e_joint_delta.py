"""End-to-end: joint worst-case pricing for access deltas.

The state-access write cost (``STORAGE_WRITE`` / ``ACCOUNT_WRITE``) is never
charged without its cold access. Pricing it as an independent per-param
worst-case overshoots, because ``max`` is subadditive: the worst-access client
and the worst-write client can differ. Instead the catalog fits the *combined*
cold-write cost as its own single-coefficient param (``COLD_STORAGE_WRITE`` /
``COLD_ACCOUNT_*_WRITE``) and recovers the delta with a derived subtraction,
floored at zero.

These tests drive the full pipeline on synthetic clients whose worst access and
worst combined-write deliberately come from different clients / contexts, and
assert the derived delta is the tight ``max(0, combined − access)`` rather than
the looser per-client ``max(combined − access)``.
"""

from __future__ import annotations

from pathlib import Path

import pandas as pd
from _data_synth import (
    ClientModel,
    base_config,
    make_block_limit_fixtures,
    make_glue_driver_fixtures,
    run_pipeline,
    write_standard_inputs,
)

# anchor_rate=1e8 ⇒ gas = 1e8 · coef_ms_per_op / 1000 = 1e5 · coef. So a planted
# slope of 0.02 ms/op surfaces as ≈2000 gas.


def _rounded(new_gas: pd.DataFrame, param: str) -> int:
    row = new_gas.loc[new_gas["gas_param"] == param]
    assert not row.empty, f"{param} missing from new_gas.csv"
    return int(row.iloc[0]["new_gas_rounded"])


def _sstore_fixtures():
    """Read (write_new_value=False) and write (write_new_value=True) fixtures.

    ``w`` mirrors write_new_value as a numeric flag so a ClientModel can plant a
    different slope on the write subset via ``extra_coefs``.
    """
    reads = make_block_limit_fixtures(
        test_file="test_sstore",
        test_name="test_sstore",
        target_opcode="SSTORE",
        params={"write_new_value": "False", "w": "0"},
    )
    writes = make_block_limit_fixtures(
        test_file="test_sstore",
        test_name="test_sstore",
        target_opcode="SSTORE",
        params={"write_new_value": "True", "w": "1"},
    )
    return reads + writes


_SSTORE_ACCESS_SPEC = {
    "test_name": "test_sstore",
    "target_operation": "SSTORE",
    "filter_by": ["write_new_value_False"],
    "model_params": {"target_coef": "COLD_STORAGE_ACCESS"},
}
_SSTORE_WRITE_SPEC = {
    "test_name": "test_sstore",
    "target_operation": "SSTORE",
    "filter_by": ["write_new_value_True"],
    "model_params": {"target_coef": "COLD_STORAGE_WRITE"},
}
# COLD_STORAGE_ACCESS and COLD_STORAGE_WRITE are raw osaka fields, so neither
# needs a new_params declaration; STORAGE_WRITE is purely derived.
_STORAGE_DERIVED = {"STORAGE_WRITE": "max(0, COLD_STORAGE_WRITE - COLD_STORAGE_ACCESS)"}


def test_storage_write_joint_is_tighter_than_independent_max(tmp_path: Path) -> None:
    # geth: worst access (2000) but small write delta (→2500).
    # besu: lower access (1500) but worst combined write (2800).
    # Independent max would publish STORAGE_WRITE = max(500, 1300) = 1300.
    # Joint pricing publishes max(0, 2800 − 2000) = 800.
    models = {
        "geth": ClientModel(intercept=50.0, slope=0.020, extra_coefs={"w": 0.005}),
        "besu": ClientModel(intercept=50.0, slope=0.015, extra_coefs={"w": 0.013}),
    }
    config = base_config(
        models_custom=[_SSTORE_ACCESS_SPEC, _SSTORE_WRITE_SPEC],
        extra={"derived": _STORAGE_DERIVED},
    )
    paths = write_standard_inputs(
        tmp_path,
        fixtures=_sstore_fixtures(),
        models=models,
        config=config,
        noise_pct=0.0005,
    )
    run_pipeline(*paths)
    out_dir = paths[3]

    new_gas = pd.read_csv(out_dir / "new_gas.csv")
    ca = _rounded(new_gas, "COLD_STORAGE_ACCESS")
    cw = _rounded(new_gas, "COLD_STORAGE_WRITE")
    sw = _rounded(new_gas, "STORAGE_WRITE")

    # Published params land near their planted worst-case values.
    assert 1950 <= ca <= 2150
    assert 2750 <= cw <= 2950
    # The derived delta is exactly the clamped joint subtraction.
    assert sw == max(0, cw - ca)

    # ... and strictly tighter than the naive per-client independent max.
    all_params = pd.read_csv(out_dir / "new_gas_all_params.csv")
    winners = all_params[all_params["is_winner"]]
    naive = 0
    for client in models:
        sub = winners[winners["client_name"] == client]
        w = sub.loc[sub["gas_param"] == "COLD_STORAGE_WRITE", "new_gas_rounded"]
        a = sub.loc[sub["gas_param"] == "COLD_STORAGE_ACCESS", "new_gas_rounded"]
        if not w.empty and not a.empty:
            naive = max(naive, int(w.iloc[0]) - int(a.iloc[0]))
    assert sw < naive, f"joint delta {sw} should be below naive max-delta {naive}"


def test_joint_delta_survives_glue_adjustment(tmp_path: Path) -> None:
    # Regression: the read (write_new_value_False) and write (write_new_value_True)
    # specs share (test_name, target, model_by) and differ only in filter_by. The
    # glue-adjustment lookup must key on source_label — otherwise the write spec
    # inherits the read spec's (lower) adjusted coefficient and the delta
    # collapses to 0 whenever glue is enabled.
    models = {
        "geth": ClientModel(intercept=50.0, slope=0.020, extra_coefs={"w": 0.030}),
        "besu": ClientModel(intercept=50.0, slope=0.015, extra_coefs={"w": 0.020}),
    }
    config = base_config(
        models_custom=[_SSTORE_ACCESS_SPEC, _SSTORE_WRITE_SPEC],
        glue_enabled=True,
        extra={"derived": _STORAGE_DERIVED},
    )
    paths = write_standard_inputs(
        tmp_path,
        fixtures=_sstore_fixtures() + make_glue_driver_fixtures(),
        models=models,
        config=config,
        noise_pct=0.0005,
    )
    run_pipeline(*paths, glue=True)
    new_gas = pd.read_csv(paths[3] / "new_gas.csv")

    ca = _rounded(new_gas, "COLD_STORAGE_ACCESS")
    cw = _rounded(new_gas, "COLD_STORAGE_WRITE")
    sw = _rounded(new_gas, "STORAGE_WRITE")
    # The write fit is genuinely far above the access fit; the delta must
    # survive the glue adjustment rather than collapsing to 0.
    assert cw > ca
    assert sw == max(0, cw - ca)
    assert sw > 0


def test_storage_write_clamps_to_zero(tmp_path: Path) -> None:
    # Combined write measures *below* the cold access for every client (here via
    # a negative synthetic write coef), so the delta floors at zero rather than
    # going negative.
    models = {
        "geth": ClientModel(intercept=50.0, slope=0.020, extra_coefs={"w": -0.005}),
        "besu": ClientModel(intercept=50.0, slope=0.018, extra_coefs={"w": -0.003}),
    }
    config = base_config(
        models_custom=[_SSTORE_ACCESS_SPEC, _SSTORE_WRITE_SPEC],
        extra={"derived": _STORAGE_DERIVED},
    )
    paths = write_standard_inputs(
        tmp_path,
        fixtures=_sstore_fixtures(),
        models=models,
        config=config,
        noise_pct=0.0005,
    )
    run_pipeline(*paths)
    new_gas = pd.read_csv(paths[3] / "new_gas.csv")
    assert _rounded(new_gas, "STORAGE_WRITE") == 0


def _account_fixtures():
    """Four single-client groups: {nocode, code} × {read, write}.

    ``value_sent`` is the read/write selector *and* the write-delta multiplier;
    ``cflag`` lifts the code-context access cost; ``cw`` adds the extra
    code-write premium so the two contexts have different write deltas.
    """
    out = []
    for test_name, cflag in (("test_acct_nocode", "0"), ("test_acct_code", "1")):
        cw_write = "1" if cflag == "1" else "0"
        out += make_block_limit_fixtures(
            test_file=test_name,
            test_name=test_name,
            target_opcode="BALANCE",
            params={"value_sent": "0", "cflag": cflag, "cw": "0"},
        )
        out += make_block_limit_fixtures(
            test_file=test_name,
            test_name=test_name,
            target_opcode="BALANCE",
            params={"value_sent": "1", "cflag": cflag, "cw": cw_write},
        )
    return out


def test_account_write_takes_worst_context(tmp_path: Path) -> None:
    # nocode: access 1000, combined 1500 → delta 500.
    # code:   access 2000, combined 3000 → delta 1000 (the worst).
    # ACCOUNT_WRITE = max(0, code_delta, nocode_delta) = 1000.
    models = {
        "geth": ClientModel(
            intercept=50.0,
            slope=0.010,
            extra_coefs={"value_sent": 0.005, "cflag": 0.010, "cw": 0.005},
        )
    }

    def spec(test_name: str, sent: str, param: str) -> dict:
        return {
            "test_name": test_name,
            "target_operation": "BALANCE",
            "filter_by": [f"value_sent_{sent}"],
            "model_params": {"target_coef": param},
        }

    specs = [
        spec("test_acct_nocode", "0", "COLD_ACCOUNT_NOCODE_ACCESS"),
        spec("test_acct_nocode", "1", "COLD_ACCOUNT_NOCODE_WRITE"),
        spec("test_acct_code", "0", "COLD_ACCOUNT_CODE_ACCESS"),
        spec("test_acct_code", "1", "COLD_ACCOUNT_CODE_WRITE"),
    ]
    config = base_config(
        models_custom=specs,
        new_params={
            "COLD_ACCOUNT_NOCODE_ACCESS": None,
            "COLD_ACCOUNT_NOCODE_WRITE": None,
            "COLD_ACCOUNT_CODE_ACCESS": None,
            "COLD_ACCOUNT_CODE_WRITE": None,
        },
        extra={
            "derived": {
                "ACCOUNT_WRITE": (
                    "max(0, COLD_ACCOUNT_CODE_WRITE - COLD_ACCOUNT_CODE_ACCESS, "
                    "COLD_ACCOUNT_NOCODE_WRITE - COLD_ACCOUNT_NOCODE_ACCESS)"
                )
            }
        },
    )
    paths = write_standard_inputs(
        tmp_path,
        fixtures=_account_fixtures(),
        models=models,
        config=config,
        noise_pct=0.0005,
    )
    run_pipeline(*paths)
    new_gas = pd.read_csv(paths[3] / "new_gas.csv")

    code_delta = _rounded(new_gas, "COLD_ACCOUNT_CODE_WRITE") - _rounded(
        new_gas, "COLD_ACCOUNT_CODE_ACCESS"
    )
    nocode_delta = _rounded(new_gas, "COLD_ACCOUNT_NOCODE_WRITE") - _rounded(
        new_gas, "COLD_ACCOUNT_NOCODE_ACCESS"
    )
    aw = _rounded(new_gas, "ACCOUNT_WRITE")
    assert aw == max(0, code_delta, nocode_delta)
    assert aw == code_delta  # code context is the worst
    assert 950 <= aw <= 1050
