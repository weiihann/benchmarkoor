# Osaka evm2 compute benchmark pipeline

Date: 2026-09-22

## Goal

Build a reproducible pipeline that exports fixed-work Osaka compute workloads
from execution-specs, executes them with evm2, records repeated measurements,
and analyzes the archived results with evm-gasfit.

Benchmarkoor owns orchestration and artifacts. execution-specs owns workload
construction and independent expected outcomes. evm2 owns execution and
operation counting. evm-gasfit remains a pinned dependency; benchmarkoor does
not copy its modeling logic.

## Scope

The eligible corpus is the existing Osaka-compatible arithmetic, bitwise,
comparison, stack and control-flow, KECCAK256, and precompile benchmarks. This
includes the Osaka P256VERIFY precompile at address `0x100`. Campaign selection
may narrow this allowlist but cannot widen it.

Dedicated memory, storage, account-access, contract-lifecycle, block-access-list,
scenario, and stateful benchmarks are excluded. Unsupported selected cases stay
in `workload.json` with an explicit reason.

The milestone measures standalone evm2. It does not integrate NewL1, change gas
constants, recommend a block gas limit, or price non-compute resources.

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
each sample. The manifest records source revisions and dirty-state evidence,
lockfile hashes, workload identity, image identities, hardware and resource
controls, execution boundary, Osaka gas-table selection, and analysis policy.

Only correct `qualification` rows are eligible for fitting. Session identifiers
are preserved for cluster bootstrap and held-out-session checks. Analysis is
anchorless unless the operator supplies an explicit gas-per-second anchor and
margin policy.

## Acceptance

- A real execution-specs Osaka export produces ready fixed-count workloads.
- evm2 executes ADD, KECCAK256, and Osaka P256VERIFY cases with independent
  receipt, log, and storage checks.
- Diagnostic counts match the selected semantic target.
- Timed rows carry positive durations and omit diagnostic counters.
- Benchmarkoor archives raw requests, results, manifest, gasfit inputs, and
  gasfit outputs without mutating source artifacts.
- The finite smoke campaign completes through generator, evm2 worker, and
  evm-gasfit analyzer.
