#!/usr/bin/env python3
"""Write the target cases of one gas budget from a campaign corpus.

Usage: extract_block_corpus.py <corpus-workload.json> <gas-budget-Mgas> <out.json>

Keeps the workload envelope (fork, generator identity, schema version) and
drops calibration cases and every other budget, so one block size can be timed
by a capture-only campaign on another machine with byte-identical blocks.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path


def main(corpus: Path, budget_mgas: int, out: Path) -> None:
    workload = json.loads(corpus.read_text())
    gas = budget_mgas * 10**6
    cases = [case for case in workload["cases"]
             if case["parameters"].get("campaign_role") == "target"
             and case["parameters"].get("gas_budget") == gas]
    if not cases:
        sys.exit(f"no target cases with gas_budget {gas} in {corpus}")
    with out.open("x", encoding="utf-8") as stream:
        json.dump({**workload, "cases": cases}, stream, separators=(",", ":"))
    print(f"wrote {out}: {len(cases)} target cases at {budget_mgas}M from {len(workload['cases'])}")


if __name__ == "__main__":
    main(Path(sys.argv[1]), int(sys.argv[2]), Path(sys.argv[3]))
