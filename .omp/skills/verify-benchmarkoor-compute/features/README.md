# Benchmarkoor compute verification map

This directory is the maintained source for verifying the user-facing behavior of benchmarkoor's NewL1 compute pipeline. Read the index before driving, then use the matching feature file as the recipe.

## Baseline preconditions

- Work from a benchmarkoor checkout with `bin/benchmarkoor` and `benchmarkoor-compute-analyzer:verify` built by `scripts/build.sh`.
- `scripts/doctor.sh` prints `READY`.
- Sibling checkouts `../execution-specs` and a bnbchain-newL1 checkout containing `bin/newl1-bench` exist when a feature builds the generator or the worker.
- Put every run home, scratch copy, and evidence directory under a path this run created. Never reuse another campaign's run home.

## Driving conventions

- Workers run unpinned on every host CPU, which is upstream's default. Keep the host otherwise idle while a campaign times samples.
- Refer to images by local `sha256:` ID in configs and stage flags. Tags are for building only.
- Treat every command as literal. Keep flags and quoted selections unchanged.
- Never stop a container, process, or campaign this run did not start.

## Proof and skip reporting

- CLI proof includes the command, exit code, controller log, and the artifacts the command wrote.
- A campaign proof includes `samples.jsonl` row counts per phase and status, `exclusions.jsonl`, and the analysis attempt's `status.json`.
- An analysis proof includes `status.json`, `reports/analysis_status.json`, and `reports/qualification.csv`.
- Record the feature ID and entry point with every artifact under `results/verify/<run-id>/`.
- Report an unreachable path with the attempted command and the unmet precondition. Do not report a skipped entry point as verified through a different path.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the user-visible behavior. It then uses exactly four H2 sections in this order: `Sub-features`, `How to get to it (user POV)`, `Driving it with <harness>`, and `Gotchas`.

## Features

- [Reanalyze an archived run](./reanalyze-archived-run.md) covers `benchmarkoor analyze` with the archived or an overriding analysis config.
- [Run a compute campaign](./run-compute-campaign.md) covers `benchmarkoor run` capture, reconciliation, export, and analysis.
- [Stage a campaign](./staged-campaign.md) covers `pricing_campaign.py` stages from inventory to config, resume, and audit.
- [Build recommendations](./recommendations.md) covers `evm_gasfit.recommendations create-config` and `build`.
