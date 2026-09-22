# Osaka evm2 compute campaigns

A compute campaign exports fixed-work Osaka workloads through execution-specs,
executes them with evm2, archives every sample, and invokes evm-gasfit. The
pipeline does not reimplement workload generation, EVM execution, or modeling.

The campaign is compute-only. It does not change gas constants, recommend a
block gas limit, or price memory, storage, state growth, or network capacity.

For pinned sources, fresh-generation recipes, new-machine setup, and remaining
glue-pricing work, see the [self-contained handoff document](compute-handoff.md).

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

`KECCAK256` is the canonical operation name. Precompile counts use
`PRECOMPILE_<20-byte-address>` keys, so P256VERIFY is counted as
`PRECOMPILE_0x0000000000000000000000000000000000000100` rather than as an
aggregate `STATICCALL` count.

The generator uses one transaction allowance for every count point. The default
is `15_000_000`. Osaka's EIP-7825 transaction cap rejects values above `2^24`.
The allowance is a generation bound, not the observed target count.

## Build inputs

Use pinned sibling checkouts:

| Image | Dockerfile | Build input |
| --- | --- | --- |
| evm2 worker | `Dockerfile.compute-worker` | Primary context `../evm2` |
| workload generator | `Dockerfile.compute-generator` | Named context `execution-specs=../execution-specs` |
| analyzer | `Dockerfile.compute-analyzer` | Named context `evm-gasfit=../evm-gasfit` |

Build the images from the selected worktrees:

```bash
docker build \
  -f Dockerfile.compute-worker \
  -t benchmarkoor-compute-worker:local ../evm2

docker build \
  --build-context execution-specs=../execution-specs \
  -f Dockerfile.compute-generator \
  -t benchmarkoor-compute-generator:local .

docker build \
  --build-context evm-gasfit=../evm-gasfit \
  -f Dockerfile.compute-analyzer \
  -t benchmarkoor-compute-analyzer:local .
```

`source_paths` in the campaign configuration records revisions, dirty-state
evidence, and dependency lock hashes for benchmarkoor, evm2, execution-specs,
and evm-gasfit. Retain patches and untracked files separately when a source
worktree is dirty.

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
workload, session, mode, and frozen list of samples. `evm2-bench` flushes one
terminal JSONL record per requested sample.

## Measurement boundary

The `evm2_transaction_execution` timer covers each transaction's validation,
execution, settlement, and state commit through evm2. It excludes:

- workload parsing and prestate preparation
- baseline cloning
- EVM construction
- correctness checks
- artifact hashing and serialization

Diagnostic samples run with an inspector and report opcode counts plus addressed
precompile invocations. Pilot, warmup, and qualification samples run without the
inspector and must report a positive `execution_duration_ns`.

Each qualification session uses a fresh worker process. The worker restores the
prepared baseline for every sample. Diagnostic and pilot requests use separate
processes and do not warm qualification sessions.

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

Run the real finite cross-repository smoke:

```bash
scripts/compute/smoke.sh
```

The script builds the three pinned local images, exports twenty ADD and
KECCAK256 cases, runs diagnostic and timed evm2 samples, invokes evm-gasfit, and
checks the resulting archive. The manual `Check - Osaka evm2 Compute` workflow
accepts immutable `evm2_ref`, `execution_specs_ref`, and `evm_gasfit_ref` commit
SHAs and retains the smoke artifacts.
