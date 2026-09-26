# Osaka compute campaigns

A compute campaign exports fixed-work or gas-budget Osaka workloads through
execution-specs, executes them with NewL1, archives every sample, and fits the
results with the in-tree evm-gasfit analyzer (`analyzer/`). The pipeline does
not reimplement workload generation or EVM execution.

The campaign is compute-only. It does not change gas constants, recommend a
block gas limit, or price memory, storage, state growth, or network capacity.

## Eligible workloads

The allowlist contains Osaka-compatible cases in these families:

- arithmetic, including operand-sensitive variants
- bitwise and comparison
- stack and control flow
- KECCAK256
- precompiles, including Osaka P256VERIFY at address `0x100`

A campaign may select a subset. Dedicated memory, storage, account-access,
contract-lifecycle, block-access-list, scenario, stateful, and post-Osaka cases
are excluded. A selected case that cannot produce a fixed-work native workload
remains in `workload.json` with `status: unsupported` and a reason.

Supporting calibration uses an explicit `parameters.campaign_role: calibration`
lane and a separate generator allowlist. It does not expand the priced target
allowlist. Targets and supporting cases share the frozen capture schedule, but
calibration rows are excluded from target-pricing models.

`KECCAK256` is the canonical operation name. Precompile counts use
`PRECOMPILE_<20-byte-address>` keys, so P256VERIFY is counted as
`PRECOMPILE_0x0000000000000000000000000000000000000100` rather than as an
aggregate `STATICCALL` count.

The generator uses one transaction allowance for every count point. The default
is `15_000_000`. Osaka's EIP-7825 transaction cap rejects values above `2^24`.
The allowance is a generation bound, not the observed target count.

## Build inputs

Use pinned sibling checkouts for the worker and generator. The analyzer builds
from this repository:

| Image | Dockerfile | Build input |
| --- | --- | --- |
| NewL1 worker | `Dockerfile.compute-worker-newl1` | Primary context: a bnbchain-newL1 checkout containing `bin/newl1-bench` |
| workload generator | `Dockerfile.compute-generator` | Named context `execution-specs=../execution-specs` |
| analyzer | `Dockerfile.compute-analyzer` | This repository's `analyzer/` |

Build the images from the selected worktrees:

```bash
docker build \
  -f Dockerfile.compute-worker-newl1 \
  -t benchmarkoor-compute-worker-newl1:local ../bnbchain-newL1

docker build \
  --build-context execution-specs=../execution-specs \
  -f Dockerfile.compute-generator \
  -t benchmarkoor-compute-generator:local .

docker build \
  -f Dockerfile.compute-analyzer \
  -t benchmarkoor-compute-analyzer:local .
```

`source_paths` in the campaign configuration records revisions, dirty-state
evidence, and dependency lock hashes for benchmarkoor (which includes the
analyzer), NewL1, and execution-specs. Retain patches and untracked files
separately when a source worktree is dirty.

## Run a campaign

The example files define an anchorless ADD and KECCAK256 control campaign:

```bash
benchmarkoor build --config examples/configuration/compute.yaml
benchmarkoor run --config examples/configuration/compute.yaml
```

The generator invokes `fill --fork Osaka` with the configured test paths,
selection expression, deterministic seed, fixed target-count grid, family
allowlist, and transaction gas cap.

The worker protocol is versioned under `pkg/compute/schema/`. A request names a
workload, session, mode, and frozen list of samples. The engine's worker flushes
one terminal JSONL record per requested sample.

`compute.resource_limits` resolves CPUs exactly as benchmark instances do.
Without `cpuset` or `cpuset_count`, the worker runs unpinned on every host CPU,
which is the default. `cpuset_count` picks random CPUs that satisfy
`cpuset_topology`. Compute campaigns reject `cpu_freq`, `cpu_turboboost`, and
`cpu_freq_governor`, because their sessions do not apply them. Keep the host
otherwise idle while a campaign times samples.

Omitting `compute.analyzer` makes a capture-only campaign. Every session runs
and every sample is archived, but no analysis follows. Use it for block-time
measurements that no pricing fit consumes, such as timing one block size on
another machine. `benchmarkoor analyze` rejects a capture-only run.

## Engines and measurement boundaries

`compute.engine` selects the worker and fixes the boundary every result must
report. `newl1` is the only engine. A row with any other boundary is an
accounting error.

| Engine | Worker | Boundary | Timed unit |
| --- | --- | --- | --- |
| `newl1` | `newl1-bench` (`Dockerfile.compute-worker-newl1`) | `newl1_block_execution` | One NewL1 production block (`NewL1EvmBlockExecution::execute`): all of the case's transactions, the Parlia system tail, and the LtHash commitment |

The boundary excludes workload parsing and prestate preparation, baseline
restoration, EVM construction, signer recovery, correctness checks, and artifact
hashing and serialization. The NewL1 worker signs real EIP-1559 transactions from
each transaction's `secret_key` (EEST test keys) and runs its bench-only genesis
at Osaka. Production NewL1 fork configuration is untouched.

Diagnostic samples run with an inspector and report opcode counts plus addressed
precompile invocations. Pilot, warmup, and qualification samples run without the
inspector and must report a positive `execution_duration_ns`. NewL1 counts only
the opcodes of the case's own transactions: system calls are per-block overhead
inside the timer, not workload. Like the fill's reference trace, it counts the
opcode that exhausts a transaction's gas.

Each qualification session uses a fresh worker process. The worker restores the
prepared baseline for every sample. Diagnostic and pilot requests use separate
processes and do not warm qualification sessions.

## Gas-budget blocks

`fill --gas-benchmark-values` exports EIP-7904's layout: each target case is one
block whose gas budget EEST splits into 2^24-gas transactions (one sender, or one
per transaction for uncachable variants)
(`workload_mode: gas_budget`, `gas_budget`, `tx_count`; IDs end in
`-benchmark-gas-value_<N>M`). The transactions run until their gas is exhausted,
so the reference oracle expects failed receipts. `scripts/compute/pricing_campaign.py
--workload-mode gas_budget --gas-budgets 120,240,...` plans one generation job per
variant and budget; the calibration lane stays fixed-count. Diagnostic target
counts must match the fill's reference trace exactly in both modes, after
removing what the Osaka pre-block system calls execute: the fill counts the whole
block, and the EIP-4788 and EIP-2935 contracts each run one `MOD`.

## Correctness and failures

The execution-specs export supplies expected transaction success, logs, and
storage witnesses. The worker checks those outcomes after the timed region.
Only a result with `status: executed` and `correctness_passed: true` is eligible
for analysis.

A failed sample remains in `samples.jsonl` with an error stage and message. A
worker crash or missing terminal record is reconciled into an explicit failure;
it is never replaced by a successful retry under the same sample identifier.
Unsupported cases also remain visible and carry their generator reason.

## Analysis

Benchmarkoor converts archived executed rows into evm-gasfit's runtimes CSV and
opcode-count JSON without mutating the raw workload or sample artifacts. The
example analysis policy selects the Osaka gas table and accepts only correct
qualification rows.

Qualification uses independent session identifiers for cluster bootstrap and
held-out-session checks. The analysis status is `succeeded`, `inconclusive`, or
`failed`; an inconclusive fit does not produce a recommended price when
`block_unqualified` is enabled.

The default example is anchorless. It compares measured slopes with the Osaka
gas table but does not claim an official calibration. Pricing scenarios require
an explicit positive gas-per-second anchor and margin policy.

The coordinated pricing operator is `scripts/compute/pricing_campaign.py`.
It stages inventory, supporting calibration, target count grids, corpus assembly,
and capture configuration. `scripts/compute/run_newl1_campaign.sh` drives every
stage through capture, recommendations, and audit in one resumable command. It
requires at least five gas budgets, because glue detection needs five block
sizes per model. The gasfit `recommendations` CLI creates the model
configuration and builds per-variant and per-group pricing evidence from archived
reports. It reads the analysis's embedded, hash-verified configuration rather than
following an old container-local path.

For block times alone, `scripts/compute/extract_block_corpus.py` takes one gas
budget's target cases from a finished corpus, and
`scripts/compute/run_newl1_block_times.sh` times them in a capture-only campaign
with the pricing session layout. `scripts/compute/campaign_block_times.py` writes
the per-case block-time table for either kind of run.

Isolated-cost and whole-workload-budget recommendations have separate coverage
and qualification decisions. A raw workload slope is not an isolated opcode
cost, and a candidate supported by only some variants is not a complete
group-wide recommendation.

## Artifacts

Each run retains:

- `workload.json`
- frozen diagnostic, pilot, warmup, and qualification requests
- `samples.jsonl`
- `manifest.json`
- `config.json`
- analyzer input snapshots and their hashes
- evm-gasfit reports under `analysis/<attempt-id>/reports/`

The manifest separates comparison factors from run provenance. Campaign
comparison is allowed only when workload, boundary, hardware, gas schedule, and
analysis method are complete and equal; software must be the only differing
factor.

## Verification

Run the focused contract tests:

```bash
go test -tags \
  'exclude_graphdriver_btrfs,exclude_graphdriver_devicemapper,containers_image_openpgp' \
  ./pkg/config ./pkg/compute
```

The `Check - Osaka Compute` workflow runs these tests plus the analyzer's tests
and lint whenever compute sources change.
