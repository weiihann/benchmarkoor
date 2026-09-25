# Stage a campaign

`scripts/compute/pricing_campaign.py` builds a frozen NewL1 campaign in stages inside one run home: it exports workloads with execution-specs, checks each block on the NewL1 worker, plans the budget grid, adds the calibration lane, assembles the corpus, and writes the controller configs.

## Sub-features

- `stage-inventory` fills every selected variant at the first budget and checks each block on the worker in diagnostic mode.
- `stage-grids` builds one block per variant and budget and checks each shard against the inventory.
- `stage-calibration` builds the fixed-count calibration lane.
- `stage-assemble` writes `corpus/workload.json`, `freeze.json`, and a corpus preflight.
- `stage-config` writes `config/compute.yaml`, `config/smoke-compute.yaml`, and `config/smoke-gasfit.yaml`.
- `stage-continue` resumes with `--continue`, keeping completed stages and restarting only unfinished shards.
- `stage-identity` refuses to continue when the images, engine, budgets, or selection differ from the run home's recorded settings.

## How to get to it (user POV)

- Run `python3 scripts/compute/pricing_campaign.py <stage> --run-home <home> --generator-image <sha256> --worker-image <sha256> --engine newl1 --workload-mode gas_budget --gas-budgets 120,240,360 [--select <-k expr>] --generation-cpuset 0-13,15-29 --fixture-format engine`.
- `<stage>` is one of `inventory`, `grids`, `calibration`, `assemble`, `config`, `audit`.
- The `config` stage also needs `--analyzer-image <sha256> --controller bin/benchmarkoor --newl1-root <checkout> --cpu 14 --memory 24g`.

## Driving it with pricing_campaign.py

Preconditions:

- `scripts/doctor.sh` prints `READY`.
- Generator image built: `docker build --build-context execution-specs=../execution-specs -f Dockerfile.compute-generator -t benchmarkoor-compute-generator:verify .`.
- NewL1 worker image built as in [Run a compute campaign](./run-compute-campaign.md).
- A new, empty run home under `results/`.

- **Small inventory.** Run the `inventory` stage with `--select '(test_arithmetic and opcode_DIV-0) or test_ecrecover' --generation-jobs 16`. It prints `inventory complete`, and `inventory/coverage.json` shows `"errors": []` and `"ready": 2`.
- **Grids.** Run the `grids` stage with the same flags. It prints `grids complete`, and `grids/plan.json` lists one job per variant and budget.
- **Calibration and assemble.** Run `calibration`, then `assemble`. Both print `complete`, and `corpus/freeze.json` records 8 sessions.
- **Analysis config.** Run `docker run --rm --network none --user $(id -u):$(id -g) -v <home>:/work --entrypoint python <analyzer-image> -m evm_gasfit.recommendations create-config --client newl1 --workload /work/corpus/workload.json --out /work/analysis-gasfit.yaml`. It prints one glue-enabled model per target variant.
- **Config.** Run the `config` stage. It prints `config complete`, and `config/compute.yaml` has `engine: newl1` and `source_paths` for `benchmarkoor`, `newl1`, and `execution_specs`.
- **Resume.** Rerun any completed stage with `--continue`. It prints `<stage> already complete (artifacts verified)` and changes no files.
- **Identity guard.** Rerun a completed stage with a different `--worker-image`. It fails with `Stage identity options must stay fixed across invocations`.
- **Proof.** Copy `inventory/coverage.json`, `inventory/diagnostic-validation.json`, `grids/plan.json`, `corpus/freeze.json`, and `config/compute.yaml` into `results/verify/<run-id>/`.

## Gotchas

- Generation runs the pure-Python reference EVM. A 120M BLAKE2F or pairing block takes tens of minutes, and a full 67-variant grid takes hours. Use `--select` for verification.
- A new generator image changes the corpus identity. A run home cannot mix blocks from two images; start a new run home instead.
- Stages fail stop and never retry. Read `<stage>/failure.json` and the generator or worker log it names, fix the cause, and rerun with `--continue`.
- The fill's reference counts include the Osaka pre-block system calls (`MOD` twice per block); the diagnostic check subtracts them.
