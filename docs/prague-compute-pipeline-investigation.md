# Prague compute pipeline: dependency investigation and implementation plan

Date: 2026-09-21

## Recommendation

Build a compute-campaign mode inside benchmarkoor, a workload exporter inside execution-specs, a separate NewL1 execution worker, and campaign-aware analysis inside evm-gasfit. Keep NewL1 consensus, public RPC, fee settlement, and EVM semantics unchanged.

This is a medium-sized, cross-repository feature, not a client-registration change. Most implementation belongs in benchmarkoor and evm-gasfit. The NewL1 work should be predominantly additive harness code. Source inspection supports a zero-production-logic-change implementation, but no worker has been compiled or executed to prove that yet.

Use the full, synchronous `NewL1EvmBlockExecution::execute` call as the primary timing boundary. It executes user transactions and the system tail, computes the LtHash state commitment, and returns execution outputs. Do not substitute the inner EVM call, an RPC round trip, or an importer acknowledgment under the same timing label.

The [campaign context](../CONTEXT.md) remains the scope contract. Memory/storage pricing, sustained persistence, production block-limit recommendations, Osaka, and evm2 remain outside this milestone.

## Scope update after investigation

Decisions taken after this investigation widened and pinned the milestone
scope. Where older prose below describes a small representative corpus, the
following applies:

- Eligibility is a strict compute allowlist: every existing Prague-compatible
  case in the arithmetic, bitwise, comparison, stack and control-flow,
  KECCAK256, and precompile families is eligible. Campaign selections narrow
  this set per run; they never widen it. Dedicated memory, storage,
  account-access, contract-lifecycle, stateful, and post-Prague benchmarks
  remain excluded.
- Benchmarkoor owns the analysis invocation: it runs the real evm-gasfit as a
  pinned dependency and archives and exposes its outputs. Modeling logic is
  not copied or reimplemented in Go.
- No upstream execution-specs generator changes are forced. The exporter
  populates independent workload receipt, log, and storage expectations
  through existing tooling; generator-side enhancements are cooperative
  proposals, not preconditions for a valid workload package.
- The 0x20 hashing operation is canonically named KECCAK256, never SHA3.
  Precompile target counts are actual invocations of the addressed precompile
  (tracked via parameters.precompile_address and a PRECOMPILE_<address>
  semantic count key), never aggregate STATICCALL totals.
- The change-size estimates in "Change-size estimates" were computed for the
  narrower representative-corpus scope and predate this expansion; treat them
  as describing the two-case feasibility path only, not the full allowlist
  corpus.
- The workload, request, and sample-result file contracts described under
  "Shared artifact and worker contracts" are now defined canonically in
  benchmarkoor under `pkg/compute/protocol.go`, with JSON Schemas and examples
  under `pkg/compute/schema/`. Defining the contract is not a claim that any
  pipeline verification has run; no worker has been built or executed.

## Sources and investigation limits

Four independent read-only investigations covered the repositories below. The integration decisions here reconcile their findings against the actual shared contracts, rather than adopting each repository's recommendation independently.

| Repository | Inspected HEAD | Working-tree state when pinned |
| --- | --- | --- |
| benchmarkoor | `e390141e101f8855d070ab380ccde3161d58ad6d` | Untracked `CONTEXT.md` |
| execution-specs | `ad202d76b52c074ffa9395bd69a72f2a12ad1784` | Clean |
| evm-gasfit | `1826b3506b00720fc5e46c1b325bd585d8f8d82d` | Clean |
| bnbchain-newL1 | `da381acf54ea304ad272c2ef5fb9d284a27d2107` | Untracked `scripts/__pycache__/` |

No source code was changed. No project builds, test suites, nodes, containers, or cloud operations were run. Verification consisted of source inspection, source-provenance commands, and the synthetic identifiability experiment reported below. This document is a feasibility assessment and implementation plan, not an end-to-end benchmark result. All size ranges are engineering estimates.

Source links are local to this multi-repository checkout. The commit table identifies the inspected revisions.

## End-to-end architecture

```text
execution-specs
  deterministic generators + independent outcome oracles
                 |
                 v
       versioned workload package
                 |
                 v
benchmarkoor compute campaign
  provenance / setup / pilot / frozen schedule
  sessions / resource controls / failures / artifact archive
                 |
                 v
        NewL1 execution worker
  prepare baseline -> diagnostic counts -> restore -> execute
  production executor + system tail + execution commitment
                 |
                 v
  immutable samples + counts + outcomes + manifest
                 |
                 v
evm-gasfit
  validation -> raw fits -> supporting-cost adjustment
  session-aware uncertainty -> held-out checks -> qualification
                 |
                 v
  current-gas comparison + comparable-campaign differences
                 |
                 v
  optional explicit-anchor pricing scenarios
```

The Go controller and Rust worker communicate through a versioned file/JSON protocol. They do not link implementations or require NewL1 to emulate an Ethereum Engine API. Use a worker container initially so benchmarkoor's existing CPU and memory controls remain useful. Do not build a general plugin framework, a second orchestration product, or multiple execution backends for this milestone.

### Ownership

| Component | Owns | Does not own |
| --- | --- | --- |
| execution-specs | Bytecode, deterministic inputs, sweep parameters, independent expected workload outcomes | NewL1 headers, system contracts, settlement, state roots |
| benchmarkoor | Campaign lifecycle, process sessions, ordering, resource controls, archive and failure ledger | EVM semantics or authoritative execution timing |
| NewL1 worker | Native context, preparation/restoration, actual execution, counts, timing, native outcomes | Corpus design, bootstrap inference, price selection |
| evm-gasfit | Local artifact ingestion, modeling, qualification, comparison and optional scenarios | Remote measurement or sample replacement |

This division avoids duplicating a workload generator in Rust or implementing statistical analysis in Go.

## 1. Benchmarkoor

### What exists

Benchmarkoor already has useful container and resource infrastructure. `ContainerManager` supports create/start/stop, log streaming, image digests, and exit/OOM observation. Resource limits include CPU sets, memory, swap, and block-I/O controls. Datadir providers and runner strategies support disk preparation and restoration. Podman additionally supports checkpoint/restore. These are reusable primitives, although the surrounding runner lifecycle contains Ethereum-specific steps. [B1] [B2]

Its current workload/execution abstraction is not a generic native-worker interface. `StepProvider.Lines()` explicitly returns JSON-RPC lines; `PreparedSource` contains setup/test/cleanup steps and Ethereum fixture metadata. The executor times RPC requests, and its validators accept `VALID` payload status. That does not prove a previously known block was executed again. [B3] [B4]

The existing raw result structures retain per-call durations and pass/fail status, but they do not constitute the required campaign/session/sample model. Gas throughput is calculated from `gasUsed` in the `engine_newPayload` request, not from independently observed execution work. [B5]

### Required modifications

1. Add a concrete compute-campaign configuration and CLI dispatch. Do not add NewL1 to the Ethereum client registry as if JWT, Engine endpoints, FCU, Ethereum genesis, and `debug_setHead` applied.
2. Add a typed workload-package loader and compute lifecycle, under `pkg/compute/` (the protocol types, validation, and JSON Schemas now exist there) and `pkg/runner/compute_lifecycle.go`. Reuse container/resource/datadir helpers where they fit; do not force workload packages into JSON-RPC `StepFile` objects.
3. Implement explicit diagnostic, pilot, warm-up, and qualification phases; seeded case ordering; process-session identity; and the frozen repetition plan.
4. Persist a requested-sample schedule before executing it. Record one terminal outcome for every requested sample, including process crash, timeout, restore failure, not-run-after-abort, and correctness failure. Never silently replace a failed attempt with a successful retry under the same sample ID.
5. Accept duration only from the worker's completed execution measurement. Preserve controller wall time separately as orchestration overhead.
6. Archive manifests, workload packages, baseline identities, count diagnostics, raw rows, logs, and analysis inputs/results locally. Existing result directories and upload facilities can carry the archive, but upload is not needed for reproducibility.
7. Link compute artifacts from existing run summaries. A custom per-sample dashboard is optional; mandatory distributions and comparisons can live in gasfit's reports.
8. Invoke the real evm-gasfit as a pinned dependency for the analysis stage, and archive and expose its outputs verbatim alongside the run's raw artifacts. Do not reimplement or port modeling logic into Go.

Existing suite content hashing is a useful pattern, not a sufficient identity by itself. The new workload identity must cover code, parameters, expectations, and relevant state/context descriptors, not just RPC text. [B3] [B6]

### Restoration policy

Start the feasibility proof with a fresh worker process and restored baseline for each sample. For repeated warm samples within a session, reconstruct all mutable execution state between samples and explicitly retain only the declared warm process conditions. Disk rollback alone does not restore an EVM journal, sender cache, snapshot resolver, executed-state overlay, or LtHash tracker.

CRIU is not necessary for the first milestone. A restored process image is also not evidence of a fresh-process replication. Retain benchmarkoor's storage-strategy capabilities without making any particular filesystem or checkpoint facility a prerequisite.

## 2. Execution-specs

### What exists

The benchmark suite supplies reusable opcode builders, operand classes, memory controls, arithmetic/hash tests, and precompile cases. Its default benchmark activation marker is Prague, but that is a lower-bound marker, not a reason to omit explicit Prague selection when running from the current Amsterdam branch. [E1]

Fixed-work generation already exists. `BenchmarkCodeGenerator.deploy_fix_count_contracts` builds a target contract and an outer loop contract; the command-line fixed-count mode uses counts expressed in thousands. The current verification allows a five-percent difference from the requested target count. A fixed-count campaign should therefore record and fit exact diagnosed counts, not assume the label is exact. [E2]

There is real opcode counting through the transition-tool/filler machinery. Those traces are useful independent checks of generated workloads, but authoritative counts for the campaign must come from the NewL1 diagnostic execution of the measured bytes and context. Ethereum traces alone cannot establish what NewL1 executed. [E3]

The current ADD generator discards intermediate values during cleanup. Some hash generators discard results; others overwrite memory. These existing gate workloads do not supply the explicit, independently calculated terminal witness needed here. [E4]

### Required modifications

These items describe the exporter design inside execution-specs. Nothing here forces upstream acceptance: the exporter works from the existing benchmark machinery, populates expectations through existing tooling where possible, and generator-side changes are cooperative proposals. Where an item cannot be done without upstream changes, the exporter still ships a valid workload package and marks the affected cases unsupported with reasons.

1. Add a workload export format in `packages/testing/src/execution_testing/benchmark/`, with a CLI/filler integration that captures materialized code, prestate requirements, transaction intent, parameters, and expectations before Ethereum-specific fixture envelopes become the transport contract.
2. Reuse the existing fixed-work and opcode-building machinery. Add exact-count/termination and output support where needed instead of using gas exhaustion as the independent variable. Keep transaction count fixed throughout each count sweep.
3. Export transaction intent and deterministic signer information sufficient for the worker to construct valid native transactions. Final signed bytes must be saved in the prepared campaign. Do not require Ethereum chain IDs, expected block hashes, or fixture state roots to pass unchanged into NewL1.
4. Add outcome-bearing ADD and KECCAK workloads. Keep the same outcome-bearing bytecode in diagnostic and timed executions. Calculate arithmetic results modulo 2^256 and hash expectations independently from the NewL1 executor, using Ethereum Keccak rather than SHA3-256.
5. Emit a final result through a receipt log or a declared output slot. Prefer an outer, once-per-transaction log and retained/returned inner results where feasible. The existing fixed-work generator often uses `STATICCALL`: inserting a log or `SSTORE` into that callee would fault. Output plumbing must respect that boundary and verify inner-call success. Do not use a repeated XOR of identical results as a witness, since even repetition counts cancel it.
6. Add setup/control workloads and independently sweep operation count and input size. Account for wrapper calls, copies, loop instructions, memory initialization, and the witness. Costs repeated per inner invocation are not a fitted constant merely because they are called setup.
7. Extend the corpus within the compute allowlist: arithmetic/bitwise/comparison classes, operand-sensitive arithmetic, hashing, and Prague precompiles. Keep unsupported cases visible. The existing data-dependent MOD workload does not support fixed-count mode and needs a deliberate bounded variant before inclusion. [E5]

### Corpus and oracle choices

The first end-to-end corpus is ADD and KECCAK256 plus the controls needed to identify their costs. Beyond that gate, every existing Prague-compatible arithmetic, bitwise, comparison, stack/control-flow, KECCAK256, and precompile case is eligible for campaign selection under the scope update above. It means multiple count/size points, not just two transactions. Pilot counts must stay inside actual NewL1 transaction, block, and code-size limits; Ethereum fixture limits are not permission to raise NewL1's limits.

For hashing, include lengths around 32-byte word boundaries and the 136-byte Keccak rate boundary. The current hash generator already exposes both scales. A gas-linear word model is a hypothesis about runtime, not a consequence of the gas formula. Inspect residuals and use a supported piecewise/parameterized model or mark the result inconclusive. [E4]

For precompiles, export input bytes, length/operand classes, expected call success, and independently expected output. Count invocations by target address as well as supporting call opcodes. A `STATICCALL` count is not sufficient when several targets are called. Intentional failure is a workload outcome: distinguish an expected failing call from a broken sample. Do not classify empty successful output as a revert.

Filter against the actual active Prague/NewL1 precompile set. In execution-specs, P256VERIFY appears in Osaka's mapping, not Prague's; its presence in a gasfit preset is not Prague activation evidence. [E6]

## 3. Sensitive repository: bnbchain-newL1

### Existing public seam

`newl1-node::components` publicly exports `BlockExecution`, `NewL1EvmBlockExecution`, execution-state and sender caches, storage handles, LtHash tracking, and lane-disjointness helpers. `NewL1EvmBlockExecution::new` and the `BlockExecution::execute` trait method are public. A separate workspace binary can therefore be built against the production wrapper without routing through the running node. [N1]

The wrapper's execution path is:

1. Obtain recovered senders from the sidecar, or recover on a miss.
2. Open the parent's post-state through the executed-state cache/provider.
3. Construct the lane-aware executor and execute transactions, including the system tail performed at executor finish.
4. Merge state transitions, project the bundle, update the in-memory LtHash accumulator, and derive the state root.
5. Calculate receipt commitments and logs bloom and return the complete execution output. [N2]

The LtHash `commit` in this call writes the in-memory map. Durable persistence is separate. The call returns synchronously; it is a better compute boundary than the scheduler/importer, which introduce queueing, deduplication, and persistence coordination. [N2] [N3]

### Proposed worker

Add a dedicated `bin/newl1-bench` workspace member. Keep it independent of the production node CLI and expose only the local worker protocol. Its responsibilities are loading packages, preparing a native baseline, running diagnostics, restoring sample state, calling the production wrapper, checking outcomes, and serializing results.

Use the real NewL1 chain spec, system-contract allocation, header/snapshot providers, and Parlia execution plan. Deploy or prepare workload contracts outside timing. Save a coherent parent baseline after untimed setup: parent header/hash, state, sender nonces, validator/snapshot context, and LtHash accumulator must all describe that same parent. A block after setup cannot keep using the genesis parent merely because the workload is small.

Use ordinary supported transactions for the compute corpus; no new transaction type is required. Select one fixed, ordinary system context initially. Block-1 initialization, epoch/day transitions, finality-reward events, and other exceptional system work must not vary silently inside a count sweep. Do not replace real providers with null implementations that omit the system tail. [N4]

### Production-equivalence details

The public constructor alone is not the complete production wiring:

- Production attaches `with_lane_disjointness_checker`; the constructor defaults to capture disabled. The primary worker must mirror production capture/checker behavior. Background observer load and its CPU placement are part of the declared environment. Turning it off defines a separate experiment. [N5]
- The production importer/miner populates `RecoveredSendersCache`. Populate it outside the timer to reproduce the usual hit path, and pin/report that policy. A cache-miss campaign includes signer recovery and must carry a different execution-condition identity. [N2] [N5]
- Record compiler, release profile, allocator/features, logging/metrics configuration, lane configuration, and effective fork selection. Use production build settings, not debug/test execution as calibration.
- Reconstruct the tracker, caches, providers, and mutable execution context from the same baseline for each independent sample. An idempotent LtHash commit is not proof that the EVM was skipped, but retaining previous sample state is still the wrong restoration policy.

These are harness wiring requirements, not requests to change production behavior.

### Counting and gas quantities

`NewL1EvmFactory` supports an inspector-bearing EVM and an uninstrumented EVM. The lane-aware convenience method currently constructs its own EVM, so the diagnostic worker must compose the existing public pieces with an inspector, or use a small additive helper for that composition. Keep the lane isolation and system-tx sink intact. Performance samples use the existing uninstrumented production wrapper. Verify matching user/system receipts and state effects between diagnostic and uninstrumented executions. [N6]

Preserve prepaid charging in both paths. Report:

- Declared gas from the prepared transactions.
- Charged user gas from native receipt deltas.
- System-tail gas separately.
- Pre-settlement gas only when an observation hook on the unchanged charging path proves its meaning; otherwise store null with an availability reason.

`set_charge_by_limit(false)` enables a different simulation settlement path. It must not be used to obtain a number mislabeled as pre-settlement gas from the measured production execution. [N7]

### Production change budget

Target zero changed lines in consensus, RPC, importer/scheduler algorithms, settlement handlers, gas tables, or revm. No reth patch is planned.

New workspace/build entries and additive benchmark code are necessary. A roughly 40–80-line additive inspector-construction helper in `crates/evm/src/config.rs` may be worthwhile if the diagnostic composition otherwise drifts. It is not yet demonstrated to be necessary. Any larger production refactor is a stop-and-review gate, not an assumed part of implementation.

The zero-production-diff route remains source-backed, not compile-proven. Native baseline construction and diagnostic equivalence are the most important practical uncertainties.

## 4. Evm-gasfit

### What exists

Keep the existing scipy NNLS backend, model specification machinery, opcode-count validation, supporting-instruction analysis, plotting, and report pipeline. It already accepts repeated runtime rows and has EEST and zkevm adapters. Its raw runtime loader preserves extra columns. [G1]

However, `_build_design` constructs a new frame with only the response and numerical features. `fit_nnls` resamples individual rows, not process sessions. Extra columns passing through a CSV loader therefore do not establish session-aware inference. The code already checks design rank and skips unfit designs; the missing part is durable qualification status and correct uncertainty semantics. [G2]

There is also an existing paired-overhead-control mechanism. Extend it rather than inventing a second baseline-subtraction path. It currently averages controls across matching fixture parameters/client, and differences all numeric columns except designated keys/counts. New numeric metadata such as gas, sequence IDs, and repetitions must not accidentally be subtracted. [G3]

Glue adjustment subtracts point estimates from coefficients and shifts the confidence bounds by the same amount. It does not propagate supporting-cost uncertainty. Proposal generation currently requires an anchor, and poor-fit flags are not the same as preventing an unqualified numeric price. The bundled fallback table covers Osaka, so a reproducible Prague/NewL1 schedule must be supplied explicitly or resolved and archived from pinned sources. [G4] [G5]

### Required modifications

1. Add a local campaign/benchmarkoor adapter, following the existing adapter pattern. Normalize IDs, units, status, phase, session, diagnostic linkage, and gas distinctions. Keep authoritative raw artifacts immutable; produce a separate analysis eligibility/exclusion table.
2. Carry grouping metadata separately from the design matrix into inference and control pairing. Resample independent sessions, retaining their within-session dependence and planned case structure. A two-stage bootstrap requires a justified within-session sampling model; do not independently resample correlated rows by default.
3. Make confidence level configurable and archive the complete frozen qualification policy. Add sufficient-session/design checks, rank/conditioning diagnostics, held-out prediction error, residual diagnostics, and explicit inconclusive reasons.
4. Hold out sessions to assess reproducibility and workload points to assess interpolation/generalization. Do not randomly split repeats of the same case/session and call that independent validation.
5. Extend the existing feature construction where needed. It currently supports `n` and `n × parameter`; a pure input-length/setup term is not expressible that way. Use explicit setup features or paired controls matched by size and session. Reject silently collapsed planned designs.
6. Propagate uncertainty through the actual control/glue adjustment procedure, preferably by refitting the calibration and target models in grouped bootstrap draws. Preserve their covariance. Adding standard errors in quadrature assumes independence and is not a generally valid substitute. If propagation is unavailable, label intervals conditional and do not imply otherwise.
7. Emit qualification status for every planned model, including unsupported, failed, and inconclusive cases. Retain numerical estimates as research observations, but prevent unqualified results from becoming recommended prices. Distinguish qualification of a whole workload slope from qualification of an adjusted target-operation estimate.
8. Make compute/current-gas comparison the mandatory report, with no required pricing anchor. Report measured cost, current schedule rules, parameter dependence, uncertainty, and relative comparisons. Add a pricing-scenario section only when an explicit gas-per-second anchor and margin policy are supplied.
9. Add manifest-driven campaign comparison. Match workload bytes/parameters, measurement boundary, state/system context, hardware, build settings, and gas schedule; identify which factor changed. Preserve comparisons of different conditions as labeled descriptive comparisons, not automatic regression claims.
10. Archive exact configs, inputs, hashes, qualification decisions, and output versions. Update the existing API/CLI tests and the three internal design contracts together with these behavior changes.

Likely files: `adapter/benchmarkoor.py`, `io/runtimes.py`, `io/fixtures.py`, `config.py`, `modeling/estimate.py`, `modeling/nnls.py`, `modeling/results.py`, a diagnostics module, `glue/adjust.py`, `proposal/aggregate.py`, `proposal/build.py`, report modules, `api.py`, and `cli.py`.

### Statistical design check performed

A synthetic NumPy rank experiment produced:

| Design | Shape | Rank |
| --- | --- | --- |
| Intercept, target count `n`, supporting count `2n` | 4 × 3 | 2 of 3 |
| Same design repeated 20 times | 80 × 3 | 2 of 3 |
| Independent count/length grid: `1, n, w, n*w` | 16 × 4 | 4 of 4 |
| Same hash design at one fixed length | 4 × 4 | 2 of 4 |

Inputs were `n = [128, 256, 512, 1024]` and `w = [1, 4, 16, 64]`. These were design matrices, not execution timings.

The consequence is structural: additional repetitions cannot separate costs whose regressors are proportional. Supporting opcodes require independent calibration or suitable controls; an intercept does not remove per-operation support cost. The two integration workload families therefore need control cases, not just more repetitions of unchanged bytecode.

Confidence intervals describe uncertainty under the measured design. They do not prove worst-case performance for all inputs, nor turn a selected maximum over noisy fitted classes into a guaranteed upper bound.

## Shared artifact and worker contracts

Use simple versioned JSON/JSONL files initially. The workload, request, and sample-result contracts are now implemented canonically in benchmarkoor (`pkg/compute/protocol.go`, JSON Schemas and examples under `pkg/compute/schema/`); the remaining rows (`campaign.json`, `prepared.json`, `diagnostics.jsonl`, analysis directory) are still proposed contracts.

| Artifact | Required content | Authority |
| --- | --- | --- |
| `campaign.json` | Source/dirty-diff and lockfile identities; binaries/images; hardware/storage/CPU allocation; fork and active schedule; baseline and corpus hashes; boundary; seeds; frozen pilot-derived plan and qualification policy | benchmarkoor |
| `workloads.json` plus blobs | Stable IDs, generator/seed, operation/input class, exact code/input bytes, prestate/setup requirements, transaction intent, parameter values, independent outcome expectations, coverage/unsupported entries | execution-specs |
| `prepared.json` plus baseline | Native parent/context identity; saved final transaction/block bytes; setup outputs; resolved chain/snapshot/lane context; baseline state identity | NewL1 worker |
| `diagnostics.jsonl` | Same prepared identity; actual target/support/system counts; precompile target counts; observed versus expected outcomes; inspector/instrumentation version | NewL1 worker |
| `requests.jsonl` / `samples.jsonl` | Complete scheduled attempts and terminal records, including failures, warm-ups, and exclusions | benchmarkoor, with worker-owned execution facts |
| Analysis directory | Derived runtime/count inputs; eligibility ledger; config; coefficients; raw/adjusted intervals; diagnostics; qualification; comparisons/scenarios | evm-gasfit |

A sample needs at least `campaign_id`, `session_id`, `sample_id`, `case_id`, repetition/order, phase, worker identity, prepared/baseline/diagnostic hashes, execution boundary, cache/capture policy, integer `execution_duration_ns`, correctness status, and an error/exclusion record. Keep expected workload failure separate from infrastructure or oracle failure. A missing execution duration is null, not zero.

The controller records a request before launching work. The worker reports completion only after the synchronous execution call returns and required checks have been evaluated. Failed correctness checks retain their observed duration but are ineligible for successful-workload calibration. For process loss, the controller reconciles missing results against the schedule; it cannot fabricate execution facts.

Convert nanoseconds to gasfit's milliseconds exactly once in the adapter. Link diagnostics by prepared workload/context identity; never count a different witness variant and attach those counts to an unmodified timed workload.

## Implementation sequence and proof gates

### Gate A: native execution feasibility

Build the minimal separate worker before large harness/UI changes. Execute one bounded ADD workload and one bounded KECCAK workload through the full production wrapper. Prepare real system state outside timing. Verify independently specified witnesses, native receipt/system effects, repeatability of commitments from freshly restored baselines, and diagnosed counts for the identical bytecode.

Prove that the public API composition compiles and that production lane capture, sender-cache policy, settlement, and context are retained. If this requires changing consensus or EVM behavior, stop and report the concrete blocker. Do not silently switch to plain revm or an inert-Parlia test harness.

### Gate B: end-to-end integration

Add benchmarkoor's minimal compute dispatch and worker protocol. Run both workload families and controls through saved raw artifacts into gasfit. Deliberately exercise a worker failure and an incorrect expected outcome; both must remain visible and must not enter the successful fit input. Demonstrate two successive requests actually execute after restoration, rather than returning previously imported results.

This proves plumbing and correctness, not performance qualification.

### Gate C: qualified campaign

Extend the corpus, run a pilot, then freeze counts, lengths, sessions, warm-ups, randomization, qualification criteria, and model choices before qualification sampling. Collect fresh-process replications and declared within-session repetitions. Fit and qualify ADD and KECCAK models, with explicit status for all other selected cases. If either integration model is inconclusive, revise the design and run a new labeled campaign rather than tuning away failed samples.

Use AWS r7a.4xlarge for calibration and record its actual storage and machine configuration. Local machines and CI establish correctness only. Cloud provisioning/execution remains a human-operated step under workspace restrictions.

### Gate D: reproducibility and comparison

Regenerate a saved corpus and rerun analysis without a remote service. Compare two compatible campaigns and demonstrate that incompatible boundaries/hardware/workloads cannot be silently classified as software regressions. Include a small deterministic end-to-end subset in CI; no unstable timing thresholds in ordinary CI. Mandatory reports include distributions, counts, model diagnostics, qualification, active-gas comparison, and campaign differences.

## Change-size estimates

These ranges describe handwritten implementation code added or materially changed. They exclude generated fixtures, raw data, lockfile churn, and most tests/documentation. Full-milestone numbers include the feasibility gate; do not add the two columns. File counts are approximate implementation touch points. Confidence is medium for locating the seams and low-to-medium for total scope until the worker and protocol compile/run. These estimates were computed for the narrower representative-corpus scope and predate the allowlist expansion; see "Scope update after investigation" — treat the full-milestone column as describing the two-case pipeline carried to completion, not the full eligible corpus.

| Repository/workstream | Feasibility + two-case pipeline | Complete milestone | Likely full implementation files |
| --- | ---: | ---: | ---: |
| benchmarkoor | 700–1,300 lines | 2,500–4,500 lines | 18–30 |
| execution-specs | 350–700 lines | 700–1,400 lines | 6–12 |
| evm-gasfit | 200–500 lines | 1,500–2,500 lines | 15–22 |
| NewL1 additive worker | 500–1,000 lines | 1,000–2,000 lines | 6–10 |
| NewL1 existing production logic | 0 intended | 0 intended | 0 intended |
| Optional NewL1 inspector-composition API | Not assumed | Approximately 40–80 lines if justified | 1 |
| **Total, excluding optional helper** | **Approximately 1,750–3,500 lines** | **Approximately 5,700–10,400 lines** | **Approximately 45–74** |

Regression/contract tests, CI integration, documentation, and example configurations make the total reviewed diff larger. Sample-level UI browsing would add roughly 300–800 implementation lines and is optional because gasfit supplies the required reports. These are sizing bands, not a delivery schedule or precision estimates.

The largest engineering risks are native baseline correctness, carrying the same semantics into diagnostics, and valid inference after supporting-cost adjustment. The first two should be resolved before committing to the upper-level implementation. Once the artifact/worker contracts are fixed, benchmarkoor lifecycle, execution-specs export, and gasfit analysis can proceed concurrently; NewL1 production changes remain a separately reviewed exception.

## Decisions that resolve conflicting dependency assumptions

- Do not reuse the existing Ethereum fixture replay envelope merely because execution-specs already exports it for benchmarkoor. That route moves compatibility work into the sensitive NewL1 side.
- Do not call the inner lane executor and label it the same boundary as the full production wrapper.
- Do not infer from compatible CSV columns that gasfit already satisfies campaign qualification. Basic fitting works; session-aware inference and qualification do not yet follow.
- Do not use a different witness-only workload for correctness while timing an unchecked variant. Verify and time the same declared program.
- Do not carry EEST's approximate target-count label into modeling as exact work. Save NewL1's diagnosed counts and explicitly handle a generator mismatch.
- Do not use receipt gas or simulation-mode gas as a substitute for actual operation counts.
- Do not add a second orchestrator or replace NNLS. Adapt the three less-sensitive repositories to these contracts.

## Source index

### Benchmarkoor

- [B1: Container lifecycle and resource interface](../pkg/docker/docker.go#L23-L88)
- [B2: Datadir providers](../pkg/datadir/provider.go), [container recreation](../pkg/runner/strategy_container.go), [checkpoint strategy](../pkg/runner/strategy_checkpoint.go), [resource controls](../pkg/runner/resources.go)
- [B3: JSON-RPC-shaped source contracts](../pkg/executor/source.go#L29-L73)
- [B4: RPC timing implementation](../pkg/executor/executor.go), [payload validators](../pkg/jsonrpc/validator.go#L34-L88)
- [B5: Raw result fields and request-derived gas throughput](../pkg/executor/results.go#L248-L370)
- [B6: Suite artifacts and hashing](../pkg/executor/suite.go), [configuration](../pkg/config/config.go), [runner lifecycle](../pkg/runner/lifecycle.go)

### Execution-specs

- [E1: Benchmark fork selection](../../execution-specs/tests/benchmark/conftest.py), [benchmark options](../../execution-specs/packages/testing/src/execution_testing/cli/pytest_commands/plugins/shared/benchmarking.py)
- [E2: Fixed-count construction and verification](../../execution-specs/packages/testing/src/execution_testing/specs/benchmark.py#L121-L186), [count tolerance](../../execution-specs/packages/testing/src/execution_testing/specs/benchmark.py#L551-L573)
- [E3: Transition-tool opcode counting](../../execution-specs/packages/testing/src/execution_testing/evm_tools/t8n/cli.py), [filler metadata](../../execution-specs/packages/testing/src/execution_testing/cli/pytest_commands/plugins/filler/filler.py)
- [E4: Arithmetic generator](../../execution-specs/tests/benchmark/compute/instruction/test_arithmetic.py#L126-L159), [hash generators and size controls](../../execution-specs/tests/benchmark/compute/instruction/test_keccak.py)
- [E5: Arithmetic operand classes](../../execution-specs/tests/benchmark/compute/instruction/test_arithmetic.py), [precompile helpers](../../execution-specs/tests/benchmark/helper/precompile.py), [benchmark authoring contract](../../execution-specs/docs/writing_tests/benchmarks.md)
- [E6: Prague precompile mapping](../../execution-specs/src/ethereum/forks/prague/vm/precompiled_contracts/mapping.py#L57-L75), [Osaka mapping](../../execution-specs/src/ethereum/forks/osaka/vm/precompiled_contracts/mapping.py#L59-L78)

### NewL1

- [N1: Public component exports](../../bnbchain-newL1/crates/node/src/components/mod.rs#L48-L70), [execution trait and constructor](../../bnbchain-newL1/crates/node/src/components/execution_scheduler.rs#L121-L260)
- [N2: Production execution wrapper](../../bnbchain-newL1/crates/node/src/components/execution_scheduler.rs#L263-L462)
- [N3: In-memory LtHash commit](../../bnbchain-newL1/crates/node/src/components/lthash_state.rs#L330-L357)
- [N4: Native EVM configuration](../../bnbchain-newL1/crates/evm/src/config.rs#L93-L190), [system execution tail](../../bnbchain-newL1/crates/evm/src/executor.rs), [offline system-state preparation precedent](../../bnbchain-newL1/bin/newl1-cli/src/genesis_seed_stakehub.rs), [Prague genesis configuration](../../bnbchain-newL1/crates/chainspec/src/genesis/devnet.json)
- [N5: Production capture/cache wiring](../../bnbchain-newL1/crates/node/src/node.rs#L503-L528), [sender cache](../../bnbchain-newL1/crates/node/src/components/senders_cache.rs)
- [N6: EVM inspector factory](../../bnbchain-newL1/crates/evm/src/precompiles/factory.rs), [lane-aware constructor](../../bnbchain-newL1/crates/evm/src/config.rs#L162-L190)
- [N7: Charging mode](../../bnbchain-newL1/crates/evm/src/aa/evm.rs), [settlement refund behavior](../../bnbchain-newL1/crates/evm/src/aa/handler.rs#L323-L336), [system gas observation](../../bnbchain-newL1/crates/evm/src/executor.rs#L181-L207)

### Evm-gasfit

- [G1: Runtime input](../../evm-gasfit/src/evm_gasfit/io/runtimes.py), [count input](../../evm-gasfit/src/evm_gasfit/io/opcounts.py), [EEST adapter](../../evm-gasfit/src/evm_gasfit/adapter/eest.py)
- [G2: Feature construction and rank checks](../../evm-gasfit/src/evm_gasfit/modeling/estimate.py#L228-L329), [row bootstrap](../../evm-gasfit/src/evm_gasfit/modeling/nnls.py#L55-L86)
- [G3: Existing paired-control subtraction](../../evm-gasfit/src/evm_gasfit/modeling/estimate.py#L117-L192)
- [G4: Glue point adjustment](../../evm-gasfit/src/evm_gasfit/glue/adjust.py#L86-L124), [proposal selection](../../evm-gasfit/src/evm_gasfit/proposal/aggregate.py)
- [G5: Required anchor configuration](../../evm-gasfit/src/evm_gasfit/config.py#L209-L214), [bundled gas tables](../../evm-gasfit/src/evm_gasfit/defaults/_fallback.py#L163-L165), [current metadata writer](../../evm-gasfit/src/evm_gasfit/api.py#L221-L246)
