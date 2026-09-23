# Standalone Osaka EVM2 benchmark methodology

This campaign measured how transaction runtime grows as a controlled workload performs more operations. It then evaluated whether the existing Osaka gas charges fund that measured work at a target of **600 million gas per second**.

The measurements support qualified **whole-workload cost estimates**. They do not establish isolated opcode or precompile costs: none of the 433 runnable target variants qualified for the complete supporting-cost subtraction analysis. The nine proposed price increases came from a separate workload-budget analysis, with explicit coverage and statistical requirements.

## Scope and campaign identity

This document describes the standalone campaign captured on **2026-09-22** using the `feat/osaka-evm2-compute-pipeline` workflow. The frozen artifacts, rather than later changes to repository defaults, define what ran.

| Item | Captured value |
| --- | --- |
| Execution mode | Standalone EVM2 Osaka interpreter |
| Capture ID | `compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2` |
| Analysis ID | `ba6bcc97-7b65-4be1-828e-8321e28b3c79` |
| EVM2 revision | `c0dfb22c27044e82e83f42f299525f740b16c1a3` |
| Workload SHA-256 | `5051433bf7c8f0bc7c2e5ff197a00138706e9f53e76cf88be417d28d656edae7` |
| Pricing anchor | `600000000` gas/s, or `0.6` gas/ns |
| Campaign seed | `20260922` |
| JIT | Disabled |
| Production gas constants | Unchanged |

This is not the built-in `cargo bench -p evm2-cli --bench evm` suite, the later Reth + EVM2 integration campaign, or a full-node throughput benchmark.

The [completed pricing report][pricing-report] records the recommendations and limitations. The [capture configuration][capture-config] and [corpus accounting][corpus-freeze] record the actual experiment settings.

## Contents

1. [Basic concepts](#1-basic-concepts)
2. [Component responsibilities](#2-component-responsibilities)
3. [Workload selection](#3-workload-selection)
4. [Workload generation and count grids](#4-workload-generation-and-count-grids)
5. [The portable workload format](#5-the-portable-workload-format)
6. [Diagnostic counting and correctness](#6-diagnostic-counting-and-correctness)
7. [The execution timer](#7-the-execution-timer)
8. [Sampling and machine controls](#8-sampling-and-machine-controls)
9. [What Benchmarkoor passes to evm-gasfit](#9-what-benchmarkoor-passes-to-evm-gasfit)
10. [Runtime modeling and the intercept](#10-runtime-modeling-and-the-intercept)
11. [Uncertainty and model qualification](#11-uncertainty-and-model-qualification)
12. [Separating isolated costs from workload budgets](#12-separating-isolated-costs-from-workload-budgets)
13. [Turning runtime into gas recommendations](#13-turning-runtime-into-gas-recommendations)
14. [Results and interpretation limits](#14-results-and-interpretation-limits)
15. [Artifacts and reproducibility](#15-artifacts-and-reproducibility)

## 1. Basic concepts

### EVM bytecode and opcodes

A deployed Ethereum contract contains EVM bytecode: instructions such as `ADD`, `DIV`, `KECCAK256`, and `STATICCALL`. EVM2 is an implementation of the machine that executes those instructions.

The worker itself was an optimized native executable built from Rust. It nevertheless **interpreted contract bytecode**; it did not use just-in-time (JIT) compilation to turn contracts into native machine code.

### Precompiles

Precompiles are native implementations of selected operations exposed at special Ethereum addresses. Examples include BN254 arithmetic, BLS operations, and SHA256.

A benchmark contract can repeatedly call a precompile. The surrounding bytecode runs through the EVM, while the cryptography runs in a native library. The campaign therefore measured both the native operation and the EVM work needed to invoke it.

### Gas and Osaka

Gas is Ethereum's resource-accounting unit, not a direct measurement of elapsed time.

- The **gas limit** is the maximum gas a transaction may consume.
- **Charged gas** is the amount reported as used by the executed transaction.
- **Osaka** selects the execution rules, available operations, gas schedule, and transaction limits.

The campaign measured elapsed time independently. Gas limits controlled whether workloads were executable; observed charged gas was used later in budget analysis. A gas limit was never substituted for gas consumed.

## 2. Component responsibilities

| Component | Responsibility |
| --- | --- |
| EEST / execution-testing | Define parameterized tests and generate contract bytecode, initial state, transactions, and fixtures |
| EELS Python reference implementation | Execute generated tests during filling and supply independent reference outcomes |
| Benchmarkoor | Coordinate generation, worker sessions, resource limits, sample schedules, result reconciliation, and analysis |
| Standalone `evm2-bench` worker | Execute prepared transactions directly in EVM2 and emit per-sample results |
| evm-gasfit | Join measurements and counts, fit runtime models, estimate uncertainty, and check model quality |
| Pricing postprocessor | Apply the 600M gas/s anchor, gas-accounting rules, coverage requirements, and change policy |

In this checkout, the Ethereum Execution Spec Tests (EEST) framework and benchmark definitions were available through the `execution-specs` repository. Its `fill` command materialized the tests. The captured generator invocation did not pass `--evm-bin`, so the reference transition backend was the in-repository **Ethereum Execution Layer Specification (EELS)** Python implementation, not EVM2, Geth, or Reth.

Reference execution time was not benchmark time. EELS supplied expected outcomes; a separate EVM2 process supplied the performance measurements.

Three principal container images separated generation, execution, and analysis. Container images froze software dependencies, but did not eliminate host scheduling, clock-frequency variation, or virtualization effects.

## 3. Workload selection

The target allowlist contained seven compute families:

- Arithmetic, including operand-sensitive variants.
- Bitwise operations.
- Comparisons.
- Stack operations.
- Control flow.
- KECCAK256.
- Precompiles, including Osaka P256VERIFY at address `0x100`.

Dedicated memory, storage, account-access, contract-lifecycle, broader stateful, and post-Osaka workloads were outside the target-pricing scope. Supporting memory and call operations could still occur inside a selected compute workload or in the separate calibration set.

### Family, variant, case, and sample

| Term | Meaning | Example |
| --- | --- | --- |
| Family | Broad class of work | Precompiles |
| Variant | One operation with a particular input shape and parameters | BLS G1 addition with changing inputs |
| Case or count point | One variant at one requested amount of work | That variant called 64 times in one transaction |
| Sample | One execution of one case | Repetition 2 in qualification session 3 |

Input size and repetition count are different dimensions. For example, BLS multi-scalar multiplication (MSM) with 128 points is a different variant from MSM with one point. Within either variant, the benchmark can vary how many times it invokes the precompile.

### Inventory and unsupported cases

The inventory selected **437 target variants**: 433 runnable and four unsupported.

The four unsupported variants remained visible with reasons:

- CLZ changing-input variant: no fixed-work export generator.
- P256VERIFY changing-input variant: no fixed-work export generator.
- Two 1,024-byte MODEXP variants: one call required 58,687,488 gas, exceeding the transaction's forwardable allowance and the Osaka transaction cap.

No gas limit or EVM validation was disabled to force those cases through. Their absence from timing models is not evidence that the same computation is cheaply executable under the current rules.

## 4. Workload generation and count grids

The experiment varied the amount of target work, rather than merely repeating one fixed transaction many times.

### Generation stages

The [campaign operator][campaign-operator] performed these stages:

1. Generate a small inventory point for every selected variant. The requested count was `0.001K`, meaning one target operation.
2. Run diagnostics to obtain execution and charged-gas evidence.
3. Generate the separate supporting-calibration workloads.
4. Select and generate target count grids.
5. Assemble the cases, retain unsupported records, reject identity collisions, and validate coverage.
6. Run a diagnostic preflight over the assembled corpus and freeze its identity before capture.

The exporter canonicalized case identities across fixture formats. Where equivalent `blockchain_test` and `blockchain_test_engine` fills existed, they did not become duplicate benchmark variants. The inventory included both formats; the final target grid shards selected the engine fixture format before exporting the portable workload.

### Ordinary opcode counts

The main requested count grid was:

```text
250, 500, 1,000, 2,000, 4,000
```

The special `test_keccak_max_permutations` workload used:

```text
125, 250, 500, 750, 1,000
```

These are target-operation counts, not total executed opcode counts or bytecode lengths. A transaction containing 1,000 target operations also executes supporting instructions.

The generator's `--fixed-opcode-count` argument is expressed in thousands: `0.25` requests 250 target operations.

### Precompile counts

Expensive precompile variants required smaller grids. The planner used the inventory's one-call charged-gas observation to screen candidate powers of two against a **10,000,000-gas planning budget**. It selected up to the five largest feasible powers, from a candidate range of 1 through 1,024 calls.

The actual frozen groups ranged from `[1]` through `[1, 2, 4, 8, 16]` to `[16, 32, 64, 128, 256]`. The planning screen was conservative and included the inventory transaction's overhead. It was not an exact prediction of the eventual transaction's gas usage. Filling still had to enforce the real execution limits.

The [frozen grid plan][grid-plan] records every selected variant and count grid.

### Gas allowance versus observed usage

Every runnable case in this campaign contained **one transaction with gas limit 16,777,216**, equal to `2^24`. This is the captured campaign setting, not the generic pipeline's default allowance.

The transaction did not have to consume that allowance. For example, the regular BLS G1-addition cases produced these diagnostic observations:

| Observed precompile calls | Charged transaction gas |
| ---: | ---: |
| 16 | 34,988 |
| 32 | 42,876 |
| 64 | 58,652 |
| 128 | 90,204 |
| 256 | 153,308 |

### Supporting instructions and wrapper shape

A generated precompile workload typically copied input into memory, invoked the selected precompile, handled the call result, and repeated the target work. Instructions such as `CALLDATACOPY`, `GAS`, `STATICCALL`, `POP`, and loop control were part of the executed transaction.

For ordinary opcode workloads, the generator could deploy a smaller inner contract and repeat it through an outer caller to remain within bytecode-size limits. Consequently, increasing the target count could also change the composition of the supporting work.

The requested count was a construction goal. The actual diagnostic target count was the analysis input. Fill-time count checks were conditional on the reference backend supplying count data; they were not a second independent opcode-counting validation for this campaign.

### Supporting calibration

Calibration used a separate role, `campaign_role: calibration`, rather than extending the target allowlist. It produced **19 supporting variants and 94 cases**.

These included stack, calldata, memory, and call-support drivers. Their purpose was to estimate the execution costs of instructions surrounding the target. They were not independently proposed as new target prices.

## 5. The portable workload format

The standalone worker consumed schema-version-2 **`workload.json`**, not Engine API requests or complete blockchain fixtures.

The root recorded the Osaka fork, generator revision, seed, and sorted cases. Each runnable case contained:

| Field | Meaning |
| --- | --- |
| `id`, family, target, parameters | Variant identity, requested count, role, and relevant source parameters |
| `pre` | Initial account balances, nonces, bytecode, and storage |
| `transactions` | Sender, recipient, calldata, value, and gas limit |
| `expected` | Expected transaction success/failure, logs, and selected storage values |
| Target-count metadata | The opcode or exact precompile-address key to count |

The initial state is called the **prestate**. It describes the accounts before the transaction executes.

This export intentionally narrowed general EEST semantics. It did not transfer full block headers or Ethereum state-root expectations. The worker used a default block environment and constructed chain-ID-1, zero-gas-price legacy call envelopes from the declared transaction intent. The sender was supplied directly; cryptographic transaction-signature recovery was outside this protocol.

The frozen corpus contained:

| Case class | Count |
| --- | ---: |
| Runnable target count points | 2,138 |
| Runnable calibration count points | 94 |
| Total runnable cases | 2,232 |
| Unsupported records | 4 |
| Total workload records | 2,236 |

These workload cases are inputs to the execution worker. They are not the same thing as the much larger collection of repeated sample records.

## 6. Diagnostic counting and correctness

### Separate diagnostic execution

Diagnostic mode installed an inspector that counted executed instructions. `SHA3` was canonicalized to `KECCAK256`.

For a precompile target, the inspector counted calls to the exact enabled Osaka precompile address. The key had the form:

```text
PRECOMPILE_0x0000000000000000000000000000000000000006
```

That example identifies BN254 addition. It is not an aggregate count of all `STATICCALL` instructions, which could also include wrapper calls.

Diagnostic rows contained counts but deliberately exposed no performance duration. Pilot, warmup, and qualification executions did not install the inspector. This kept instruction-counting overhead out of the performance measurements.

Benchmarkoor linked timed rows to successful diagnostic observations using matching prepared-workload, baseline, and result-commitment identities. The counts were therefore associated with the same prepared case without being collected inside its timed executions.

EELS supplied independent expected outcomes. This campaign did not supply a second independent opcode counter.

### Correctness checks

After the timed region, the worker compared:

1. Expected and observed transaction success/failure.
2. Exact emitted logs, including address, topics, and data.
3. The explicitly listed account/storage-slot/value witnesses.

A mismatch made the sample ineligible, even if its runtime was fast. Passing these checks did not constitute a full independent comparison of every account or storage slot, nor a general proof of EVM correctness.

The worker also hashed observed receipts and an account/storage snapshot for reproducibility and linkage. This commitment was a JSON-derived SHA-256 digest, not an Ethereum state root and not an independent correctness oracle.

### Failure handling

The worker emitted one terminal JSONL record per requested sample. Benchmarkoor reconciled those records against the frozen request identities.

Missing results, duplicate sample IDs, malformed records, unexpected identities, nonzero exits, OOMs, and cancellations became explicit failures. Failed samples were not silently replaced by successful retries under the same sample ID. Unsupported cases retained their reasons.

## 7. The execution timer

For each sample, the worker located its prepared case, cloned the prescribed prestate, constructed a new Osaka EVM, and prepared an in-memory destination for state changes.

The measurement boundary, named `evm2_transaction_execution`, was:

```text
Outside timer:
    Read and prepare the workload
    Clone the initial state
    Construct the EVM and result storage

Start timer
    Validate and execute the prepared transaction
    Settle the transaction
    Apply its state changes to the in-memory database
Stop timer

Outside timer:
    Check expected outcomes
    Collect result metadata and hashes
    Serialize the sample record
```

The actual worker timed `transact(...)` followed by `commit_with(...)`, using a monotonic clock and recording elapsed wall time in nanoseconds.

| Included in the timer | Excluded from the timer |
| --- | --- |
| EVM transaction validation | Workload-file parsing and transaction preparation |
| Contract execution | Prestate cloning and EVM construction |
| Native precompile execution | Expected-outcome verification |
| Transaction settlement | Result hashing and serialization |
| Applying state changes in memory | Signature recovery |
| | Networking and Engine API orchestration |
| | Persistent database writes and Ethereum state-root calculation |

Here, **commit means an in-memory state update**. It does not mean writing a block to a persistent database.

### State reset does not imply cold caches

Every sample started from a fresh clone of its prestate. One sample could not leave changed account balances or storage values for the next sample.

CPU caches, allocator state, and process-global library caches could nevertheless remain warm. Restoring Ethereum state is not equivalent to flushing machine or library caches.

Warmup and measured repetitions shared a qualification process, but every sample still received a new EVM and restored baseline state.

## 8. Sampling and machine controls

### Frozen schedule

The final capture used the following schedule for each runnable case:

| Phase | Purpose | Executions per runnable case | Included in timing models? |
| --- | --- | ---: | --- |
| Diagnostic | Count operations and establish execution identities | 1 | No |
| Pilot | Rehearsal timing in separate worker processes | 8 total | No |
| Warmup | Warm each qualification process | 8 total | No |
| Qualification | Collect measurement evidence | 8 sessions × 5 repetitions = 40 | Yes |

Each runnable case therefore had:

```text
1 diagnostic + 8 × (1 pilot + 1 warmup + 5 qualification) = 57 records
```

The final capture used **17 fresh worker processes**: one diagnostic process, eight pilot processes, and eight qualification processes. Warmup and qualification shared the same qualification worker. These sessions ran sequentially, rather than concurrently competing for CPU 14. Earlier generation, preflight, and smoke processes are outside this final-capture count.

Pilot results did not adapt the qualification schedule. The requests were frozen in advance. Case order was deterministically shuffled using seed `20260922`; within a qualification phase, its five repetitions reused that phase's shuffled case order.

### Captured record counts

| Record class | Count |
| --- | ---: |
| Diagnostic, including four unsupported cases | 2,236 |
| Pilot | 17,856 |
| Warmup | 17,856 |
| Qualification | 89,280 |
| All terminal records | 127,228 |
| Executed, correctness-passing records | 127,224 |
| Unsupported records | 4 |

Only executed, correctness-passing qualification rows were eligible for timing models. For a five-point variant, the model normally received `5 × 8 × 5 = 200` measured observations. The analysis did not simply choose the fastest run.

### Machine and build

The host was an Intel Xeon Platinum 8559C under KVM, exposing 16 cores and 32 logical CPUs. Worker containers were pinned to **logical CPU 14**, allowed 24 GiB of memory, and had swap disabled. The controller was pinned to CPU 0. The per-session timeout was eight hours.

The worker used the optimized `release` build, EVM2's interpreter, MCL 1.2.0 for BN254 arithmetic, and blst 0.3.17 for BLS. It did not enable JIT.

Generation excluded CPU 14 and its reported simultaneous-multithreading (SMT) sibling, CPU 30. The sibling was not reserved at the host level. Virtual-machine scheduling, shared resources, and frequency variation remained limitations.

## 9. What Benchmarkoor passes to evm-gasfit

The two main measurement inputs were **`runtimes.csv`** and **`opcounts.json`**. Benchmarkoor exported them from the archived worker results. Configuration, workload metadata, raw samples, and provenance accompanied them.

### Runtime rows

This excerpt uses actual campaign values. Fixture names are shortened to `DIV_250` and `DIV_500`, and extra columns are omitted; these abbreviations are not literal exported fixture IDs.

```csv
client_name,fixture_name,test_runtime_ms,session_id,phase,status,correctness_passed,charged_gas
evm2,DIV_250,0.013724,qualification-00,qualification,executed,true,26371
evm2,DIV_500,0.024236,qualification-00,qualification,executed,true,28371
```

Each row is one execution, not an average. Runtime is expressed in **milliseconds**. Session identity is retained for uncertainty estimation.

The full file also includes sample ID, repetition, execution boundary, baseline/prepared/commitment hashes, declared gas, and workload parameters. It retains other phases and ineligible records; the analyzer's configuration determines which rows enter models. Diagnostic rows have no runtime value.

### Operation counts

The corresponding excerpt from the diagnostic-derived count mapping is:

```json
{
  "DIV_250": {
    "opcount": 250,
    "DIV": 250,
    "DUP2": 252,
    "STATICCALL": 1,
    "STOP": 2
  },
  "DIV_500": {
    "opcount": 500,
    "DIV": 500,
    "DUP2": 502,
    "STATICCALL": 1,
    "STOP": 2
  }
}
```

Additional instruction counts are omitted here. `opcount` is the observed target count. Other entries describe instructions executed by that workload, including supporting work.

The analyzer joins the CSV's `fixture_name` to the JSON object key. It then has observations such as 250 DIVs taking 13.724 microseconds and 500 DIVs taking 24.236 microseconds.

Configuration identifies the target operation and variant, eligible sample phases, statistical requirements, and pricing policy. The actual [runtime input][runtimes] and [count input][opcounts] preserve the full identities and fields.

## 10. Runtime modeling and the intercept

For one fixed variant, the basic model was:

$$
T(N) \approx a + bN
$$

- `N` is the observed number of target operations.
- `T(N)` is the measured transaction runtime.
- `a` is the estimated count-independent part of that runtime, called the **intercept**.
- `b` is the extra runtime associated with each additional target operation, called the **slope**.

The analyzer used **nonnegative least squares**. It fitted the intercept and cost coefficients under non-negativity constraints. Session IDs were grouping metadata for uncertainty estimation, not additional regressors in this basic model.

### What the intercept means

The intercept accounts for measured work that does not grow as `N` increases. Possible contributors include transaction validation, setup instructions executed once, and fixed portions of settlement and in-memory commit.

It cannot include fixture parsing or EVM construction in this experiment, because those occurred outside the timer. It is also not a separately timed function: the model infers it from the observations. If linearity holds, it describes the fitted runtime at `N = 0`, which need not have been measured.

Using only the two real DIV observations above gives an illustrative two-point calculation:

$$
b = \frac{24.236 - 13.724}{500 - 250}
  = 0.042048\ \text{microseconds}
  = 42.048\ \text{ns}
$$

$$
a = 13.724 - 250 \times 0.042048
  = 3.212\ \text{microseconds}
$$

| Target count | Inferred fixed portion | Count-dependent portion | Observed total |
| ---: | ---: | ---: | ---: |
| 250 | 3.212 microseconds | 10.512 microseconds | 13.724 microseconds |
| 500 | 3.212 microseconds | 21.024 microseconds | 24.236 microseconds |

**These are illustrative two-point coefficients, not the campaign's final fitted DIV intercept or slope.** The actual model used all eligible repetitions and count points, with statistical qualification. A two-point calculation alone does not establish a reliable operation cost.

Without an intercept, dividing total runtime by operation count would assign part of the fixed transaction overhead to every operation. That particularly inflates the apparent cost of small workloads.

### Why the raw slope is not an isolated opcode cost

If each extra target operation needs additional stack manipulation, calls, or loop control, that support also grows with `N`. It belongs in the slope:

$$
b_{\text{raw}} \approx b_{\text{target}} + b_{\text{support}}
$$

The intercept cannot remove work that repeats with every target operation. The raw slope therefore measures additional **whole-workload time per counted target**, not automatically the execution cost of the target alone.

The linear relationship is also an assumption to test. Changing wrapper shapes, cache behavior, or input-dependent costs can invalidate it.

## 11. Uncertainty and model qualification

Repeated observations within one process share machine conditions. Treating every row as fully independent would overstate the amount of independent evidence.

The analyzer used a **session-cluster bootstrap**:

1. Group observations by qualification session.
2. Resample whole sessions with replacement.
3. Refit the model.
4. Repeat 1,000 times with the recorded seed.
5. Derive 95% coefficient confidence intervals.

This captures between-session variation present in the experiment. It does not quantify every possible hardware, software, or production-workload difference.

### Frozen acceptance checks

| Check | Captured requirement | Purpose |
| --- | --- | --- |
| Independent sessions | At least 4; 8 captured | Require process-level replication |
| Overall fit quality | R-squared at least 0.5; coefficient significance checks | Reject poorly supported models |
| Condition number | At most `1e8` | Reject unstable or poorly distinguishable designs |
| Residual-curvature R-squared | At most 0.1 | Detect systematic structure left after fitting a line |
| Relative coefficient interval width | At most 0.5 | Reject excessive uncertainty |
| Held-out session error | At most 0.25 | Check prediction outside the fitted sessions |
| Held-out count-point error | At most 0.25 | Check prediction at an omitted workload size |

The held-out checks refit after excluding an entire session or an entire distinct target count. They did not randomly split near-duplicate repetitions into training and validation sets.

A narrow confidence interval or high overall R-squared did not override another failed check. Models with insufficient count variation could not identify a slope, regardless of how many times the same point was repeated.

The result was **403 qualified raw workload models and 30 inconclusive models** among 433 runnable target variants. The 30 included 24 curvature-related failures, four holdout failures, and two constant-count grids. Failed gates were not relaxed to obtain more price recommendations.

Confidence intervals were pointwise. Taking maxima across many intervals did not create a simultaneous 95% guarantee for the entire proposed gas schedule.

## 12. Separating isolated costs from workload budgets

Two different analyses were maintained. Their outputs must not be conflated.

### Isolated-cost analysis

The first approach tried to subtract supporting execution costs:

$$
b_{\text{target}} = b_{\text{raw}} - b_{\text{support}}
$$

The 19 calibration variants were intended to estimate stack, calldata, memory, and call-support costs. A defensible subtraction required the relevant supporting measurements and their uncertainty to qualify and match the target's actual instruction composition.

That complete chain did not qualify:

- The POP driver's signal was overwhelmed by noise; its fit R-squared was about 0.05.
- A joint call-support model depended on that unresolved POP contribution, despite its own high R-squared.
- Some wrappers changed instruction composition across count points.
- Some supporting shapes did not match the target's actual call path.

**All 433 adjusted target estimates were withheld.** Missing support was not treated as free execution, and raw workload slopes were not renamed as isolated costs.

### Whole-workload gas-budget analysis

The second approach asked whether the measured workload received enough gas in total. It did not require an isolated CPU-time estimate for every supporting instruction.

For a variant, it checked the observed charged gas against:

$$
G(N) = G_0 + a_gN
$$

`G_0` is fixed transaction gas. `a_g` is the additional total gas charged for each additional target operation. This gas equation is distinct from the runtime equation and its intercept `a`.

If the current target charge is `g`, the existing marginal gas paid for other work is:

$$
h = a_g - g
$$

This is a **gas credit**, not an estimate of supporting CPU time. Subtracting it from a required gas budget avoids charging the target again for budget already paid by the wrapper.

The charged-gas relation had to be exact across the observed count points. The postprocessor compared adjacent slopes using rational arithmetic, rather than accepting an approximately straight gas curve. It also required a known target charge and nonnegative supporting credit.

The captured grids comprised 218 exact-affine, 213 non-affine, and two single-count target grids. Additional statistical, cache, and input-mapping requirements reduced the set eligible for pricing. Non-affine or otherwise unsupported cases remained visible but could not produce strict budget recommendations.

## 13. Turning runtime into gas recommendations

### The conversion anchor

The chosen pricing anchor was:

$$
600{,}000{,}000\ \text{gas/s} = 0.6\ \text{gas/ns}
$$

For a raw runtime slope with lower and upper 95% bounds `L` and `U`, existing target charge `g`, and supporting-gas credit `h`, the budget analysis used:

$$
\text{lower discrepancy} = \frac{0.6L - h}{g}
$$

$$
\text{required target budget} = \max(g,\ 0.6U - h)
$$

The discrepancy equation applies when `g` is positive; zero-charge cases were handled separately. Final candidates were rounded according to the policy below.

These equations concern **marginal workload growth**. They do not certify the fixed cost of small transactions, complete block processing, or 600M gas/s full-node throughput.

### Change policy

The frozen policy was:

1. Use the 600M gas/s anchor without an additional discretionary margin.
2. Make no decreases.
3. Trigger an increase only when a qualified lower confidence bound supports at least a 2× discrepancy from the current target charge.
4. Size the candidate against the 95% upper runtime bound.
5. Round final physical gas parameters upward to two significant digits and integer gas.
6. Preserve existing formula shapes and discount tables where the evidence supports doing so.
7. Require complete evidence across every selected variant in the pricing group before accepting a group-wide recommendation.

The 2× trigger intentionally favors fewer changes. A price retained by this policy is not necessarily adequate for the 600M anchor. Coverage refers to the selected variants, not every valid input.

### Worked example: BLS G1 addition

The regular G1-addition diagnostic observations listed earlier satisfy:

$$
G(N) = 27{,}100 + 493N
$$

The current precompile charge was 375 gas, so:

$$
h = 493 - 375 = 118\ \text{gas per call}
$$

The changing-input variant also had 118 gas of marginal supporting credit. Its raw workload slope was **3,654.69 ns per counted call**, with a 95% interval of **3,621.92 to 3,696.85 ns**.

Using its upper bound:

$$
0.6 \times 3{,}696.85 = 2{,}218.11\ \text{gas}
$$

Crediting the existing supporting gas:

$$
2{,}218.11 - 118 = 2{,}100.11\ \text{gas}
$$

Rounding upward to two significant digits gives **2,200 gas**. The regular variant required less; the shared candidate had to cover both selected variants.

The lower-bound trigger was:

$$
\frac{0.6 \times 3{,}621.92 - 118}{375} \approx 5.48
$$

This exceeded the 2× threshold. Under the candidate charge, the changing-input variant's modeled marginal rate was:

$$
\frac{2{,}200 + 118}{3{,}696.85\ \text{ns}} \approx 627\ \text{Mgas/s}
$$

This example is a workload-budget calculation. It does not establish that the native G1-addition primitive alone takes 3,696.85 ns. The [reviewed schedule][reviewed-schedule] retains the exact bounds, credits, and calculations for every accepted variant.

### Shared formulas

For operations priced by input size, round count, pair count, or MSM size, one shared formula had to cover the selected variants. The postprocessor did not assign each sampled input an unrelated price.

The final evaluation proposed nine precompile-parameter increases supported by 43 variants. The BLS MSM discount tables remained unchanged. Review solved the G2 MSM coefficient directly against required fees, avoiding an extra rounding step in a generic scale-then-coefficient calculation.

No production constants were changed, and this was not a complete validated Osaka gas schedule.

## 14. Results and interpretation limits

### Successful execution is different from qualified pricing

All 127,224 runnable sample records passed their exported correctness checks. That did not make every fit usable. The capture completed successfully while the isolated-cost analysis remained inconclusive.

The final group inventory recorded nine increases, one retain decision with complete sampled budget coverage, 44 blocked groups, and one rejection-path group. A blocked recommendation was not evidence that the existing price was safe.

### Process-global KECCAK cache

The worker enabled alloy-primitives' process-global KECCAK cache. In the pinned implementation on this architecture, 1–87-byte inputs were cache-eligible. Empty input used its fixed hash, while inputs above 87 bytes bypassed the cache.

Restoring EVM state did not clear this cache. All 19 eligible short-input target variants were excluded from worst-case pricing evidence. A workload name containing `uncachable` was not accepted as proof of measured cache misses. The campaign did not record cache hit/miss statistics or establish adversarial cold-cache cost.

### Nonuniform workloads and input mapping

Some supporting instruction compositions changed with the count grid, producing nonlinear runtime or non-affine gas relationships. A raw slope therefore does not automatically transfer to a cheaper wrapper or different contract composition.

Some EXP workloads fed one result into the next exponent. The initial exponent parameter was only a seed, not a constant per-call exponent size. The postprocessor withheld a scalar target charge for those evolving-input variants rather than misclassifying their changing target gas as supporting gas.

Two expensive MODEXP variants had only one feasible count point. Additional repetitions at that one point could not identify a slope.

### Reference and correctness coverage

The oracle checked exported receipt outcomes, logs, and selected storage witnesses. It did not establish full-state equivalence for every case, independently validate every diagnostic count, or cover unrepresented EVM behavior.

Successful expected rejection paths were not treated as measurements of valid expensive computation for pricing purposes.

### Hardware and software specificity

The results describe the pinned interpreter, native libraries, compiler build, machine, and sampling conditions. They do not automatically transfer to an ARM machine, a JIT execution path, or another backend combination.

In particular, the later Reth integration used a different EVM2 revision, an arkworks BN254 backend, a different build profile, and an execution-plus-state-root timer. Differences between those campaigns cannot be attributed solely to Reth overhead.

### Relation to the built-in benchmark suite

The built-in `cargo bench -p evm2-cli --bench evm` command uses bundled transaction fixtures and a blockchain replay case, with mixed forks and Criterion sampling. It is useful for timing those fixed workloads.

This campaign instead used generated Osaka count grids, separate operation-count diagnostics, independent worker sessions, and statistical/pricing qualification. A built-in benchmark's transaction time is not directly interchangeable with a fitted marginal workload slope.

## 15. Artifacts and reproducibility

The archived evidence includes:

| Artifact | Purpose |
| --- | --- |
| `workload.json` | Frozen generated cases, prestate, transactions, and expected outcomes |
| `requested-samples.jsonl` and session requests | Exact planned sample identities, phases, and ordering |
| `samples.jsonl` | Every terminal worker result, including unsupported or failed outcomes |
| `manifest.json` and `config.json` | Resource settings, identities, capture status, and provenance |
| Analyzer `runtimes.csv` and `opcounts.json` | Measurement and diagnostic-count inputs |
| Analyzer `config.yaml` | Frozen modeling, eligibility, qualification, and pricing settings |
| `results.csv` and `qualification.csv` | Raw fits, intervals, acceptance status, and rejection reasons |
| Glue reports | Supporting-cost estimates and unresolved adjustment requirements |
| `reviewed-schedule.json` | Accepted gas-budget calculations and final physical parameters |

The base revisions were:

| Repository | Base revision |
| --- | --- |
| Benchmarkoor | `fda9738ed7e8524945ab5b8c2f407388b11ce03f` |
| execution-specs | `6ee9c7854ca22cb8f5259bac2fa20892d6dfab06` |
| EVM2 | `c0dfb22c27044e82e83f42f299525f740b16c1a3` |
| evm-gasfit | `c40409e3c83e082d8629d0545e268d109833ef27` |

EVM2 was unchanged, but the generator, controller, and analyzer included local changes. **Base revisions alone do not reconstruct the campaign.** Frozen image digests, source fingerprints, configuration, and input artifacts are authoritative. The [final provenance record][final-provenance] distinguishes the capture analyzer from later recommendation-only corrections; those corrections did not rewrite the captured timings.

The worker did not expose an active gas-schedule fingerprint. The archive records Osaka, the cap, source and lock identities, and the worker image, but comparisons that require the missing fingerprint must remain blocked rather than inventing it.

Generated result artifacts referenced here are retained local workspace evidence and may not be present in a fresh repository clone. The methodology document does not claim that those artifacts have been published remotely.

The [pipeline overview][pipeline-overview] describes the reusable workflow. The [capture audit][capture-audit] verifies sample accounting and execution identities. The [pricing report][pricing-report] records all recommendations and the cases whose evidence remained insufficient.

[pricing-report]: docs/compute-gas-pricing-600m.md
[pipeline-overview]: docs/compute.md
[campaign-operator]: scripts/compute/pricing_campaign.py
[capture-config]: results/osaka-pricing-600m-20260922T163024Z/reviewed/config/compute.yaml
[corpus-freeze]: results/osaka-pricing-600m-20260922T163024Z/reviewed/corpus/freeze.json
[grid-plan]: results/osaka-pricing-600m-20260922T163024Z/reviewed/grids/plan.json
[runtimes]: results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/runtimes.csv
[opcounts]: results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/opcounts.json
[reviewed-schedule]: results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/reviewed-schedule.json
[capture-audit]: results/osaka-pricing-600m-20260922T163024Z/reviewed/full-campaign-main-review.json
[final-provenance]: results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/provenance.json
