"""Loader for the opcode counts JSON input."""

from __future__ import annotations

import json
from pathlib import Path

from evm_gasfit.errors import ConfigError


def load_opcounts(path: Path) -> dict[str, dict[str, float]]:
    """Load the opcode counts JSON.

    Args:
        path: Path to a JSON file mapping fixture name to a per-opcode count
            dict. Each inner dict must contain an ``opcount`` key (the
            target-opcode count) and may contain per-opcode mnemonic keys.

    Returns:
        Mapping ``fixture_name -> {opcode_or_opcount: count_as_float}``.

    Raises:
        ConfigError: If the file is missing, the JSON is malformed, or any
            fixture's inner dict lacks an ``opcount`` key.
    """
    if not path.exists():
        raise ConfigError(f"opcounts JSON not found: {path}")
    try:
        raw = json.loads(path.read_text())
    except json.JSONDecodeError as exc:
        raise ConfigError(f"opcounts JSON {path} is not valid JSON: {exc}") from exc
    if not isinstance(raw, dict):
        raise ConfigError(
            f"opcounts JSON {path} must be a JSON object at the top level"
        )

    out: dict[str, dict[str, float]] = {}
    for fixture, entry in raw.items():
        if not isinstance(entry, dict):
            raise ConfigError(
                f"opcounts JSON {path}: fixture {fixture!r} does not map to an object"
            )
        if "opcount" not in entry:
            raise ConfigError(
                f"opcounts JSON {path}: fixture {fixture!r} is missing 'opcount' key. "
                "This usually means upstream title parsing failed or the opcode/trace "
                "join produced NaN during the fetch step; check the fetch output's "
                "meta.json 'opcount_coverage' field (with_opcount / without_opcount)."
            )
        out[fixture] = {k: float(v) for k, v in entry.items()}
    return out
