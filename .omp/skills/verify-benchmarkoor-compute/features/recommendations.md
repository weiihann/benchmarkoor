# Build recommendations

The in-tree evm-gasfit analyzer turns a corpus into an analysis config and turns a finished analysis into per-variant gas recommendations with a policy decision for each pricing group.

## Sub-features

- `recs-create-config` writes one glue-enabled model per ready target variant plus a `.variants.json` sidecar.
- `recs-build` writes `recommendations.json` and `recommendations.csv` with one row per variant.
- `recs-policy` assigns each pricing group a decision such as `increase_candidate`, `keep_proven_adequate`, or `blocked-coverage`.

## How to get to it (user POV)

- Run `python -m evm_gasfit.recommendations create-config --client newl1 --workload <corpus>/workload.json --out <yaml>`.
- Run `python -m evm_gasfit.recommendations build --workload <corpus>/workload.json --analysis <attempt>/reports --diagnostics <run>/sessions/diagnostic-00/samples.jsonl --out <dir>`.
- Both run inside the analyzer image (`--entrypoint python`) or from the checkout with `uv run --project analyzer python -m evm_gasfit.recommendations ...`.

## Driving it with the analyzer image

Preconditions:

- `benchmarkoor-compute-analyzer:verify` is built by `scripts/build.sh`.
- A run home with `corpus/workload.json` and a finished run with an analysis attempt.

- **Create config.** Run `docker run --rm --network none --user $(id -u):$(id -g) -v <home>:/work:ro -v results/verify/<run-id>:/out --entrypoint python benchmarkoor-compute-analyzer:verify -m evm_gasfit.recommendations create-config --client newl1 --workload /work/corpus/workload.json --out /out/analysis-gasfit.yaml`. It exits 0, and `analysis-gasfit.yaml` lists `clients: [newl1]` with one model per target variant.
- **Build.** Run the image with `build --workload /work/corpus/workload.json --analysis /work/runs/<run>/analysis/<attempt>/reports --diagnostics /work/runs/<run>/sessions/diagnostic-00/samples.jsonl --out /out/recommendations`. It exits 0, and `recommendations.csv` has one row per variant in the corpus.
- **Proof.** Keep `analysis-gasfit.yaml`, its `.variants.json`, and both recommendation files under `results/verify/<run-id>/`.

## Gotchas

- `create-config` requires `--client`. The value must be `newl1`, or the controller rejects the config.
- Recommendations are provisional evidence. `policy_decision` and `missing_coverage` explain why a group is blocked; a missing price is not a failure.
- The analyzer image entrypoint is `evm-gasfit`. Override it with `--entrypoint python` for `recommendations`.
