# Run a compute campaign

`benchmarkoor run --config <compute.yaml>` executes a frozen NewL1 workload in fresh worker containers, one session at a time, reconciles every requested sample into an archive, exports evm-gasfit inputs, and analyzes them.

## Sub-features

- `run-plan` writes one `diagnostic-00`, N `pilot-NN`, and N `qualification-NN` sessions with frozen `request.json` files.
- `run-capture` runs each worker container unpinned on every host CPU (upstream's default; `cpuset` or `cpuset_count` pins it) and records `samples.jsonl`, `container.log`, and `exit.json` per session.
- `run-reconcile` writes `samples.jsonl` with one terminal row per requested sample and lists every non-qualifying row in `exclusions.jsonl`.
- `run-manifest` records images by digest, host facts, ordering, and source provenance for `benchmarkoor`, `newl1`, and `execution_specs`.
- `run-analyze` exports `runtimes.csv` and `opcounts.json` and runs the analyzer into `analysis/<uuid>/`.
- `run-reject` refuses invalid configs, such as an engine other than `newl1` or an analysis config whose `clients` lacks `newl1`.

## How to get to it (user POV)

- Run `bin/benchmarkoor run --config <home>/config/smoke-compute.yaml` for a short campaign.
- Run `bin/benchmarkoor run --config <home>/config/compute.yaml` for the full campaign.
- Both configs come from the staged `config` step in [Stage a campaign](./staged-campaign.md).

## Driving it with the benchmarkoor CLI

Preconditions:

- `scripts/doctor.sh` prints `READY`.
- A run home with a finished `config` stage, whose `config/smoke-compute.yaml` names local `sha256:` image IDs.
- The NewL1 worker image is built: `docker build -f Dockerfile.compute-worker-newl1 -t benchmarkoor-compute-worker-newl1:verify <bnbchain-newL1 checkout>`.

- **Smoke capture.** Run `bin/benchmarkoor run --config <home>/config/smoke-compute.yaml 2>&1 | tee results/verify/<run-id>/run.log`. The log ends with `Compute campaign artifacts retained run_dir=<home>/runs/compute-<uuid>`.
- **Sample accounting.** Count rows with `python3 -c "import json,collections,sys; print(collections.Counter((r['phase'],r['status']) for r in map(json.loads,open(sys.argv[1]))))" <run_dir>/samples.jsonl`. Every requested sample in `requested-samples.jsonl` has exactly one row, and correct rows are `executed`.
- **Provenance.** Read `<run_dir>/manifest.json`. `provenance` has `benchmarkoor`, `newl1`, and `execution_specs`, and `boundary` is `newl1_block_execution`.
- **Analysis.** Read `<run_dir>/analysis/<uuid>/status.json`. `status` is `succeeded` or `inconclusive`, and `reports/qualification.csv` has one row per target model.
- **Rejection.** Copy the config, set `engine: evm2`, and point `analyzer.config` at a gasfit YAML with `clients: [evm2]`. Run `bin/benchmarkoor run --config <copy>`. It exits 1 with `compute.engine: invalid value "evm2" (must be "newl1")` before starting any container.
- **Proof.** Copy `run.log`, the run's `samples.jsonl`, `exclusions.jsonl`, `manifest.json`, and `analysis/<uuid>/status.json` into `results/verify/<run-id>/`.

## Gotchas

- The smoke capture of a two-variant corpus takes about 16 minutes, and a full 67-variant campaign takes hours. Pick the smallest corpus that exercises the change.
- `run` stops with a fatal log when analysis fails, even though capture finished. The capture artifacts are still retained; read them before rerunning.
- `run` never resumes. A second invocation creates a new `compute-<uuid>` directory.
- Timings are only measurements when nothing else runs on the host. A verification capture proves correctness, not cost.
- Never run `bin/benchmarkoor cleanup` to clear leftovers. It removes other campaigns' containers.
