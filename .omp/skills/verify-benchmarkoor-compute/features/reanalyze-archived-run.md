# Reanalyze an archived run

`benchmarkoor analyze` creates a new, immutable evm-gasfit analysis attempt for a campaign that was already captured, without running any worker. It uses the analysis config and analyzer image archived with the run unless the user overrides the config.

## Sub-features

- `analyze-archived` reanalyzes with the run's own `analysis-config.yaml`.
- `analyze-override` reanalyzes with `--analysis-config <yaml>`.
- `analyze-status` records `succeeded`, `inconclusive`, or `failed` in the attempt's `status.json` and in the run's `config.json`.
- `analyze-immutable` adds `analysis/<uuid>/`, points `config.json`'s `compute.analysis` at it, and leaves every other earlier run file unchanged.

## How to get to it (user POV)

- Run `bin/benchmarkoor analyze --run <run-dir>`.
- Run `bin/benchmarkoor analyze --run <run-dir> --analysis-config <yaml>`.

## Driving it with reanalyze.sh

Preconditions:

- `scripts/doctor.sh` prints `READY`.
- An archived NewL1 run directory whose `campaign.json` names `"engine": "newl1"`, for example a `results/<home>/runs/compute-<uuid>/` directory from a finished campaign.

- **Archived config.** Run `.omp/skills/verify-benchmarkoor-compute/scripts/reanalyze.sh <run-dir>`. `summary.json` shows `controller_exit` 0 and `analysis_status` `succeeded` or `inconclusive`, and `reports` lists `analysis_status.json`, `qualification.csv`, and `results.csv`.
- **Override config.** Run `.omp/skills/verify-benchmarkoor-compute/scripts/reanalyze.sh <run-dir> <yaml>`. `command.txt` contains `--analysis-config`, and `attempt/config.yaml` equals the override file byte for byte.
- **Failure path.** Override with a config whose model counts a key missing from `opcounts.json`. `summary.json` shows `analysis_status` `failed`, and `attempt/analyzer.log` names the missing count column.
- **Immutability.** Before cleanup, run `diff -r --exclude analysis --exclude config.json <run-dir> /tmp/benchmarkoor-verify/<run-id>/run`. There is no output. `diff <run-dir>/config.json /tmp/benchmarkoor-verify/<run-id>/run/config.json` differs only in `compute.analysis`, which names the new attempt ID.
- **Proof.** Keep `results/verify/<run-id>/` with `summary.json`, `attempt/status.json`, and `attempt/reports/analysis_status.json`.

## Gotchas

- `analyze` runs the analyzer image recorded in the run's `campaign.json`, not this checkout's `:verify` image. To prove a change under `analyzer/`, capture a campaign with the new image or use [Build recommendations](./recommendations.md).
- The analyzer container inherits the run's archived `resource_limits`. Runs captured with the default are unpinned, so reanalysis competes with any campaign timing on the host, which is why the doctor refuses while a compute container runs.
- `inconclusive` exits 0. Read `analysis_status`, not the exit code.
- Runs whose `campaign.json` lacks `engine` are rejected (`names no engine`). Only runs made by an engine-aware controller can be reanalyzed.
- Running `analyze` directly on a real run home appends an attempt to it. Use the helper, which works on a copy.
