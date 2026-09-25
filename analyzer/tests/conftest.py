"""Pytest configuration for the e2e suite.

Shared fixtures live here. The e2e tests rely on a temporary output directory
per test (the standard `tmp_path` from pytest) and import helpers from
`_data_synth.py` directly. No package-state is shared.
"""

from __future__ import annotations

import sys
from pathlib import Path

# Make `_data_synth` importable from test modules without a package shim.
sys.path.insert(0, str(Path(__file__).parent))
