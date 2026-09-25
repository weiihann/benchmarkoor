"""Unit tests for the ``derived`` formula mini-language.

Focus on the ``max``/``min`` extension: clamping, variadic/nested forms,
``None`` propagation, identifier discovery, and rejection of anything outside
the whitelist.
"""

from __future__ import annotations

import pytest

from evm_gasfit.errors import ConfigError
from evm_gasfit.proposal.derived import evaluate, names_referenced, parse_formula


def _eval(formula: str, env: dict[str, int | float | None]) -> float | None:
    return evaluate(parse_formula(formula), env)


def test_max_clamps_to_zero() -> None:
    assert _eval("max(0, a - b)", {"a": 3, "b": 10}) == 0.0
    assert _eval("max(0, a - b)", {"a": 12, "b": 10}) == 2.0


def test_max_is_variadic() -> None:
    env = {"a": 10, "b": 3, "c": 2, "d": 9}
    # max(0, a-b, c-d) == max(0, 7, -7) == 7
    assert _eval("max(0, a - b, c - d)", env) == 7.0


def test_nested_max_matches_flat_max() -> None:
    env = {"a": 10, "b": 3, "c": 2, "d": 9}
    flat = _eval("max(0, a - b, c - d)", env)
    nested = _eval("max(0, max(a - b, c - d))", env)
    assert flat == nested


def test_min() -> None:
    assert _eval("min(a, b)", {"a": 5, "b": 2}) == 2.0


def test_none_propagates_through_call() -> None:
    # An unfitted input (None) makes the whole formula resolve to None rather
    # than raising, mirroring BinOp null-propagation.
    assert _eval("max(0, a - b)", {"a": None, "b": 1}) is None


def test_names_referenced_descends_into_args() -> None:
    refs = names_referenced(parse_formula("max(0, a - b, c)"))
    assert set(refs) == {"a", "b", "c"}


def test_arithmetic_still_works() -> None:
    assert _eval("(a + b) * 4800 / 5000", {"a": 2000, "b": 3000}) == 4800.0


@pytest.mark.parametrize(
    "formula",
    [
        "abs(a)",  # function not in the whitelist
        "max()",  # no positional args
        "max(a, key=b)",  # keywords rejected
        "max(*a)",  # starred args rejected
    ],
)
def test_rejected_calls(formula: str) -> None:
    with pytest.raises(ConfigError):
        parse_formula(formula)
