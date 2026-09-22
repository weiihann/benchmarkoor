# Prague compute campaigns

A compute campaign records a reproducible execution experiment. Benchmarkoor generates the workload through the pinned execution-specs tool, executes it through NewL1's native worker, retains every terminal result, and invokes the pinned evm-gasfit CLI. It does not implement a second workload generator, execution engine, or cost model.

This guide covers the Prague compute milestone only. It does not change production gas constants, recommend a block limit, or price memory, storage, state growth, or network capacity.

## What a campaign measures

The eligible corpus is the complete set of existing Prague-compatible benchmark cases in these families:

- arithmetic, including operand-sensitive variants
- bitwise and comparison
- stack and control flow
- KECCAK256
- precompiles

A campaign may select a narrower explicit subset, but cannot expand this list. Dedicated memory, storage, account-access, contract-lifecycle, stateful, post-Prague, and otherwise unsupported cases are excluded from this milestone. The workload package retains an unsupported case with its status and reason. It must not silently disappear from the exported corpus.

`KECCAK256` is the operation name throughout this pipeline. For a precompile, the target count is the number of invocations of one addressed precompile, recorded as `PRECOMPILE_<20-byte-address>` in `opcode_counts`. It is not the aggregate number of `STATICCALL` instructions.

`fixed_opcode_count` requests **thousands of target operations**, not gas. The diagnostic `opcount` is the native target-operation counter and includes fixed target work from the wrapper or control workload. A zero point is therefore a valid setup-control case, not evidence that its native target count is zero; the current ADD and KECCAK256 controls demonstrate this. Include at least one positive point, and use the native diagnostic count rather than inferring counts from the requested grid.

Every exported transaction uses one fixed generator allowance. Its default and recommended campaign value is `15_000_000`; it does not scale with target count. NewL1 rejects a generator cap above its native `2^24` transaction limit. The configured allowance remains a workload-generation bound, not the measured operation count.

## Prerequisites and pinned build inputs

Use checked-out sibling repositories. The worker uses NewL1 as its primary Docker build context because NewL1's repository ignore rules apply to a named context. The generator and analyzer retain their named contexts:

| Image | Dockerfile | Build input |
| --- | --- | --- |
| Native worker | `Dockerfile.compute-worker` | Primary context `../bnbchain-newL1` |
| Workload generator | `Dockerfile.compute-generator` | Named context `execution-specs=../execution-specs` |
| Analyzer | `Dockerfile.compute-analyzer` | Named context `evm-gasfit=../evm-gasfit` |

Before building an experiment, choose and record an immutable commit SHA for benchmarkoor, NewL1, execution-specs, and evm-gasfit. Do not replace unpublished local work with an upstream branch or `latest` tag. If a worktree has local changes, retain its binary patch and untracked-file inventory with the campaign. `source_paths` causes the manifest to record the available revision, dirty-state evidence hash, and dependency-lock hashes, but a hash is not a substitute for retaining the patch itself.

The recipes use NewL1's `Cargo.lock` and execution-specs and evm-gasfit `uv.lock` files. Build images from those selected worktrees:

```bash
docker build \
  -f Dockerfile.compute-worker \
  -t benchmarkoor-compute-worker:local ../bnbchain-newL1

docker build \
  --build-context execution-specs=../execution-specs \
  -f Dockerfile.compute-generator \
  -t benchmarkoor-compute-generator:local .

docker build \
  --build-context evm-gasfit=../evm-gasfit \
  -f Dockerfile.compute-analyzer \
  -t benchmarkoor-compute-analyzer:local .

make build-core
```

The controller first inspects each configured image. A locally cached immutable image ID is used directly and is never pulled merely because it is present. A registry digest is retained as a registry manifest reference (`repository@digest`) after inspection. These identity types are not interchangeable. Only an image that is absent locally is pulled with `if-not-present` before its immutable identity is resolved and recorded in `manifest.json`; a mutable tag is only a build and launch convenience.

## Local control campaign

[`examples/configuration/compute.yaml`](../examples/configuration/compute.yaml) is an ADD and KECCAK256 control campaign. It generates a zero point plus positive fixed-count points, uses four fresh qualification sessions, and has an anchorless analysis policy. A local Ryzen smoke or control run checks the pipeline on the machine where it runs; it is not an official calibration, designated performance baseline, or gas-price recommendation.

Run generation independently before measurement when you want to inspect the workload package:

```bash
./bin/benchmarkoor build --config examples/configuration/compute.yaml
```

This invokes `fill` in the generator image with the selected tests, deterministic seed, fixed target-count grid, and the 15M allowance. Prague is fixed by the generator command, not by a pytest marker expression. The generated `workload.json` is under `results/workloads/compute-build-*/`.

Run the campaign:

```bash
./bin/benchmarkoor run --config examples/configuration/compute.yaml
```

`run` builds a workload if necessary, creates a new `results/runs/compute-<uuid>/` directory, then automatically starts analysis. A run directory is returned even if a worker or analysis failure is attributable to the campaign, so preserve it rather than retrying into the same directory.

The native worker uses the production NewL1 block executor with an inspector only for the diagnostic phase. The timed pilot, warmup, and qualification executions use the uninstrumented production executor. The timed boundary is `newl1_block_execution`: the timer starts after preparation and restoration and covers the complete native block-execution call. It is not an RPC round trip or isolated interpreter timing.

The diagnostic request has its own worker process. Every pilot session also has its own fresh worker process. Each qualification session has a separate fresh worker process that runs all of its warmup rows first, then its qualification rows; diagnostic and pilot work never share that process. Consequently, independent qualification `session_id` values mean independent worker processes and containers. Before each requested sample the worker restores the declared baseline and relevant native execution state, and the controller records all phase and session identities. A successful timing row must retain the diagnostic `prepared_hash`, baseline identity, and commitment identity. Deployment and workload preparation occur outside the timed interval.

The finite deterministic smoke script performs a smaller real campaign:

```bash
scripts/compute/smoke.sh
```

It builds the three local images, invokes `benchmarkoor run` to generate the workload and measure it, and checks actual worker and analyzer artifacts. Its diagnostic session contains twenty distinct cases: one ADD shape and four KECCAK256 input-length shapes at four requested target-count points. It validates each actual native `target_count` against that operation's counter, including fixed work in the zero-count controls, rather than treating the requested count as the executed count. Set `COMPUTE_SMOKE_RESULTS_DIR` to retain the smoke in a chosen directory, or `BENCHMARKOOR_BIN` to use a prebuilt controller binary.

## CI behavior

Pull requests that change the compute controller run its focused Go configuration, protocol, reconciliation, and gasfit-export tests. The shell smoke does not mock malformed worker output or analyzer failure; focused Go tests own those negative paths.

Cross-repository execution is intentionally manual. Run the `Check - Prague Compute` workflow with three required inputs: `newl1_ref`, `execution_specs_ref`, and `evm_gasfit_ref`. Each input must be a published 40-character commit SHA; an unpublished local revision does not satisfy this requirement. The workflow checks out those exact revisions, exposes the sibling paths required by the image recipes, runs `scripts/compute/smoke.sh`, and retains its resulting campaign artifacts. It has no implicit external source revision and requires no repository secret beyond GitHub's read-only workflow token.

## Qualification campaign procedure

A qualifying campaign separates experiment selection from qualification. Do not use the same measurements for both.

1. Run a pilot on the target hardware. Sweep target counts and relevant parameters independently of gas. For example, sweep KECCAK256 count and input length independently. Use the pilot to select measurable count points, repetition counts, warmup policy, and resource allocation.
2. Freeze the selected workload set, count grid, random seed, session plan, qualification policy, source revisions, image identities, and resource policy before collecting qualification samples. Save those choices in the configuration and retain the pilot artifacts.
3. Retain diagnostic, pilot, warmup, and qualification rows in the canonical ledger. The export has canonical `session_id`, `phase`, `status`, and `correctness_passed` columns. For a native campaign, only `qualification` rows with `status=executed` and `correctness_passed=true` may calibrate a model. Warmup, pilot, diagnostic, failed, and incorrect rows remain auditable evidence and cannot be promoted to calibration data.
4. The shared native-campaign validator applies both when `run` starts a campaign and when `analyze --run` replays one offline. It requires the frozen qualification gates, `qualification.block_unqualified` enabled, and fail-closed eligibility. An explicit `require_correctness_passed: false` is invalid; it does not disable the correctness requirement. Generic evm-gasfit analysis may also admit a `performance` phase when configured, but never failed, incorrect, or warmup rows.
5. Use independent worker sessions. The qualification policy must explicitly set confidence level, tolerated relative uncertainty, held-out prediction error, and minimum session count. Bootstrap resampling preserves session grouping; repeated rows from one session do not establish independent replication.
6. Treat a rank-deficient model, systematic residuals, failed holdout, missing independent-session evidence, a missing paired control, or a failed policy gate as inconclusive or failed analysis. A weak result must not become a recommended price.

The analyzer export includes every terminal row in `runtimes.csv`, with status, correctness, phase, session, execution boundary, observed duration when available, and failure reason. `opcounts.json` uses `opcount` for the diagnostic target count. It never sums setup and supporting instructions into an invented operation total. A timing row may join diagnostic counts only when its prepared, baseline, and commitment identities agree.

For models that declare `overhead_baseline_param: overhead_baseline`, pair each primitive boolean `overhead_baseline=true` zero-count control with its matching `false` workload before modeling interaction features such as input length. The pairing differences runtime, `opcount`, and per-opcode counts, so fixed native target work in the control does not become variable target work. Match within the declared session and shape key; a missing matching control is an analysis error.

Raw workload slopes include the complete measured workload. They are useful because the boundary and conditions are explicit. They do not claim that the target opcode had no setup, copying, memory, wrapper, or system cost. [`compute-gasfit.yaml`](../examples/configuration/compute-gasfit.yaml) deliberately disables glue adjustment: its setup-differenced results are full-workload slopes, not isolated opcode prices. If an analysis adjusts supporting instructions, report both raw and adjusted estimates, their confidence intervals, residuals, fit quality, held-out error, and whether supporting-cost uncertainty propagated into the adjusted interval. A point-only supporting-cost adjustment has conditional uncertainty and cannot support a recommended price.

The mandatory analysis compares measured compute cost with the configured Prague gas-cost table. It does not establish a native gas-schedule identity, which the current worker cannot fingerprint. An anchorless policy, including the control example, produces that comparison without a proposed gas value. Pricing is optional and requires an explicit positive `anchor_rate` in gas per second and an explicit margin policy or `pricing_scenarios` entry. Such scenarios are research outputs only; they do not modify NewL1 gas constants.

## Retained artifacts and offline replay

A run directory is immutable at the measurement level. It contains:

```text
<results>/runs/<run-id>/
  config.json
  campaign.json
  generator.json                         # when generation was configured
  manifest.json
  workload.json
  analysis-config.yaml
  requested-samples.jsonl
  samples.jsonl
  exclusions.jsonl
  sessions/<session-id>/
    request.json
    samples.jsonl
    container.log
    exit.json
  analysis/<attempt-id>/
    status.json
    analyzer.log
    config.yaml
    manifest.json
    workload.json
    samples.jsonl
    runtimes.csv
    opcounts.json
    reports/<evm-gasfit report files, including analysis_status.json>
```

Each analysis attempt is a snapshot. The analyzer container sees the campaign mount read-only; only that attempt's `reports/` directory is writable in the container.
The selected analysis policy is snapshotted before validation. Rejected overrides retain their exact bytes and input hashes without starting an analyzer container.


The archived campaign timeout bounds analyzer image resolution, container startup, and analyzer execution. On expiry, the controller retains the attempt; a running analyzer is stopped and removed through bounded cleanup, and exit facts are recorded only when the runtime observed them.

`config.json` preserves the ordinary benchmarkoor index fields and adds:

```json
{
  "compute": {
    "schema_version": 1,
    "manifest_path": "manifest.json",
    "samples_path": "samples.jsonl",
    "analysis": {
      "status": "pending|running|succeeded|failed|inconclusive",
      "attempt_id": "...",
      "artifacts": [{"name": "...", "path": "analysis/..."}]
    }
  }
}
```

The paths are relative to the run. `campaign.json` is the saved execution and reanalysis configuration with its workload and analyzer configuration rewritten to archived paths. `manifest.json` records the image identities, source facts, lock hashes, host and resource facts, fixed limits, workload hash, execution boundary, active fork, frozen ordering, and phase policy. The native worker does not currently expose a gas-schedule fingerprint: `active_schedule_identity` is recorded as unavailable. That absence is a comparison blocker, not evidence that native campaigns used the same gas schedule.

Retain all of the following for an offline replay:

- the whole run directory, without pruning session logs, failed rows, exclusions, or analysis attempts
- exact source checkouts at their recorded commits, plus binary patches and untracked-file inventories for dirty worktrees
- the three immutable image exports, or the accessible registry manifest references, that match the recorded image identities
- `Cargo.lock`, `uv.lock`, controller `go.sum`, the command lines, and host provenance
- the pilot archive and frozen qualification configuration

Capture host provenance before a designated run, including AWS instance ID and type, AMI and kernel, `lscpu`, CPU allocation/governor, memory, attached volume type/size/IOPS/throughput and mount options, filesystem, Docker version and storage driver, and NewL1, execution-specs, evm-gasfit, benchmarkoor, Go, Rust, Python, and uv versions. Also retain the resource-limit configuration. Disk is recorded to explain the experiment, not priced as a compute resource.

To rerun analysis without invoking a generator or worker, make the recorded analyzer image available locally and run:

```bash
./bin/benchmarkoor analyze --run results/runs/<run-id>
```

This creates a new unique `analysis/<attempt-id>/` snapshot from the saved workload, measurements, manifest, and archived analysis YAML. It never overwrites a prior attempt. The controller copies its analysis inputs into the attempt, records their hashes in `status.json`, and preserves the evm-gasfit `reports/analysis_status.json` provenance and report hashes.

Compare two completed analysis report directories with evm-gasfit's real CLI, not a benchmarkoor command:

```bash
cd ../evm-gasfit
uv run evm-gasfit compare-campaigns \
  --baseline /absolute/path/to/baseline/analysis/<attempt-id>/reports \
  --candidate /absolute/path/to/candidate/analysis/<attempt-id>/reports \
  --out /absolute/path/to/campaign-comparison
```

The comparator requires `analysis_status.json` on both sides, matching archived analysis-policy/config hashes and evm-gasfit versions, and a valid archived `new_gas.csv` output hash on each side. It also requires matching workload, hardware, execution-boundary, and gas-schedule identities; software is the only permitted difference. The current native gas-schedule fingerprint is unavailable, so native campaigns currently cannot pass this comparison. A regression claim further requires qualified results on both sides.

## Designated AWS baseline

AWS r7a.4xlarge is the designated performance baseline. Provisioning and AWS commands are operator actions; this repository does not create, start, modify, or terminate cloud resources.

On a manually provisioned r7a.4xlarge host:

1. Record the instance and storage provenance listed above before the pilot. Attach and document the actual benchmark volume even though storage performance is out of scope.
2. Install the selected Docker, Go, Rust, Python, and uv versions. Check out the four source repositories at the exact recorded SHAs. Preserve any local patch rather than fetching an upstream replacement.
3. Build the images with the worker primary context and generator/analyzer named contexts shown in [Prerequisites and pinned build inputs](#prerequisites-and-pinned-build-inputs). Record image IDs and build logs.
4. Run the pilot and archive it. Choose count points and session/repetition policy from that pilot only.
5. Freeze the policy and counts, then run the qualification configuration. Keep CPU allocation, governor, memory and swap policy, container runtime, image IDs, and attached storage configuration unchanged for the campaign.
6. Archive the complete run directory, provenance bundle, and image exports before changing the host. A local control run may validate correctness, but only this recorded r7a.4xlarge procedure may be labelled as a baseline calibration.

Do not merge timings from a laptop, CI runner, or another cloud shape into an r7a.4xlarge calibration. Those machines can prove the pipeline and workload correctness; their timing distributions are separate experiments.

## Failure accounting and focused tests

A worker crash, timeout, missing row, duplicate row, malformed JSONL record, identity mismatch, failed oracle, or unsupported workload is visible in the measurement ledger. Benchmarkoor reconciles every write-ahead request to one terminal result and retains observed duration, gas, and commitment fields only when the production execution actually reached them. It never invents missing durations, hashes, counts, or success rows. Analysis errors are retained in the separate attempt status and log.

If every sample is ineligible, analysis still emits the complete eligibility and planned-model ledgers, reports those models as inconclusive, and leaves recommended prices empty. This does not turn failed native execution into a successful campaign.

The focused Go tests own those boundaries. When changing them, run the focused package tests rather than adding shell mocks:

```bash
go test -tags 'exclude_graphdriver_btrfs,exclude_graphdriver_devicemapper,containers_image_openpgp' ./pkg/config ./pkg/compute
```

These tests cover the protocol and configuration contract plus campaign reconciliation and gasfit export behavior, including malformed worker output and analysis failure paths. The smoke script is reserved for the genuine generator, native worker, and evm-gasfit path.
