---
name: verify-benchmarkoor-compute
description: Drive benchmarkoor's NewL1 Osaka compute pipeline (the `bin/benchmarkoor` run/analyze CLI, the staged `scripts/compute/pricing_campaign.py` operator, and the in-tree evm-gasfit analyzer) and capture proof. Use when verifying a change to pkg/compute, pkg/config, scripts/compute, analyzer/, or the compute Dockerfiles.
---

# Verify benchmarkoor compute

The surface is a CLI. `bin/benchmarkoor run --config <compute.yaml>` captures a
campaign, `bin/benchmarkoor analyze --run <dir>` reanalyzes an archived one,
`scripts/compute/pricing_campaign.py` stages campaigns, and the analyzer image
runs evm-gasfit. Every step runs Docker containers. The web UI and API
(`make run-ui`, `benchmarkoor api`) only display results and are not mapped here.

Paths below are relative to the repository root. Helpers live in
`.omp/skills/verify-benchmarkoor-compute/scripts/`; call them by that path.

## Launch

There is no server. Launch means building the controller and analyzer from
this checkout:

```bash
.omp/skills/verify-benchmarkoor-compute/scripts/build.sh
```

It prints `controller: benchmarkoor <commit>` and the analyzer image ID tagged
`benchmarkoor-compute-analyzer:verify`. The NewL1 worker and the workload
generator build from sibling checkouts; see `features/run-compute-campaign.md`
and `features/staged-campaign.md`.

## Doctor

```bash
.omp/skills/verify-benchmarkoor-compute/scripts/doctor.sh
```

Read-only. It prints `READY` and exits 0 only when `bin/benchmarkoor` was built
from the checked-out commit, Docker answers, the `:verify` analyzer image
exists, and no `benchmarkoor-compute-worker-*` or `benchmarkoor-compute-analyze-*`
container is running. A running compute container belongs to a campaign that
is timing samples on a pinned CPU. Wait for it. Never stop a container this
run did not start. Run the doctor first whenever anything looks off.

## Drive

Each mapped feature has a recipe in `features/`. Start at `features/README.md`.
The fastest end-to-end drive reanalyzes a copy of an archived run:

```bash
.omp/skills/verify-benchmarkoor-compute/scripts/reanalyze.sh <archived-run-dir> [analysis-config.yaml]
```

It runs the doctor, copies the run (without `analysis/`) to
`/tmp/benchmarkoor-verify/<run-id>/run`, runs `bin/benchmarkoor analyze` on the
copy under `taskset -c 0`, and never writes the source run.

## Evidence

Every drive writes to `results/verify/<run-id>/` (gitignored). The reanalyze
helper stores `command.txt`, `controller.log`, `controller.exit`, the full
attempt directory under `attempt/`, and `summary.json` with the controller
exit, attempt ID, analysis status, per-model qualification, and report list.

Proof standards:

- Drive the real CLI with real images and archived or freshly captured data.
  Do not call Go functions, Python helpers, or test fixtures in place of the CLI.
- Record the command and its result, and read the produced artifacts: the
  attempt's `status.json`, `reports/analysis_status.json`, `reports/*.csv`, or a
  run's `samples.jsonl`, `exclusions.jsonl`, and `manifest.json`.
- Check side effects, not just exit codes. A campaign writes one terminal row per
  requested sample. `analyze` adds a new `analysis/<uuid>/`, rewrites only the
  `compute.analysis` block of `config.json` to point at it, and leaves every
  other earlier file byte-identical.
- `analyze` succeeding with status `inconclusive` still exits 0. Report the
  status from `status.json`, not the exit code.

## Cleanup

```bash
.omp/skills/verify-benchmarkoor-compute/scripts/cleanup.sh <run-id>
```

It stops the controller PID this run recorded, removes only
`benchmarkoor-compute-analyze-<attempt>` containers for this run's attempts,
and deletes `/tmp/benchmarkoor-verify/<run-id>`. It keeps
`results/verify/<run-id>/`. Never run `bin/benchmarkoor cleanup` for this: it
removes every dangling benchmarkoor container on the host, including a live
campaign's.

## Helpers

| Script | Invocation | Effect |
| --- | --- | --- |
| `build.sh` | `scripts/build.sh` | `make build-core`, then builds `benchmarkoor-compute-analyzer:verify` |
| `doctor.sh` | `scripts/doctor.sh` | Read-only readiness check; exit 1 lists blockers |
| `reanalyze.sh` | `scripts/reanalyze.sh <run> [config]` | Reanalyzes a copy of an archived run; writes evidence |
| `cleanup.sh` | `scripts/cleanup.sh <run-id>` | Stops what the run started; keeps evidence |
