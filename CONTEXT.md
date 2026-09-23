# Osaka evm2 compute benchmark pipeline

Date: 2026-09-23

## Goal

Repeat the completed standalone Osaka EVM2 compute campaign on a second machine,
preserving the first machine's data and using the same frozen workload, executable
images, controller, sample schedule, and analysis settings. Compare the two
machines before deciding how to proceed with compute gas-schedule changes.

Benchmarkoor owns orchestration and artifacts. execution-specs owns workload
construction and independent expected outcomes. evm2 owns execution and
operation counting. evm-gasfit remains a pinned dependency; benchmarkoor does
not copy its modeling logic.

## Current handoff

The completed 2026-09-22 campaign used **600 million gas/second** (0.6 gas/ns),
zero discretionary margin, no decreases, and a qualified lower-bound discrepancy
of at least 2× to trigger an increase. It captured 127,228 terminal records:
127,224 executed and correctness-passing samples plus four unsupported cases.
Of 433 runnable target variants, 403 raw workload models qualified on that host.
All isolated, supporting-cost-adjusted estimates were withheld. Nine provisional
precompile-parameter increases came from the separate whole-workload budget
analysis. Production gas constants remain unchanged.

Read [the second-machine handoff](docs/compute-handoff.md) for the transfer,
configuration, smoke, full capture, analysis, and verification procedure.
Read [the methodology](METHODOLOGY.md) for the experimental design and
[the first-machine report](docs/compute-gas-pricing-600m.md) for baseline results.

The older 500M gas/s, document-only transfer, fresh-generation, and glue-disabled
instructions are superseded. Transfer the frozen corpus, analysis inputs,
controller binary, exact container images, and first-machine archive. A fresh
clone at the base source revisions cannot reproduce the local changes or ignored
lockfiles. Do not substitute rebuilt images or newly generated workloads for an
exact hardware comparison.

On the second machine, generate fresh diagnostics and all pilot, warmup, and
qualification measurements in a new output directory. Keep the transferred
first-machine archive unchanged and out of the second machine's timing fits.
Adapt host paths and CPU affinity deliberately; retain the 24 GiB/no-swap worker
limit and all experimental settings. Record any unavoidable deviation rather
than silently claiming an exact replay. The second-machine run is planned, not
yet completed. Do not choose a cross-machine aggregation rule or finalize gas
changes before reviewing both datasets.

## Scope

The eligible corpus is the existing Osaka-compatible arithmetic, bitwise,
comparison, stack and control-flow, KECCAK256, and precompile benchmarks. This
includes the Osaka P256VERIFY precompile at address `0x100`. Campaign selection
may narrow this allowlist but cannot widen it.

Dedicated memory, storage, account-access, contract-lifecycle, block-access-list,
scenario, and stateful benchmarks are excluded. Unsupported selected cases stay
in `workload.json` with an explicit reason.

The campaign measures standalone evm2. It does not integrate NewL1 or Reth,
enable JIT, change gas constants, recommend a block gas limit, or price
non-compute resources.

## Contracts

Protocol v2 is defined by the Go types and JSON Schemas under
`pkg/compute/`. Workloads declare Osaka prestate, recovered transaction intent,
expected receipt outcomes, and expected storage. Transactions identify the
sender directly; private keys and chain-specific block envelopes are absent.

The evm2 worker runs `evm2-bench --request <request.json> --output
<samples.jsonl>`. Diagnostic samples use an inspector to report exact opcode and
addressed-precompile counts. Timed samples run without the inspector.

The `evm2_transaction_execution` boundary includes transaction validation,
execution, settlement, and state commit. Workload parsing, prestate preparation,
baseline cloning, EVM construction, correctness checks, and artifact hashing are
outside the timer.

Every requested sample produces one terminal result. Executed results require a
passing oracle, valid artifact hashes, declared and charged gas, and a positive
duration for pilot, warmup, and qualification phases. Diagnostic executions may
omit duration but must report the semantic target count. Failed and unsupported
results carry an explicit stage and message.

## Reproducibility

A campaign freezes its sample schedule before execution. Each qualification
session uses a fresh worker process and restores the declared baseline before
each sample. The manifest records workload and image identities, hardware and
resource controls, execution boundary, and analysis policy. A binary/image replay
retains original source provenance separately; it must not pretend that an absent
local source checkout was inspected. The worker's active gas-schedule fingerprint
is unavailable, so native comparisons requiring that field remain blocked.

Only correct `qualification` rows are eligible for fitting. Session identifiers
are preserved for cluster bootstrap and held-out-session checks. Analyze each
host independently using the frozen 600M policy. Do not merge equal-named sessions
from different hosts, copy first-host coefficients into the new analysis, or
relax failed qualification gates.

## Acceptance

- Verify transferred file checksums, image IDs, controller digest, and the frozen
  workload SHA-256 before running.
- Pass the archived mixed-lane smoke with fresh second-host measurements.
- Capture all 2,232 runnable cases and four unsupported records with eight
  sessions, one pilot, one warmup, and five qualification repetitions.
- Reconcile all 127,228 requested samples, phase counts, session exits, correctness
  results, and per-case identities; retain failures instead of silently retrying.
- Verify diagnostic target/opcode counts, charged gas, and execution commitments
  against the first host; timed samples must omit diagnostic counters.
- Retain separate immutable capture and analysis artifacts for both machines.
  Statistical qualification counts and proposed prices may differ by host.
- Return both datasets and host metadata for comparison. A completed capture or
  successful smoke is not approval to change the production gas schedule.
