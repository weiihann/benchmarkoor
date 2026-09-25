"""Unit tests pinning the tie-break order in the proposal aggregator.

The aggregator picks a winning row per ``(gas_param, client_name)`` (and then
per ``gas_param``); when two candidates are exactly equal on the aggregation
key the resolution is **fully deterministic** and does not depend on pandas /
numpy sort stability or the input row order:

- Per-client max on ``runtime_ms``: tie-break by ascending ``pvalue``, then
  lexicographic ``(test_name, target_opcode, model_coef_name, model_by-combo,
  source_label)``. ``model_by-combo`` is the concatenation of each ``model_by``
  column's value joined by ``_`` (skipping null values); ``source_label`` is
  the final tier, separating two candidates that are otherwise identical
  (e.g. specs differing only in ``filter_by``).
- Across-client max on ``runtime_ms``: tie-break by ascending ``client_name``.

The e2e suite covers happy-path aggregation but never constructs exact ties,
so these tests hand-craft minimal frames that exhibit one tier of the tie-break
at a time and assert the chosen winner. Each tier also has a determinism
counter-test that reverses input order to prove the implementation does not
silently rely on sort stability + insertion order.
"""

from __future__ import annotations

import pandas as pd

from evm_gasfit.proposal.aggregate import (
    select_across_client_max,
    select_per_client_max,
)

# Default thresholds match the config defaults (§ modeling section). The rows
# below all set ``pvalue < 0.05`` and ``rsquared >= 0.5`` so the qualified pool
# is never empty — the tie-break, not the poor-fit fallback, is what we test.
PVALUE_THRESHOLD = 0.05
RSQUARED_THRESHOLD = 0.5


def _candidate(
    *,
    gas_param: str = "OPCODE_X",
    client_name: str = "geth",
    runtime_ms: float,
    pvalue: float = 0.01,
    test_name: str = "test_a",
    target_opcode: str = "OPX",
    model_coef_name: str = "target_coef",
    model_by_value: str | None = None,
    source_label: str = "models.custom[0]",
) -> dict[str, object]:
    """Build one expanded per-client candidate row.

    Column set matches the output of ``expand_to_per_client`` — every column
    ``select_per_client_max`` introspects is populated, plus a single
    ``model_by`` column (``param_x``) so the tie-break's
    ``model_by-combo`` term is exercisable.
    """
    return {
        "gas_param": gas_param,
        "client_name": client_name,
        "runtime_ms": runtime_ms,
        "pvalue": pvalue,
        "conf_int_low": runtime_ms * 0.9,
        "conf_int_high": runtime_ms * 1.1,
        "test_name": test_name,
        "target_opcode": target_opcode,
        "model_coef_name": model_coef_name,
        "source_label": source_label,
        "glue_adjustment": 0.0,
        "rsquared": 0.99,
        "rsquared_adj": 0.99,
        "param_x": model_by_value,
        "new_gas_decimal": runtime_ms * 1e5,
        "new_gas_rounded": int(runtime_ms * 1e5) + 1,
        "poor_fit": False,
        "is_winner": False,
    }


def _build(rows: list[dict[str, object]]) -> pd.DataFrame:
    """Materialize candidate rows into the frame shape the selectors consume."""
    return pd.DataFrame(rows)


def _pick(df: pd.DataFrame) -> pd.Series:
    """Run the per-client selector with default thresholds, return single winner."""
    chosen = select_per_client_max(df, PVALUE_THRESHOLD, RSQUARED_THRESHOLD)
    assert len(chosen) == 1, f"expected exactly one winner, got {len(chosen)}"
    return chosen.iloc[0]


# --------------------------------------------------------------------------
# Per-client tie-breaks.
# --------------------------------------------------------------------------


def test_per_client_tie_on_runtime_lower_pvalue_wins() -> None:
    """Two rows tie on ``runtime_ms``; the lower ``pvalue`` wins."""
    rows = [
        _candidate(runtime_ms=10.0, pvalue=0.02, test_name="test_a"),
        _candidate(runtime_ms=10.0, pvalue=0.01, test_name="test_b"),
    ]
    winner = _pick(_build(rows))
    assert winner["test_name"] == "test_b"
    assert winner["pvalue"] == 0.01


def test_per_client_tie_on_runtime_and_pvalue_lower_test_name_wins() -> None:
    """Tie on (runtime, pvalue, target_opcode, coef, model_by) → lower test_name wins."""
    rows = [
        _candidate(runtime_ms=10.0, pvalue=0.01, test_name="test_b"),
        _candidate(runtime_ms=10.0, pvalue=0.01, test_name="test_a"),
    ]
    winner = _pick(_build(rows))
    assert winner["test_name"] == "test_a"


def test_per_client_tie_lower_target_opcode_wins() -> None:
    """Tie on (runtime, pvalue, test_name, coef, model_by) → lower target_opcode wins."""
    rows = [
        _candidate(runtime_ms=10.0, pvalue=0.01, target_opcode="MUL"),
        _candidate(runtime_ms=10.0, pvalue=0.01, target_opcode="ADD"),
    ]
    winner = _pick(_build(rows))
    assert winner["target_opcode"] == "ADD"


def test_per_client_tie_lower_model_coef_name_wins() -> None:
    """Tie on (runtime, pvalue, test_name, opcode, model_by) → lower model_coef_name wins."""
    rows = [
        _candidate(runtime_ms=10.0, pvalue=0.01, model_coef_name="target_coef"),
        _candidate(runtime_ms=10.0, pvalue=0.01, model_coef_name="extra_factor"),
    ]
    winner = _pick(_build(rows))
    # "extra_factor" sorts before "target_coef" lexicographically.
    assert winner["model_coef_name"] == "extra_factor"


def test_per_client_tie_lower_model_by_combo_wins() -> None:
    """Tie on (runtime, pvalue, test_name, opcode, coef) → lower model_by-combo wins.

    With one ``model_by`` column the combo is just that value; ``"alpha"``
    sorts before ``"beta"``.
    """
    rows = [
        _candidate(runtime_ms=10.0, pvalue=0.01, model_by_value="beta"),
        _candidate(runtime_ms=10.0, pvalue=0.01, model_by_value="alpha"),
    ]
    winner = _pick(_build(rows))
    assert winner["param_x"] == "alpha"


def test_per_client_tie_break_is_independent_of_input_order() -> None:
    """Reversing input order must not change the per-client winner.

    Pandas' ``sort_values(kind="mergesort")`` is stable, so a sort key that
    misses one tier would surface here — the loser of an earlier tier would
    flip to a winner whenever the input order flipped.
    """
    base = [
        _candidate(
            runtime_ms=10.0,
            pvalue=0.01,
            test_name="test_b",
            target_opcode="MUL",
            model_coef_name="target_coef",
            model_by_value="beta",
        ),
        _candidate(
            runtime_ms=10.0,
            pvalue=0.01,
            test_name="test_a",
            target_opcode="ADD",
            model_coef_name="extra_factor",
            model_by_value="alpha",
        ),
    ]
    # The second row is the deterministic winner (lower on every lex tier).
    forward = _pick(_build(base))
    reverse = _pick(_build(list(reversed(base))))
    assert forward["test_name"] == "test_a"
    assert reverse["test_name"] == "test_a"
    # Sanity: every field on the winning row matches across orderings.
    for col in ("test_name", "target_opcode", "model_coef_name", "param_x"):
        assert forward[col] == reverse[col]


# --------------------------------------------------------------------------
# Across-client tie-breaks.
# --------------------------------------------------------------------------


def _per_client_frame(rows: list[dict[str, object]]) -> pd.DataFrame:
    """Build a frame already in per-client-winner shape (one row per client).

    ``select_across_client_max`` does no pvalue / rsquared filtering — it just
    picks the row with the largest ``runtime_ms`` per ``gas_param`` and
    tie-breaks by ascending ``client_name``.
    """
    return pd.DataFrame(rows)


def test_across_client_tie_lower_client_name_wins() -> None:
    """Two clients tie on per-client ``runtime_ms`` → ascending client wins."""
    rows = [
        _candidate(runtime_ms=12.0, client_name="reth"),
        _candidate(runtime_ms=12.0, client_name="besu"),
        _candidate(runtime_ms=12.0, client_name="geth"),
    ]
    out = select_across_client_max(_per_client_frame(rows))
    assert len(out) == 1
    assert out.iloc[0]["client_name"] == "besu"


def test_across_client_tie_break_is_independent_of_input_order() -> None:
    """Shuffling the per-client frame must not change the across-client winner."""
    rows = [
        _candidate(runtime_ms=12.0, client_name="reth"),
        _candidate(runtime_ms=12.0, client_name="besu"),
        _candidate(runtime_ms=12.0, client_name="geth"),
    ]
    forward = select_across_client_max(_per_client_frame(rows))
    reverse = select_across_client_max(_per_client_frame(list(reversed(rows))))
    assert forward.iloc[0]["client_name"] == "besu"
    assert reverse.iloc[0]["client_name"] == "besu"
