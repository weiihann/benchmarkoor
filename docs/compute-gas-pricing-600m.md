# Osaka compute gas-price recommendation at 600 Mgas/s

Date: 2026-09-22. Engine: the pinned standalone evm2 worker on an Intel Xeon Platinum 8559C host.

## Decision

Recommend **nine precompile parameter increases** for the next candidate gas schedule. Keep the existing formula shapes and BLS MSM discount tables. Make **no decreases**, no blanket opcode-tier increase, and no new BLAKE2F base charge.

The evidence supports these nine changes over the selected benchmark variants, using whole-workload gas accounting. It does **not** establish a complete 600 Mgas/s schedule. No target qualified as a fully isolated opcode/precompile cost. Significant unresolved discrepancies remain in integer division/modular arithmetic, pairings, KZG, P256, KECCAK, and ECRECOVER. Retaining a blocked price is a temporary evidence decision, not a finding that it is adequate.

All prices in this report are **gas units**, not gwei. The 600,000,000 gas/s target is a conversion anchor of **0.6 gas/ns** for this implementation and machine. It is not a measured full-node throughput or a block gas-limit recommendation. No production gas constants were changed.

### Recommended changes

| Operation | Current parameter | Recommended parameter | Qualified variants | Largest lower-bound discrepancy | Current / proposed minimum modeled Mgas/s |
| --- | ---: | ---: | ---: | ---: | ---: |
| BLAKE2F | 1 / round | **5 / round** | 10/10 | 4.57× | 131.1 / 635.6 |
| BLS G1 ADD | 375 | **2,200** | 2/2 | 5.48× | 133.4 / 627.0 |
| BLS MAP_FP_TO_G1 | 5,500 | **24,000** | 2/2 | 4.23× | 143.8 / 617.3 |
| BLS G1 MSM | 12,000 MSM coefficient | **70,000 MSM coefficient** | 5/5 | 5.74× | 104.3 / 607.8 |
| BLS G2 ADD | 600 | **3,400** | 2/2 | 5.11× | 124.5 / 609.9 |
| BLS MAP_FP2_TO_G2 | 23,800 | **77,000** | 2/2 | 3.22× | 186.6 / 601.6 |
| BLS G2 MSM | 22,500 MSM coefficient | **120,000 MSM coefficient** | 5/5 | 5.01× | 119.5 / 637.0 |
| BN254 ADD | 150 | **1,600** | 6/6 | 10.13× | 96.4 / 618.3 |
| BN254 MUL | 6,000 | **18,000** | 9/9 | 2.94× | 205.3 / 608.1 |

The throughput columns use each variant's **95% upper runtime-slope bound**, retain its measured non-target marginal gas, and show the minimum across that group's selected variants. They are modeled marginal workload rates, not whole-node measurements. All **43 variants** in these nine groups pass the raw qualification gates and exact-affine gas-accounting check. Every proposed fee covers its corresponding pointwise upper-bound budget; the minima are 601.6–637.0 Mgas/s after rounding.

The action artifact is [reviewed-schedule.json](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/reviewed-schedule.json). It contains every checked input size, current fee, supporting-gas credit, required fee, proposed fee, and resulting modeled rate. The complete machine-readable evidence is [recommendations.json](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/recommendations.json), [per-variant CSV](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/recommendations.csv), and [55-group inventory](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/group-inventory.json).

### Preserve the formulas

- BLAKE2F: change `gas = r` to **`gas = 5r`**, where `r` is the round count. Keep the base at zero.
- BLS G1 MSM: **`floor(k × 70,000 × D1(k) / 1000)`**.
- BLS G2 MSM: **`floor(k × 120,000 × D2(k) / 1000)`**.
- Preserve both 128-entry discount tables and their existing tails, `D1(k>128)=519` and `D2(k>128)=524`. The measured MSM sizes were **1, 16, 64, and 128**, plus a changing-input single-point variant. Larger sizes and all intermediate sizes are not thereby certified.
- The remaining six changes are fixed per-call fees, as listed above.

The G2 MSM coefficient is deliberately **120,000**, not 130,000. The generic candidate exporter first rounds a scale to 5.4; rounding `22,500 × 5.4` again would produce 130,000. Review instead solved the existing physical coefficient directly against all five required fees and rounded that coefficient once. A coefficient of 120,000 covers every required fee and retains a minimum modeled rate of 637.0 Mgas/s. This removes excess rounding without changing confidence bounds, qualification gates, coverage, the increase trigger, or the discount schedule. The same direct calculation gives 70,000 for G1 MSM.

## Change policy and accounting

The policy prioritizes a small change set over automatic alignment of every price with a timing estimate:

1. Use 600 Mgas/s throughout, with no discretionary margin.
2. Do not propose decreases.
3. Trigger an increase only when a **qualified lower confidence bound** supports a discrepancy of at least **2×** the current target charge.
4. Size the candidate against the **95% upper bound**, then round physical gas coefficients upward to two significant digits and integer gas.
5. Preserve existing formulas when they can cover the evidence. Withhold a formula that cannot cover a zero-charge boundary rather than silently introducing a base fee.
6. Keep isolated-cost and whole-workload-budget evidence separate. Do not rename a raw workload slope as an isolated cost.

This policy can retain a price that falls short of the 600 Mgas/s anchor by less than the trigger threshold. “Keep by policy” is not synonymous with “proven adequate.”

### Exact-affine budget lane

For a variant, let `N` be the native diagnostic target count, `G(N)` the charged transaction gas, and `g` the known current charge per target operation. The accepted grid must satisfy an **exact**, rational-arithmetic relation:

```text
G(N) = intercept + aN
h = a - g >= 0
```

Here `h` is gas already charged for the changing non-target work. It is **not** an estimate of that work's execution cost. For raw runtime slope `t` in ns/target, with bounds `L95` and `U95`:

```text
lower discrepancy = (0.6 × L95 - h) / g
required target fee = max(g, round_up(0.6 × U95 - h))
```

The group triggers at a lower discrepancy of at least 2, but a candidate is accepted only when **every selected variant in that group** has the required evidence. Unsupported variants, cache-risk inputs, unqualified fits, unknown current charges, and non-affine charged-gas grids remain visible coverage blockers. Zero current charges are handled separately, not by division.

This accounting avoids charging the target again for work its wrapper already pays for. It remains conditional on the measured workload shapes. Cheaper alternative wrappers, different operand sequences, unmeasured inputs, and other implementations need separate validation.

### Why BLAKE2F does not need a new base

The zero-round variant measured **164.48 ns/call**, with a 95% interval of **162.29–166.28 ns**. Its wrapper contributes **118 gas/call**. At the upper bound, the entire marginal workload needs about **99.77 gas**, so the residual target fee is zero. A new base would double-charge work already funded in this workload.

The 65,535-round variant measured **499,731 ns/call**, with an upper bound of **500,604 ns**. After the same 118-gas credit, its conservative rounded requirement is **310,000 gas/call**. Five gas per round supplies 327,675 gas. Shorter 6-, 12-, and 24-round cases, including changing-input variants, also fit under `5r`.

This is a workload-budget argument for retaining the zero base, not a claim that zero-round compression takes no time.

## Campaign and qualification results

The full coordinated run was [compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/manifest.json), analyzed in attempt `ba6bcc97-7b65-4be1-828e-8321e28b3c79`.

| Item | Result |
| --- | ---: |
| Selected target variants | 437 |
| Ready / unsupported target variants | 433 / 4 |
| Ready target count-point cases | 2,138 |
| Separate supporting-calibration variants / cases | 19 / 94 |
| Total ready cases | 2,232 |
| Full-run records | 127,228 |
| Executed records, all correctness-passing | 127,224 |
| Unsupported diagnostic records | 4 |
| Qualification records | 89,280 |
| Target / calibration qualification records | 85,520 / 3,760 |
| Raw workload models qualified / inconclusive | 403 / 30 |
| Fully qualified glue-adjusted target estimates | 0 / 433 |
| Exact-affine / non-affine / single-count target grids | 218 / 213 / 2 |
| Priced variants eligible for the exact-budget lane | 206 |
| Additional eligible rejection-path variants, not priced | 3 |
| Pricing groups: increase / retain with complete budget / blocked / rejection | 9 / 1 / 44 / 1 |

The schedule used eight independent qualification workers, each with one warmup and five qualification repetitions per ready case. Eight separate pilot workers ran one repetition per case, and a diagnostic worker collected counts. This is **17 worker processes**, not one long warmed process. Each ready case has 57 records: one diagnostic plus eight times `(one pilot + one warmup + five qualification)`.

Requested and observed sample-ID sets match exactly. Sample IDs are unique. Each case has stable baseline, prepared, and result-commitment identities across repetitions. Baseline and prepared hashes deliberately identify different things and are not expected to equal each other. The worker clones its baseline before each timed execution. The independent expected receipts/logs/storage checks passed for every executed sample.

The [main capture audit](../results/osaka-pricing-600m-20260922T163024Z/reviewed/full-campaign-main-review.json) records these checks. The [full capture command/log](../results/osaka-pricing-600m-20260922T163024Z/reviewed/full-capture.log.command.json) completed successfully in **443.33 seconds**, including analysis. A successful process exit does not mean every estimate qualified.

### Frozen statistical gates

- 1,000 session-cluster bootstrap draws; seed `20260922`.
- 95% confidence intervals.
- At least four independent sessions required; eight were captured.
- Condition number at most `1e8`.
- Residual-curvature R² at most `0.1`.
- Relative interval width at most `0.5`.
- Held-out session and workload-point error at most `0.25`.
- Existing fit-quality/significance checks retained; unqualified prices blocked.
- Only correct qualification-phase rows enter timing models. Diagnostics, pilots, and warmups are not pooled into estimates.

The 30 raw rejections comprise **24 curvature-related failures** including uncomputable diagnostics, **four holdout failures**, and **two constant-count grids**. Details are retained in [qualification.csv](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/reports/qualification.csv). These gates were not relaxed to increase the number of recommendations.

Confidence intervals are pointwise and describe uncertainty in the fitted estimates. Taking the maximum across many such bounds is not a simultaneous 95% guarantee for the entire gas schedule.

### Why the isolated lane remains inconclusive

The supporting lane is explicit and separate from targets. Calibration cases never become target-pricing models. Some individual drivers were usable, but the full adjustment chain was not:

| Driver / block | Point estimate | R² | Assessment |
| --- | ---: | ---: | --- |
| ISZERO | 6.620 ns | 0.9822 | Isolated driver |
| JUMPDEST | 1.441 ns | 0.8249 | Isolated driver |
| SWAP | 2.017 ns | 0.8715 | Isolated driver |
| CALLDATALOAD | 2.685 ns | 0.8993 | Isolated driver |
| POP | 1.272 ns | **0.0500** | Independent count design, but fit quality fails |
| Joint CALL/STATICCALL/PUSH/DUP/etc. block | CALL 155.114 ns; STATICCALL 150.153 ns | 0.9972 | Not isolated because POP is unqualified |

The POP driver varies 32–512 POPs against fixed stack seeding. Its small signal is overwhelmed by noise in this capture. A high R² for the joint block does not remove its unresolved POP contribution. Legacy wrappers also change their instruction composition across count points; some support ratios are unreliable, and embedded STOP bundles do not always match the target's actual call path. Missing support and shape mismatches are not treated as free execution.

Consequently, **all 433 adjusted target estimates were withheld**. The nine recommendations above use the separately qualified budget lane, not these adjusted estimates. The exporter's top-level summary counts the isolated lane; its “0 increase candidates” is not the budget-lane decision count.

See [driver fits](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/reports/glue_results.csv), [detected support](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/reports/glue_opcodes_by_test.csv), and [adjusted-price/coverage records](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/reports/new_gas_all_params.csv).

## Significant discrepancies still blocked

These are not reasons to treat current prices as safe. They are reasons not to turn an incomplete estimate into a universal price.

### Non-affine opcode grids

A review-only calculation bounded the supporting-gas credit by the **minimum and maximum adjacent observed charged-gas slopes**. The lower discrepancy subtracts the largest supporting credit; the upper requirement subtracts the smallest. This exposes large gaps without assuming the gas grid is affine.

| Opcode | Current | Review-only candidate | Lower-bound discrepancy after maximum observed supporting-gas credit | Variants reviewed |
| --- | ---: | ---: | ---: | ---: |
| DIV | 5 | 24 | 4.32× | 2/2 |
| SDIV | 5 | 30 | 5.56× | 2/2 |
| MOD | 5 | 19 | 3.28× | 4/4 |
| SMOD | 5 | 23 | 4.15× | 4/4 |
| MULMOD | 8 | 45 | 5.38× | 4/4 |

These values are **conditional investigation targets**, not promoted recommendations under the frozen exact-affine policy. They use qualified raw fits, but do not solve isolation, transfer to different wrappers, or extrapolation beyond the sampled count regimes. Preserve current prices temporarily while resolving those issues. In particular, do not interpret the absence of an automatic opcode recommendation as evidence against repricing DIV/SDIV/MOD/SMOD/MULMOD.

The [review-only gas-envelope calculations](../results/osaka-pricing-600m-20260922T163024Z/reviewed/non-affine-envelope-review.json) retain the exact input grids, both supporting-gas bounds, and each candidate calculation. A structurally uniform count grid is preferable to loosening the affinity or curvature gates.

### Precompile and KECCAK candidates with missing coverage

| Group | Current formula | Candidate supported by the qualified exact-budget subset | Eligible / selected | Blocker |
| --- | --- | --- | ---: | --- |
| KECCAK256 | `30 + 6w` | `150 + 30w` | 1/39 | 19 short-input cache-risk variants; non-affine grids and raw rejections elsewhere |
| BLS12-381 pairing | `37,700 + 32,600k` | `190,000 + 160,000k` | 6/11 | Five non-affine changing-input grids |
| BN254 pairing | `45,000 + 34,000k` | `300,000 + 230,000k` | 16/21 | Five non-affine grids |
| MODEXP | `max(500, C·I)` | `max(500, ceil(3C·I))` | 110/119 | Five raw rejections, two single-count grids, two over-cap unsupported variants |
| P256VERIFY | `6,900` | `30,000` | 3/4 | Changing-input variant lacks an export generator |
| KZG point evaluation | `50,000` | `570,000` | 1/2 | Changing-input grid is non-affine |

Here `w=ceil(input_bytes/32)`, `k` is the pair count, and MODEXP's `C` and `I` are Osaka's complexity and iteration factors. These are **subset candidates**, not deployable group-wide conclusions. The review-only envelope also flags the non-affine pairing and KZG variants as substantially underpriced, so those gaps deserve priority rather than dismissal.

ECRECOVER is a separate near-gate case. Its raw slope was **29,418.70 ns**, with interval **29,159.56–29,643.10 ns**, and an exact supporting-gas credit of 131. A calculation ignoring its failed gate would suggest about **18,000 gas** instead of 3,000. However, its residual-curvature R² is **0.103842**, above the frozen 0.1 limit. Its small interval width does not override that failure. Retain the price pending a controlled rerun; 18,000 is an investigation value, not an accepted recommendation.

### Do not confuse expensive wrappers with expensive opcodes

Raw workload cost divided by a target's fee can be misleading. The truncated-PUSH32 workload has a raw upper-bound equivalent of about **35×** the PUSH fee, but it also pays about **115–116 gas per PUSH** for its wrapper. That is not evidence to raise all PUSH prices by 35×.

EXP has an additional input-mapping issue. In `test_exp_bench_arithmetic`, the `DUP2; EXP` attack feeds each result into the next exponent. The `exp` parameter is only the seed. For base 11 and seed 13, successive exponent byte sizes begin **1, 6, 32, 32, ...**, giving opcode fees **60, 310, 1,610, 1,610, ...**. Assigning 60 gas to every execution would misclassify the remaining target gas as wrapper gas. The final postprocessor therefore withholds a scalar per-call charge for these **36 evolving-input variants**. The Osaka formula `10 + 50e` remains known and unchanged; the separate invariant all-ones EXP variant has a known 32-byte exponent. See the [EXP review](../results/osaka-pricing-600m-20260922T163024Z/reviewed/exp-seed-pricing-review.json).

### KECCAK cache limitation

The worker enables alloy-primitives' process-global KECCAK cache. In the pinned alloy 1.7.3 implementation on this architecture, **1–87-byte inputs are cache-eligible**. Empty input returns its fixed hash; inputs above 87 bytes bypass this cache. Repeated inputs can remain warm across samples even though EVM state is reset.

All **19 eligible short-input target variants** are conservatively excluded from worst-case pricing evidence. “Uncachable” in a benchmark name is not a measured cache-miss rate. Cache hit/miss statistics were not enabled, and this campaign does not establish adversarial cold-cache cost. Raising a coefficient based on long-input measurements does not resolve that missing short-input coverage.

### Unsupported and non-identifiable cases

The selected corpus retains all four unsupported variants:

- CLZ changing-input variant: no fixed-work export generator.
- P256VERIFY changing-input variant: no fixed-work export generator.
- Two 1,024-byte MODEXP cases: one call needs **58,687,488 gas**, beyond the current transaction's forwardable allowance and the **16,777,216** transaction cap.

Two additional 512-byte MODEXP variants execute only one count point under the cap, so a per-call slope is not identifiable. Repeating the same count more often does not fix that design problem.

The over-cap MODEXP cases are not executable successful calls at the current price and cap. They must remain listed, but their absence is not evidence that expensive computation is currently available cheaply. No gas limit or EVM validation was disabled to force them through.

## Complete pricing inventory

`A` means a recommended change from the complete selected-variant budget evidence. `K` means retain with complete sampled budget coverage. `H` means hold the current formula pending coverage; it is **not** a 600 Mgas/s adequacy claim. `R` is an input-rejection path, not a new valid-call price.

For formulas, `e` is the actual exponent byte length, `w` is the input word count, `r` is the round count, and `D1/D2` are the existing BLS discount factors. The “selected” denominator includes unsupported variants. Rejection-path eligibility is shown for audit completeness but does not authorize a fee proposal.

| Pricing group | Current charge/formula | Recommendation | Raw qualified / ready | Exact-budget eligible / selected |
| --- | --- | --- | ---: | ---: |
| MODEXP_INPUT_REJECTION | `failure path; no valid-call fee` | R: retain failure semantics | 3/3 | 3/3 |
| ADD | `3` | H: retain pending coverage | 2/2 | 1/2 |
| ADDMOD | `8` | H: retain pending coverage | 4/4 | 0/4 |
| AND | `3` | H: retain pending coverage | 1/1 | 0/1 |
| BYTE | `3` | H: retain pending coverage | 1/1 | 0/1 |
| CLZ | `5` | H: retain pending coverage | 1/1 | 0/2 |
| DIV | `5` | H: retain pending coverage | 2/2 | 0/2 |
| DUP | `3` | H: retain pending coverage | 14/16 | 0/16 |
| EQ | `3` | H: retain pending coverage | 1/1 | 0/1 |
| EXP | `10 + 50e` | H: retain pending coverage | 32/37 | 0/37 |
| GAS | `2` | H: retain pending coverage | 1/1 | 0/1 |
| GT | `3` | H: retain pending coverage | 1/1 | 0/1 |
| ISZERO | `3` | H: retain pending coverage | 1/1 | 0/1 |
| JUMP | `8` | H: retain pending coverage | 2/2 | 0/2 |
| JUMPDEST | `1` | H: retain pending coverage | 1/1 | 0/1 |
| JUMPI | `10` | H: retain pending coverage | 2/2 | 0/2 |
| KECCAK256 | `30 + 6w` | H: retain pending coverage | 33/39 | 1/39 |
| LT | `3` | H: retain pending coverage | 1/1 | 0/1 |
| MOD | `5` | H: retain pending coverage | 4/4 | 0/4 |
| MUL | `5` | H: retain pending coverage | 1/1 | 0/1 |
| MULMOD | `8` | H: retain pending coverage | 4/4 | 0/4 |
| NOT | `3` | H: retain pending coverage | 1/1 | 0/1 |
| OR | `3` | H: retain pending coverage | 0/1 | 0/1 |
| PC | `2` | H: retain pending coverage | 0/1 | 0/1 |
| PUSH | `3` | H: retain pending coverage | 37/38 | 0/38 |
| PUSH0 | `2` | H: retain pending coverage | 0/1 | 0/1 |
| SAR | `3` | H: retain pending coverage | 6/6 | 0/6 |
| SDIV | `5` | H: retain pending coverage | 2/2 | 0/2 |
| SGT | `3` | H: retain pending coverage | 1/1 | 0/1 |
| SHL | `3` | H: retain pending coverage | 3/3 | 0/3 |
| SHR | `3` | H: retain pending coverage | 4/4 | 0/4 |
| SIGNEXTEND | `5` | H: retain pending coverage | 1/1 | 0/1 |
| SLT | `3` | H: retain pending coverage | 1/1 | 0/1 |
| SMOD | `5` | H: retain pending coverage | 4/4 | 0/4 |
| SUB | `3` | H: retain pending coverage | 1/1 | 0/1 |
| SWAP | `3` | H: retain pending coverage | 11/16 | 0/16 |
| XOR | `3` | H: retain pending coverage | 1/1 | 0/1 |
| BLAKE2F | `r` | A: 5r | 10/10 | 10/10 |
| BLS12-381 pairing | `37,700 + 32,600k` | H: retain pending coverage | 11/11 | 6/11 |
| BLS G1 ADD | `375` | A: 2,200 | 2/2 | 2/2 |
| BLS MAP_FP_TO_G1 | `5,500` | A: 24,000 | 2/2 | 2/2 |
| BLS G1 MSM | `floor(12,000k D1(k)/1000)` | A: coefficient 70,000 | 5/5 | 5/5 |
| BLS G2 ADD | `600` | A: 3,400 | 2/2 | 2/2 |
| BLS MAP_FP2_TO_G2 | `23,800` | A: 77,000 | 2/2 | 2/2 |
| BLS G2 MSM | `floor(22,500k D2(k)/1000)` | A: coefficient 120,000 | 5/5 | 5/5 |
| BN254 ADD | `150` | A: 1,600 | 6/6 | 6/6 |
| BN254 MUL | `6,000` | A: 18,000 | 9/9 | 9/9 |
| BN254 pairing | `45,000 + 34,000k` | H: retain pending coverage | 21/21 | 16/21 |
| ECRECOVER | `3,000` | H: retain pending coverage | 0/1 | 0/1 |
| IDENTITY | `15 + 3w` | H: retain pending coverage | 8/8 | 6/8 |
| MODEXP | `max(500, C·I)` | H: retain pending coverage | 110/117 | 110/119 |
| P256VERIFY | `6,900` | H: retain pending coverage | 3/3 | 3/4 |
| KZG point evaluation | `50,000` | H: retain pending coverage | 2/2 | 1/2 |
| RIPEMD160 | `600 + 120w` | K: retain; sampled budget adequate | 10/10 | 10/10 |
| SHA256 | `60 + 12w` | H: retain pending coverage | 10/10 | 9/10 |

RIPEMD160 is the only priced group with complete exact-budget coverage and no increase trigger. All ten variants remain funded at the anchor by the current `600 + 120w` formula; the minimum modeled rate at the upper runtime bound is **1173.4 Mgas/s**. Keep that price; do not decrease it.

As an additional conservative screen, ADD, GAS, JUMP, JUMPI, MUL, NOT, SIGNEXTEND, and SUB have qualified upper **raw** workload costs below the target fee itself for every selected variant. This supports retaining those prices in the measured domain without claiming isolated costs. It does not override the formal coverage status in the table or certify other bytecode compositions.

## Sensitivity and minimal-change tradeoff

| Lower-bound discrepancy trigger | Complete-budget groups selected for increases | Difference from the 2× policy |
| --- | ---: | --- |
| 1.5× | 9 | None |
| **2×** | **9** | Chosen policy |
| 3× | 8 | BN254 MUL is omitted; its largest qualified lower discrepancy is 2.94× |

The eight remaining groups still trigger at 3×. Their gaps are not marginal artifacts of choosing 2 rather than 1.5. Retaining BN254 MUL at the 3× threshold would leave its worst selected modeled rate near **205.3 Mgas/s**, despite the 600 Mgas/s anchor.

The recommendation changes **nine physical parameters**, preserves the two MSM discount tables, and introduces no additional pricing branches. Fixed prices and shared formulas necessarily overprice some cheaper sampled inputs. This report does not estimate a production-wide average fee increase because no production workload distribution was supplied or measured.

## Measurement boundary and reproducibility

The timed boundary is `evm2_transaction_execution`: EVM transaction validation on prepared/recovered transactions, execution, settlement, and in-memory state commit. Parsing, transaction/prestate preparation, baseline cloning, EVM construction, expected-outcome checks, hashing, and serialization are outside the timer. Transaction signature recovery, networking, persistent state I/O, consensus, and a full node's other work are not covered.

Diagnostic executions use an opcode/precompile inspector. Timed executions do not. The execution-specs reference backend supplied independent expected outcomes; it did not supply a second opcode counter. Native counter checks and independent outcome checks are distinct evidence.

The host is Linux/Amazon Linux 2023 under KVM, with a Xeon Platinum 8559C, 32 logical CPUs / 16 reported cores and about 247.7 GiB RAM. The worker was pinned to logical CPU **14**, with a **24 GiB** memory limit and swap disabled. The controller was pinned to CPU 0. Generation excluded CPUs 14 and its reported sibling 30. The sibling was not reserved at the host level, and cloud scheduling/frequency effects remain limitations. No heavy build or test validation ran during the full timed capture.

### Frozen identities

| Component | Identity |
| --- | --- |
| Workload SHA-256 | `5051433bf7c8f0bc7c2e5ff197a00138706e9f53e76cf88be417d28d656edae7` |
| Generator image | `sha256:cabf253b75e8ba745f68cbad0c54f4a141dd5db178c140027f679ceef8ad2f22` |
| Worker image | `sha256:5e2dda70a14ad0cf6fdab1a5784019fa58bc321dad004ebd3c242cc2fb15e0a5` |
| Capture analyzer image | `sha256:dd75e2bf0310efbe2cdea1da504e49af67868921d347e9d0f39f9dca66fea919` |
| Final recommendation postprocessor image | `sha256:93b31045e31659d7ccd0bba0d0856c198fd2a8463d40312e5072404ab4213d42` |
| Controller SHA-256 | `c5c29e2ab617353974298026086ba4e5b403a33616ce79c637783cca5dd1f191` |

Base repository revisions:

| Repository | Base revision |
| --- | --- |
| benchmarkoor | `fda9738ed7e8524945ab5b8c2f407388b11ce03f` |
| execution-specs | `6ee9c7854ca22cb8f5259bac2fa20892d6dfab06` |
| evm2 | `c0dfb22c27044e82e83f42f299525f740b16c1a3` |
| evm-gasfit | `c40409e3c83e082d8629d0545e268d109833ef27` |

The generator, controller repository, and analyzer include local changes; these base revisions alone do not reconstruct them. Frozen executable images, source fingerprints, commands, and artifacts are authoritative. The evm2 source was unchanged. The [final provenance record](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/provenance.json) distinguishes the capture analyzer from later recommendation-only corrections. Timings, diagnostics, expected outcomes, and statistical gates were not rewritten.

The worker does not expose an active gas-schedule fingerprint. The archive records Osaka, the transaction cap, source/lock identities, and the worker image, but native campaign comparison must remain blocked when it requires that missing fingerprint. It was not fabricated for this report.

### Artifact ledger

All links below refer to retained local workspace artifacts. Generated results remain uncommitted; this is not a claim that they have been uploaded to a public archive.

- [Frozen corpus](../results/osaka-pricing-600m-20260922T163024Z/reviewed/corpus/workload.json), [capture config](../results/osaka-pricing-600m-20260922T163024Z/reviewed/config/compute.yaml), and [analysis config](../results/osaka-pricing-600m-20260922T163024Z/reviewed/analysis-gasfit.yaml).
- [Full manifest](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/manifest.json), [requested schedule](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/requested-samples.jsonl), and [all sample records](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/samples.jsonl).
- [Diagnostic records](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/sessions/diagnostic-00/samples.jsonl), [raw fits](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/reports/results.csv), [qualification](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/reports/qualification.csv), and [analysis provenance](../results/osaka-pricing-600m-20260922T163024Z/reviewed/runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2/analysis/ba6bcc97-7b65-4be1-828e-8321e28b3c79/reports/analysis_status.json).
- [Executed capture command](../results/osaka-pricing-600m-20260922T163024Z/reviewed/full-capture.log.command.json) and [final recommendation command](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed.command.json). The controller entry point is `benchmarkoor run --config ...`, not `benchmarkoor compute run`.
- [Reviewed optimized-input evidence](../results/osaka-pricing-600m-20260922T163024Z/reviewed/reviewed-optimized-inputs.json) and [hash/case-bound input sidecar](../results/osaka-pricing-600m-20260922T163024Z/reviewed/reviewed-resolved-inputs.json). The verified optimized sizes are 65,281 bytes for KECCAK, 177,569 for IDENTITY, 78,081 for RIPEMD160, and 112,961 for SHA256. Three other sizes were recovered from actual exported transaction data. These annotations do not alter execution payloads.
- [Exact charged-gas grid review](../results/osaka-pricing-600m-20260922T163024Z/reviewed/charged-gas-grid-review.json), [review-only non-affine screens](../results/osaka-pricing-600m-20260922T163024Z/reviewed/non-affine-envelope-review.json), and [final actionable schedule](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/reviewed-schedule.json).

Only the full coordinated capture supplies the final price evidence. Earlier captures and the small smoke campaign were not pooled into these estimates.

## Implementation review and verification

Implementation work was performed by task agents; Main reviewed the code, exercised the real pipeline, investigated the measurements, and selected the final coefficients.

Review corrected recommendation defects that could otherwise mislead this report:

- Read the analysis's embedded, hash-verified JSON/YAML config rather than trying to reopen an archived container-local path.
- Scale MODEXP's unfloored complexity product when preserving its 500-gas floor. Scaling a current fee of 500 by 2.4 does not mean that multiplying an underlying product of 16 by 2.4 produces 1,200 gas.
- Keep evidence triggers visible while blocking a formula that cannot cover a positive zero-round cost. Input rejection is not advertised as a new fee candidate.
- Do not infer a constant EXP fee from an evolving exponent seed.
- Preserve qualified-raw and qualified-adjusted states separately, with explicit missing-support and bundle-coverage records.

Verification evidence:

| Check | Result |
| --- | --- |
| Generator tests | 20 passed |
| Real calibration export/native diagnostic execution | 94 ready cases passed independent expected-outcome checks |
| Real glue-enabled smoke | 2,703 executed records passed correctness; all 13 unqualified adjusted prices withheld |
| Malformed campaign-role smoke | Rejected with exit 1; no config emitted |
| Full analyzer suite after formula/provenance corrections | 234 passed, no failures or skips |
| Final focused pricing suite after EXP correction | 33 passed |
| Final undefined-name checks (`F821/F822/F823`) and pricing-code format check | Passed |
| Reference pricing calculations | 80 MODEXP boundary vectors and all 256 BLS MSM discount entries matched pinned execution-specs |
| Final immutable-image recommendation CLI | Completed over all 437 selected target variants |
| Final physical coefficient arithmetic | All 43 accepted variants cover their required fee and exceed 600 Mgas/s at the fitted upper bound |

The four new regression cases failed against archived pre-correction source, and the corrected suite passed. The updated EXP regression also failed against the old seed-based mapping and passed after correction. See [regression-before evidence](../results/osaka-pricing-600m-20260922T163024Z/reviewed/formula-review/before.log), [full-suite XML](../results/osaka-pricing-600m-20260922T163024Z/reviewed/formula-review/full-suite.xml), [final pricing XML](../results/osaka-pricing-600m-20260922T163024Z/reviewed/formula-review/pricing-final.xml), [EXP-before evidence](../results/osaka-pricing-600m-20260922T163024Z/reviewed/formula-review/exp-before.log), and [smoke audit](../results/osaka-pricing-600m-20260922T163024Z/reviewed/smoke-main-review.json).

The [reference gas vectors](../results/osaka-pricing-600m-20260922T163024Z/reviewed/osaka-reference-gas.json) retain the formula cross-check inputs. The [final source checks](../results/osaka-pricing-600m-20260922T163024Z/reviewed/formula-review/symbols-final.log) and [report validation](../results/osaka-pricing-600m-20260922T163024Z/reviewed/recommendations-reviewed/report-validation.json) are retained with the campaign.

## Conditions before a 600 Mgas/s rollout

The nine changes are a supported **candidate schedule**, not sufficient rollout evidence by themselves. The remaining work is specific:

1. Resolve the large held discrepancies first: the five division/modular opcodes, pairings, KZG, P256, KECCAK, and ECRECOVER. Use structurally consistent grids or an explicitly reviewed alternative accounting model; do not simply relax failed gates.
2. Amplify the POP calibration signal while keeping non-POP counts controlled, then re-establish the supporting-cost dependency chain and exact call/STOP bundle coverage.
3. Measure adversarial cold short-input KECCAK behavior and expand the missing CLZ/P256 export coverage. Record cache behavior rather than inferring it from test names.
4. Treat evolving EXP workloads as variable-byte-cost sequences, and use a different measurement design for single-count MODEXP cases without disabling transaction limits.
5. Validate alternative bytecode compositions, operand distributions, relevant production hardware, and any other execution clients before treating these values as a chain-wide worst-case schedule.
6. Regenerate cap-constrained count grids under any proposed schedule. Some currently valid benchmark transactions would exceed their gas allowance after an increase. Gas forwarding and contract compatibility also need review. This campaign did not execute a worker with the proposed prices installed.

Finite sampled inputs, one client/hardware configuration, pointwise confidence intervals, global caches, and permitted sub-2× discrepancies prevent a universal 600 Mgas/s claim. The practical decision is to advance the nine supported changes, retain the other prices provisionally, and keep the listed large gaps as blockers to declaring the overall target achieved.

## Primary references

- [EIP-152: BLAKE2F](https://eips.ethereum.org/EIPS/eip-152), including the existing one-gas-per-round formula.
- [EIP-2537: BLS12-381 precompiles](https://eips.ethereum.org/EIPS/eip-2537), including fixed fees, MSM discounts, and pairing coefficients.
- [EIP-7883: Osaka MODEXP pricing](https://eips.ethereum.org/EIPS/eip-7883). The pinned execution-specs implementation is the executable reference for edge cases.
- [EIP-7951: P256VERIFY](https://eips.ethereum.org/EIPS/eip-7951), address `0x100` and current 6,900-gas price.
- [Pinned Osaka gas constants](https://github.com/weiihann/execution-specs/blob/6ee9c7854ca22cb8f5259bac2fa20892d6dfab06/src/ethereum/forks/osaka/vm/gas.py).
- [Alloy 1.7.3 KECCAK cache implementation](https://github.com/alloy-rs/core/blob/v1.7.3/crates/primitives/src/utils/keccak_cache.rs).
