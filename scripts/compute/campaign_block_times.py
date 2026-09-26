#!/usr/bin/env python3
"""Write per-case block execution times from a finished compute run.

Usage: campaign_block_times.py <run-dir> <out.csv>

One row per target case: the whole-block time at the newl1_block_execution
boundary over qualification samples, with the diagnostic call count. These are
block times for gas-limit questions and cross-machine comparison, not
per-operation prices: they include loop, call, and per-block overhead.
"""

from __future__ import annotations

import csv
import json
import statistics
import sys
from collections import defaultdict
from pathlib import Path


def main(run_dir: Path, out: Path) -> None:
    cases = {case["id"]: case for case in json.loads((run_dir / "workload.json").read_text())["cases"]}
    durations: dict[str, list[float]] = defaultdict(list)
    calls: dict[str, int] = {}
    for line in (run_dir / "samples.jsonl").read_text().splitlines():
        row = json.loads(line)
        case = cases[row["case_id"]]
        if case["parameters"].get("campaign_role") != "target" or row["status"] != "executed":
            continue
        if row["phase"] == "diagnostic":
            calls[row["case_id"]] = row["target_count"]
        elif row["phase"] == "qualification":
            durations[row["case_id"]].append(row["execution_duration_ns"] / 1e6)

    with out.open("w", newline="") as handle:
        writer = csv.writer(handle, lineterminator="\n")
        writer.writerow(["case_id", "target_operation", "gas_budget", "calls", "samples",
                         "median_ms", "min_ms", "max_ms", "us_per_call"])
        for case_id in sorted(durations, key=lambda c: (cases[c]["target_operation"], c)):
            times = durations[case_id]
            median = statistics.median(times)
            count = calls.get(case_id)
            writer.writerow([case_id, cases[case_id]["target_operation"],
                             cases[case_id]["parameters"]["gas_budget"], count, len(times),
                             f"{median:.3f}", f"{min(times):.3f}", f"{max(times):.3f}",
                             f"{median * 1000 / count:.3f}" if count else ""])
    print(f"wrote {out}: {len(durations)} target cases")


if __name__ == "__main__":
    main(Path(sys.argv[1]), Path(sys.argv[2]))
