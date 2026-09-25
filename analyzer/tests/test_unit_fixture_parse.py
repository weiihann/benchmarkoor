"""Unit tests for the fixture-name parser.

Pins the acceptance requirement that the legacy ``file.py__test[...]`` form and
the pytest node-ID ``path/file.py::test[...]`` form parse to identical
``(test_name, params)``.
"""

from __future__ import annotations

import pytest

from evm_gasfit.io.fixtures import parse_fixture_name


def test_legacy_and_node_id_forms_agree() -> None:
    params = "account_mode_EXISTING_EOA-opcode_CALL"
    legacy = parse_fixture_name(
        f"test_account_query.py__test_codecopy_benchmark[{params}]"
    )
    node_id = parse_fixture_name(
        f"benchmark/compute/instruction/test_account_query.py::test_codecopy_benchmark[{params}]"
    )

    assert legacy["test_name"] == node_id["test_name"] == "test_codecopy_benchmark"
    assert legacy["params"] == node_id["params"]
    assert legacy["params"] == {"account_mode": "EXISTING_EOA", "opcode": "CALL"}


@pytest.mark.parametrize(
    ("fixture_name", "expected_name", "expected_params"),
    [
        (
            "test_account_query.py__test_codecopy_benchmark[account_mode_EXISTING_EOA-opcode_CALL]",
            "test_codecopy_benchmark",
            {"account_mode": "EXISTING_EOA", "opcode": "CALL"},
        ),
        (
            (
                "benchmark/compute/instruction/test_account_query.py::test_codecopy_benchmark"
                "[account_mode_EXISTING_EOA-opcode_CALL]"
            ),
            "test_codecopy_benchmark",
            {"account_mode": "EXISTING_EOA", "opcode": "CALL"},
        ),
        # No params bracket (node-ID form) parses without error.
        ("path/test_foo.py::test_bar", "test_bar", {}),
    ],
)
def test_parse_variants(
    fixture_name: str,
    expected_name: str,
    expected_params: dict[str, str],
) -> None:
    parsed = parse_fixture_name(fixture_name)
    assert parsed["test_name"] == expected_name
    assert parsed["params"] == expected_params
