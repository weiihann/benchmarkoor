# Prague compute benchmark pipeline

Date: 2026-09-21

## Objective and scope

Build a reproducible pipeline that generates compute workloads, executes them on
current Prague NewL1, collects repeated measurements, fits cost models, and
compares campaigns. The first milestone proves this entire path.

Workload eligibility is a strict compute allowlist: every existing
Prague-compatible benchmark case in the arithmetic, bitwise, comparison, stack
and control-flow, KECCAK256, and precompile families is eligible. An individual
campaign selects an explicit subset of eligible cases; selections can narrow
any run but never widen eligibility. Dedicated memory, storage, account-access,
contract-lifecycle, and stateful benchmarks, and benchmarks for post-Prague
forks, are excluded. An earlier draft limited the milestone to a small
representative corpus; that narrower scope is superseded, and size estimates
produced under it no longer describe the full corpus.

Benchmarkoor is the preferred experiment harness. Reuse Ethereum execution-specs
workload generators and evm-gasfit analysis where their contracts fit. NewL1 owns
the execution integration and the correctness checks for its behavior.

Analysis is invoked, not reimplemented: benchmarkoor runs the real evm-gasfit as
a pinned dependency and archives and exposes its outputs; modeling logic is
never copied or ported into benchmarkoor's Go code. Generator cooperation
happens through existing execution-specs tooling: the exporter populates
independent workload receipt, log, and storage expectations with the machinery
that already exists, and upstream generator changes are cooperative proposals,
never preconditions for a valid workload package.

Keep the production fork, runtime, gas schedule, and fee settlement unchanged.
Osaka and evm2 are later campaigns. Memory pricing, storage pricing and growth,
network propagation, sustained persistence capacity, and production block-limit
recommendations are outside this milestone. Existing intrinsic and calldata gas
rules remain active.

## Pipeline and responsibilities

| Stage | Responsibility | Artifact |
| --- | --- | --- |
| Campaign definition | Pin software, hardware, execution context, and experiment settings | Run manifest |
| Workload generation | Generate fixed amounts of work with deterministic inputs | Workloads and expected outcomes |
| Preparation and counting | Prepare state; verify workload behavior and actual operation counts | Baseline identity and count metadata |
| Measurement | Repeatedly execute through NewL1 using benchmarkoor orchestration | Raw samples and correctness results |
| Analysis | Fit supported compute models and assess their quality | Coefficients, uncertainty, and diagnostics |
| Reporting | Compare current gas with measured compute cost and compare campaigns | Baseline report and optional pricing scenarios |

The manifest records source revisions and local changes, dependency lockfiles,
build settings, binary or image identities, Prague configuration, the active gas
schedule, machine configuration, CPU allocation, state identity, workload hashes,
timing boundaries, and analysis settings.

The workload, request, and sample-result file formats are defined canonically in
`pkg/compute/protocol.go`, with JSON Schemas and examples under
`pkg/compute/schema/` for the Python generator and the Rust worker. The 0x20
hashing operation is named KECCAK256 throughout, never SHA3. Precompile target
counts are per-address invocation counts, not aggregate call-opcode totals.

The designated performance baseline is AWS r7a.4xlarge. Record its actual
configuration, including attached storage, even though disk performance is not a
pricing target in this milestone. Other machines may verify pipeline correctness;
their timings must remain separate from baseline calibration results.

## Benchmarkoor integration

Use benchmarkoor for the experiment lifecycle, repetition, state restoration,
resource controls, result organization, and its existing reporting where suitable.
Add a benchmark integration that invokes NewL1's production execution code. Keep
the integration separate from production consensus and public RPC behavior.

The first implementation gate is to verify benchmarkoor's execution extension
points against a pinned source revision, then demonstrate one arithmetic workload
and one hashing workload. This is a feasibility gate, not an assumption that a
NewL1 plugin interface already exists. If supporting NewL1 requires extensive
changes to benchmarkoor or production execution semantics, return to the design
with the concrete integration cost before building a replacement harness.

NewL1 uses its own block structure, system transactions, fee settlement, and state
commitments. Ethereum fixture envelopes and expected hashes are not interchangeable
with NewL1 outcomes. Reuse workload intent and generation code, while preparing a
valid NewL1 execution context and checking the appropriate outcomes.

A sample finishes when the measured execution has actually completed. Submission
acknowledgment or duplicate-block recognition does not constitute execution.

## Workload corpus eligibility

Eligible cases are every existing Prague-compatible benchmark in the
arithmetic, bitwise, comparison, stack and control-flow, KECCAK256, and
precompile families, including operand-sensitive arithmetic variants. The
integration gate still uses arithmetic and KECCAK256 first; the wider allowlist
feeds subsequent campaigns through the same completed pipeline. Each campaign
explicitly lists its covered operations and unsupported cases; unsupported or
excluded cases stay represented in exported workload packages with status and
reason rather than being dropped.

Sweep target-operation counts and relevant input parameters independently of gas.
For example, vary both hash count and input length. Keep generated transactions
within NewL1's existing limits. Add operand classes when cost can depend on values,
and identify successful and intentionally failing precompile cases separately.

Use a small, controlled prestate. Deploy contracts outside timed samples. Hold
transaction count and block context constant within an operation-count sweep so
fixed transaction and system work can be distinguished from marginal operation
cost. Treat exceptional system contexts as separate workloads.

Compute operations still touch memory and use surrounding instructions. Initialize
and reuse memory consistently, account for setup and copying, and include control
cases for setup costs that change with input length. These are measurements of the
implemented operation under declared conditions, not a claim to isolate CPU
arithmetic from every memory effect.

## Measurement methodology

1. Verify behavior and collect actual target and supporting instruction counts in
   a diagnostic execution. Check counts rather than trusting generator labels.
2. Perform performance executions without detailed opcode tracing. Preserve
   normal production behavior and record any measurement instrumentation.
3. Use a pilot to choose counts that yield measurable durations and a repetition
   plan. Freeze those choices before collecting qualification samples.
4. Collect repeated samples across independent process sessions. Randomize case
   order using a recorded seed, specify warm-up treatment, and keep session IDs.
5. Restore the declared baseline between independent samples. Restoration includes
   database state and relevant in-memory execution, commitment, and consensus
   context. Distinguish deliberately warm execution from a fresh process.
6. Keep every raw sample, failure, and exclusion reason. Do not discard slow
   samples solely because they are slow or silently replace failed runs.

Record execution duration, diagnostic work counts, declared gas, gas consumed
before settlement where available, and charged gas as distinct quantities.
NewL1's prepaid settlement makes receipt gas alone unsuitable as the work measure.
Host CPU and memory observations describe the experiment and help detect noise;
they do not expand this milestone into memory pricing.

Document whether timing covers the interpreter, the NewL1 block executor, or the
larger block-execution wrapper. The primary campaign uses a consistent production
execution boundary. Record fixed wrapper work and use control cases to assess its
effect. Do not label an RPC round-trip measurement as isolated interpreter time.

## Analysis and outputs

Benchmarkoor invokes the real evm-gasfit as a pinned dependency for the analysis
stage and archives and exposes its outputs; no modeling logic is reimplemented
in Go. Use evm-gasfit for compatible runtime/count inputs and model
definitions. Simple
models relate duration to operation count; parameterized models add interactions
such as operation count multiplied by input words. Supporting instructions whose
counts grow with the target require explicit accounting; a fitted intercept alone
does not remove their cost.

Report raw and adjusted estimates, confidence intervals, residuals, model fit,
and held-out prediction error. Separate real execution repetitions from bootstrap
resampling. Preserve session grouping when assessing repeatability; tightly
correlated observations do not establish independent replication. State whether
uncertainty in supporting-instruction estimates is propagated into adjusted
confidence intervals.

Qualification settings are required campaign inputs, recorded before qualification
runs: confidence level, tolerated uncertainty, and held-out prediction error.
Rank-deficient models, unexplained systematic residuals, or failed qualification
produce an inconclusive result. Weak or missing models cannot silently become
recommended prices. Revisit the experiment or model when a result is inconclusive.

The mandatory report compares measured compute costs with the active Prague/NewL1
gas schedule. Optional pricing scenarios accept an explicit gas-per-second anchor
and margin policy, without treating Ethereum's chosen anchor as a NewL1 result.
They are research outputs and do not alter production constants. Cost comparisons
are scoped to compute and do not establish the correct price of an operation's
storage or other resource effects.

Preserve manifests, workloads, diagnostic counts, raw timing rows, model settings,
and report outputs so analysis can be repeated without access to a remote service.
Campaign comparisons distinguish software changes, fork changes, hardware changes,
and workload changes; only comparable observations support a regression claim.

## Completion criteria

- A saved campaign can regenerate its workload corpus and rerun the measurement
  and analysis stages from documented inputs.
- Arithmetic and hashing cases traverse the complete benchmarkoor-to-NewL1-to-
  analysis path with independently specified outcome checks.
- Each reported timing represents real execution; restoration failures, skipped
  cases, and unsuccessful samples remain visible.
- Repetitions across fresh sessions demonstrate repeatability within the
  campaign's declared uncertainty criteria. At least the two integration cases
  produce qualified models; other cases retain explicit qualification status.
- Reports show operation counts, timing distributions, model diagnostics, current
  gas comparisons, and differences between comparable campaigns.
- A small deterministic subset validates pipeline correctness in CI. Performance
  qualification runs on the designated baseline host with recorded conditions.
- Prague production semantics and gas constants remain unchanged.

No production-safe gas limit is a deliverable of this compute-only milestone.

## Later campaigns

Preserve workload descriptions and result formats independently of revm's internal
objects. For Osaka, regenerate fork-dependent expectations and label the fork in
every result. For evm2, update the NewL1 execution integration and rerun the corpus;
do not assume a drop-in replacement or a particular speedup.

Memory, storage, state-growth policy, and eventual block-limit validation extend
the demonstrated pipeline in later milestones. They do not delay the compute
milestone and are not preimplemented as additional runtime counters or backends.

## References

- [Benchmarkoor](https://github.com/ethpandaops/benchmarkoor) and its
  [configuration reference](https://github.com/ethpandaops/benchmarkoor/blob/master/docs/configuration.md).
- [Ethereum execution-specs benchmarks](https://github.com/ethereum/execution-specs/tree/main/tests/benchmark).
- [evm-gasfit](https://github.com/misilva73/evm-gasfit) and the
  [EIP-7904 analysis pipeline](https://github.com/misilva73/eip-7904-repricing).
- [evm2](https://github.com/alloy-rs/evm2).
