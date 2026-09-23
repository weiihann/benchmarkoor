# Osaka compute campaign: handoff

Date: 2026-09-22.

> Historical handoff: the current pricing evaluation is the
> [600 Mgas/s recommendation report](compute-gas-pricing-600m.md).
> This snapshot retains the earlier 500 Mgas/s assumption and implementation
> status; do not treat those sections as the current campaign result.

## 1. Goal and user decisions

Continue developing and running a reproducible compute gas-calibration pipeline:

```text
execution-specs workloads and independent expected outcomes
    → benchmarkoor orchestration
    → standalone evm2 execution and operation counting
    → evm-gasfit modeling, glue adjustment, and proposed compute prices
```

This document is the entire handoff. Copy this Markdown file to the new machine
and clone the repositories below. There is no accompanying context archive,
metadata file, helper-script package, model-config package, or results transfer.
All workload files, configs, manifests, measurements, and reports used for the
new experiment must be generated there.

The user's decisions are:

- The previous machine was only a pipeline trial. The actual campaign will run
  on a different, initially fresh machine.
- Do not transfer or analyze old measurements, diagnostics, reports, host
  manifests, or pre-generated workloads on the new machine.
- Do not commit generated results. Repository source and documentation are
  separate from experiment output.
- Use **500,000,000 gas/second** as the working pricing calibration. This is
  **0.5 gas/ns**, or 2 ns/gas. It is a conversion policy, not a demonstrated
  full-node throughput or a block gas limit.
- Direct evm2 execution is sufficient for the compute campaign. NewL1
  integration is not a prerequisite. Do not restore the abandoned
  Prague/NewL1 integration approach.
- Preserve production EVM semantics, gas charging, and transaction validation.
  Do not disable gas limits to make benchmark cases run.
- Benchmarkoor owns orchestration and artifacts; gasfit remains the modeling
  dependency. Do not copy its numerical analysis into Go.

### Scope

Include all existing Osaka-compatible variants in arithmetic, bitwise,
comparison, stack, control flow, KECCAK256, and precompiles, including Osaka's
P256VERIFY at address `0x100`.

Exclude dedicated memory, storage, account-access, contract-lifecycle,
block-access-list, scenario, stateful, and post-Osaka target benchmarks.
Supporting instructions and in-memory state work remain part of actual
execution and must be accounted for when estimating the target's cost.

## 2. Current implementation state

The following components exist on the pinned feature branches:

| Repository | Responsibility and implemented path |
| --- | --- |
| execution-specs | Fixed-work Osaka exports through `fill`, with prestate, recovered transaction intent, expected outcomes, and storage witnesses |
| evm2 | Finite `evm2-bench` worker; instrumented diagnostics; uninstrumented timed execution; correctness checks and per-sample artifacts |
| benchmarkoor | Generator/worker/analyzer containers; frozen sample schedules; fresh sessions; raw records, provenance, gasfit input export, and immutable reanalysis attempts |
| evm-gasfit | Regression, session-aware bootstrap, qualification gates, glue machinery, pricing scenarios, and reports |

The end-to-end generation, execution, recording, and **unadjusted workload**
analysis paths have been exercised. **Glue-adjusted gas pricing is unfinished.**
The earlier full trial deliberately used per-variant workload models with glue
disabled. Those observations are not isolated opcode cost estimates.

The committed example configuration and smoke script cover ADD/KECCAK controls,
not an already-finished full-corpus pricing proposal. The temporary full-corpus
configs and generation shards from the earlier machine are not dependencies of
this handoff. Reconstruct generation selections and model recipes from the new
machine's freshly exported corpus, using sections 6–9.

Before a final pricing capture, resolve the calibration-driver and model-mapping
gaps in section 9. A pipeline smoke passing with glue disabled is not evidence
that the complete repricing analysis works.

## 3. Repositories and immutable source pins

All four feature branches are named `feat/osaka-evm2-compute-pipeline`.
The following branch heads were checked against GitHub when preparing the
handoff. Use these user forks, not the upstream default branches.

| Repository | Clone URL | Commit |
| --- | --- | --- |
| benchmarkoor | https://github.com/weiihann/benchmarkoor.git | `ef12d9f7848df806321087a865291522ae58477b` |
| execution-specs | https://github.com/weiihann/execution-specs.git | `6ee9c7854ca22cb8f5259bac2fa20892d6dfab06` |
| evm2 | https://github.com/weiihann/evm2.git | `c0dfb22c27044e82e83f42f299525f740b16c1a3` |
| evm-gasfit | https://github.com/weiihann/evm-gasfit.git | `c40409e3c83e082d8629d0545e268d109833ef27` |

Keep the four checkouts as siblings. The existing smoke script depends on that
layout. After installing Git, this creates and verifies the pinned checkouts:

```bash
export ROOT="$HOME/osaka-compute"
mkdir -p "$ROOT"
python3 - "$ROOT" <<'PY'
import subprocess
import sys
from pathlib import Path

pins = {
    "benchmarkoor": "ef12d9f7848df806321087a865291522ae58477b",
    "execution-specs": "6ee9c7854ca22cb8f5259bac2fa20892d6dfab06",
    "evm2": "c0dfb22c27044e82e83f42f299525f740b16c1a3",
    "evm-gasfit": "c40409e3c83e082d8629d0545e268d109833ef27",
}
root = Path(sys.argv[1]).resolve()
for name, commit in pins.items():
    dest = root / name
    if dest.exists():
        raise SystemExit(f"Refusing to overwrite an existing checkout: {dest}")
    subprocess.run([
        "git", "clone", "--single-branch", "--branch",
        "feat/osaka-evm2-compute-pipeline",
        f"https://github.com/weiihann/{name}.git", str(dest)
    ], check=True)
    subprocess.run(["git", "-C", str(dest), "checkout", "--detach", commit], check=True)
    actual = subprocess.check_output(
        ["git", "-C", str(dest), "rev-parse", "HEAD"], text=True
    ).strip()
    assert actual == commit, (name, actual)
print("All four checkouts match the pinned commits.")
PY
```

Detached checkouts make the starting version explicit. Create working branches
before implementation changes, preserve local work, and record new commits and
patches before measuring. No commits or pushes were made for this handoff;
this document itself must be copied separately from the pinned checkouts.

## 4. Fresh-machine setup and installation smoke

### Host assumptions

The commands below assume native Ubuntu 24.04 or 26.04 x86_64 Linux, sudo access,
and outbound HTTPS. Other distributions need equivalent package installation.
Avoid QEMU or Docker Desktop virtualization for reference measurements. No GPU,
NewL1 node, reth checkout, Node.js, or UI build is required.

A host with at least 32 GiB RAM is recommended when using the example 24 GiB
container limit. Budget at least 50 GiB of free SSD space for sources, images,
compiler caches, fixtures, and reports. These are operational recommendations,
not measured minimum requirements. If changing resource limits, record them and
verify no worker or analyzer is OOM-killed.

The host needs Docker, Git, Python 3, Make, and basic utilities. Go, Rust, uv,
scipy, and the other language dependencies can stay inside containers.

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git make python3 jq \
  tar gzip coreutils util-linux openssh-client
```

### Docker

Follow the [official Docker Ubuntu installation instructions](https://docs.docker.com/engine/install/ubuntu/).
For a fresh host with no conflicting Docker/containerd installation:

```bash
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"
```

Docker-group membership grants root-equivalent privileges. Log out and back in,
then restore `ROOT` in the new shell. Do not run the whole campaign with sudo to
work around Docker socket permissions.

```bash
export ROOT="$HOME/osaka-compute"
docker version
docker buildx version
docker run --rm hello-world
```

Named build contexts require BuildKit/buildx. Do not use the legacy builder.
The campaign does not require published container ports.

### Controller build

Clone section 3's repositories, then build a static controller in a Go container.
Run the resulting binary on the host so Docker bind-mount paths refer to the
actual host filesystem:

```bash
mkdir -p "$ROOT/benchmarkoor/bin"
docker run --rm \
  -e CGO_ENABLED=0 -e GOTOOLCHAIN=local \
  -e GOPATH=/tmp/gopath -e GOCACHE=/tmp/go-cache \
  -v "$ROOT/benchmarkoor:/src:ro" \
  -v "$ROOT/benchmarkoor/bin:/out" -w /src \
  golang:1.24.11-bookworm@sha256:656be510c8b4d33acf4eac8575b7f04a3e30b705c152dc6bde112dbe1a87b602 \
  go build -mod=readonly -buildvcs=false \
    -tags exclude_graphdriver_btrfs,exclude_graphdriver_devicemapper,containers_image_openpgp \
    -o /out/benchmarkoor ./cmd/benchmarkoor
export BENCHMARKOOR_BIN="$ROOT/benchmarkoor/bin/benchmarkoor"
"$BENCHMARKOOR_BIN" run --help
"$BENCHMARKOOR_BIN" analyze --help
```

The three build tags are needed by the controller's container dependencies.
`-buildvcs=false` avoids Git ownership/worktree-path problems inside the
read-only mount. The campaign manifest separately records source commits,
dirty state, and lockfile hashes. The repository Dockerfile uses the same
CGO-disabled build approach.

### Real installation smoke

The pinned evm2 checkout ignores `Cargo.lock`; a fresh clone does not contain it.
Generate it once with the worker's Rust toolchain before the smoke build:

```bash
test -f "$ROOT/evm2/Cargo.lock" || docker run --rm \
  -v "$ROOT/evm2:/src" -w /src rust:1.96.0-bookworm \
  cargo generate-lockfile
```

Keep this lockfile for subsequent builds. Archive it with the new campaign;
the manifest records its hash even though Git ignores it. Initial dependency
resolution can differ from the previous machine. The worker build must retain
`--locked` so it cannot silently change that resolution.

```bash
export COMPUTE_SMOKE_RESULTS_DIR="$ROOT/benchmarkoor/results/setup-smoke-$(date -u +%Y%m%dT%H%M%SZ)"
cd "$ROOT/benchmarkoor"
bash scripts/compute/smoke.sh
```

Expected final message: `compute smoke passed: ...`. This builds all three
images, generates 20 ADD/KECCAK cases, and executes the worker and real analyzer.
The script's relaxed qualification thresholds are for installation checking
only. Do not copy them into the full campaign. Its glue adjustment is disabled.

The script produces these local tags:

```text
benchmarkoor-compute-worker:smoke
benchmarkoor-compute-generator:smoke
benchmarkoor-compute-analyzer:smoke
```

To rebuild components separately after changes:

```bash
cd "$ROOT/benchmarkoor"
docker build -f Dockerfile.compute-worker \
  -t benchmarkoor-compute-worker:smoke "$ROOT/evm2"
docker build --build-context execution-specs="$ROOT/execution-specs" \
  -f Dockerfile.compute-generator -t benchmarkoor-compute-generator:smoke .
docker build --build-context evm-gasfit="$ROOT/evm-gasfit" \
  -f Dockerfile.compute-analyzer -t benchmarkoor-compute-analyzer:smoke .
```

The worker image uses Rust 1.96.0. Generator/analyzer images use Python 3.12,
uv 0.10.4, and their committed lockfiles. Several base-image tags and apt
repositories are mutable, so rebuilding does not guarantee an identical image
ID. Resolve local image IDs and retain them with each new campaign. Old local
`sha256:...` image IDs are not registry references and are not needed here.

## 5. Measurement environment and protocol

Select the new machine's CPU deliberately rather than copying a previous CPU
number. Docker affinity does not reserve an SMT sibling or prevent other host
work from using it. Keep builds, generation, other benchmarks, and background
maintenance away from the timed phase. Record governor/turbo, SMT, NUMA,
virtualization, firmware/kernel, and resource policies; do not silently alter
system-wide settings.

```bash
lscpu
lscpu -e=CPU,CORE,SOCKET,NODE,ONLINE
free -h
df -h "$ROOT" /var/lib/docker
docker info
```

Choose one available logical CPU; `2` below is an example to replace after
inspecting the topology:

```bash
export CPU=2
export MEMORY=24g
export RUN_HOME="$ROOT/benchmarkoor/results/osaka-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$RUN_HOME"
{
  date -u
  uname -a
  lscpu
  lscpu -e=CPU,CORE,SOCKET,NODE,ONLINE
  free -h
  docker version
  docker info
  cat "/sys/devices/system/cpu/cpu${CPU}/topology/thread_siblings_list"
  if [ -r "/sys/devices/system/cpu/cpu${CPU}/cpufreq/scaling_governor" ]; then
    cat "/sys/devices/system/cpu/cpu${CPU}/cpufreq/scaling_governor"
  fi
} > "$RUN_HOME/host-before.txt"
docker image inspect benchmarkoor-compute-worker:smoke \
  benchmarkoor-compute-generator:smoke benchmarkoor-compute-analyzer:smoke \
  > "$RUN_HOME/images.json"
export GENERATOR_IMAGE="$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-generator:smoke)"
export WORKER_IMAGE="$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-worker:smoke)"
export ANALYZER_IMAGE="$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-analyzer:smoke)"
```

### Execution contract

- Protocol v2 is defined in `benchmarkoor/pkg/compute/protocol.go` and
  `pkg/compute/schema/`. Old Prague protocol v1 is not the contract.
- Worker command: `evm2-bench --request <request.json> --output <samples.jsonl>`.
- Boundary: `evm2_transaction_execution`, including transaction validation,
  execution, settlement, and state commit.
- Parsing, prestate preparation, baseline cloning, EVM construction, oracle
  checks, hashing, and serialization are outside the timer.
- Diagnostics run with an inspector and record actual executed target counts.
  Timed runs do not use the inspector.
- Precompile counts are address-specific `PRECOMPILE_0x<40 hex digits>` keys,
  not aggregate STATICCALL counts. KECCAK256 is the canonical opcode name.
- Each sample restores its declared baseline. Each qualification session is a
  fresh worker process. Separate diagnostic/pilot processes do not warm the
  qualification process; its own warmup samples do.
- Every requested sample gets exactly one terminal result. Executed rows need
  passing correctness checks, valid hashes, and gas evidence. Performance rows
  additionally need a positive duration. Failed/unsupported rows carry reasons.

## 6. Generate and preflight a fresh full corpus

The source allowlist covers 437 distinct variants at the pinned revisions.
Expected limitations are four unsupported variants, described below. Source
changes may change this inventory, so investigate differences rather than
silently trimming the selection.

### Initial inventory

Generate at one requested target operation to discover variants and their
reference outcomes. The count option is in **thousands**, so `0.001` means one
operation. Both fixture formats can be collected; the exporter canonicalizes
and deduplicates equivalent case identities.

```bash
mkdir "$RUN_HOME/inventory"
docker run --rm -v "$RUN_HOME/inventory:/out" "$GENERATOR_IMAGE" \
  --fork Osaka \
  --benchmark-workload-export=/out/workload.json \
  --benchmark-workload-seed=20260922 \
  --benchmark-workload-revision="$GENERATOR_IMAGE" \
  --benchmark-workload-tx-gas-cap=16777216 \
  --benchmark-workload-families=arithmetic,bitwise,comparison,stack,control_flow,keccak,precompile \
  --fixed-opcode-count=0.001 --output=/out/fixtures --no-html --skip-index -q \
  tests/benchmark/compute/instruction/test_arithmetic.py \
  tests/benchmark/compute/instruction/test_bitwise.py \
  tests/benchmark/compute/instruction/test_comparison.py \
  tests/benchmark/compute/instruction/test_stack.py \
  tests/benchmark/compute/instruction/test_control_flow.py \
  tests/benchmark/compute/instruction/test_keccak.py \
  tests/benchmark/compute/precompile
```

Use the existing worker to obtain fresh diagnostic gas/count evidence:

```bash
python3 - "$RUN_HOME/inventory" <<'PY'
import json
import sys
from pathlib import Path
out = Path(sys.argv[1])
workload = json.loads((out / "workload.json").read_text())
request = {
    "schema_version": 2,
    "session_id": "inventory-diagnostic",
    "mode": "diagnostic",
    "workload_path": "/campaign/workload.json",
    "samples": [
        {"sample_id": f"inventory-{i}", "case_id": case["id"],
         "repetition": 0, "phase": "diagnostic"}
        for i, case in enumerate(workload["cases"])
    ],
}
with (out / "request.json").open("x") as f:
    json.dump(request, f, indent=2)
PY
docker run --rm -v "$RUN_HOME/inventory:/campaign" "$WORKER_IMAGE" \
  --request /campaign/request.json --output /campaign/diagnostic.jsonl
```

### Construct the count grids

Do not use the one-count inventory as the final regression corpus. Generate
separate shards with distinct count points, using the same exporter and gas cap.
The following policy and pitfalls are the implementation context for preparing
those jobs; the committed smoke example is not a full-corpus job generator.

| Target class | Count-grid policy |
| --- | --- |
| Ordinary instruction variants | 250, 500, 1,000, 2,000, 4,000 requested operations |
| `test_keccak_max_permutations` | 125, 250, 500, 750, 1,000 instead |
| Precompiles | Choose feasible powers-of-two count points per input variant; normally up to five points and at most 1,024 calls |
| Expensive precompiles | Shorter grids such as 1/2, 1/2/4, or 1/2/4/8 where necessary |
| Expensive 512-byte MODEXP variants | One call fits but a slope cannot be identified from a single count point |

For ordinary instructions, the exporter flag is
`--fixed-opcode-count=0.25,0.5,1,2,4`. For the special KECCAK grid it is
`--fixed-opcode-count=0.125,0.25,0.5,0.75,1`.
Use the six instruction module paths above, exclude
`test_keccak_max_permutations` from the ordinary shard with `-k`, and generate
that variant separately. Arbitrary powers of two are unsuitable for some
instruction generators because of their outer-loop granularity.

For precompiles, derive each variant's feasible count range from the new
machine's inventory diagnostics, not an old measurement file. A conservative
initial screen is to keep the estimated per-transaction variable gas below
10 million, using single-call charged gas as an upper estimate per call. This
is a screening heuristic, not proof of feasibility. Retain a single-call case
when one call fits the transaction cap but no useful larger grid fits. Preflight
all generated count points with the real worker before freezing the corpus.

Use exact variant selection when grouping precompile jobs. Gasfit selectors
and pytest selectors have different syntax: pytest accepts `-k` or the fill
command's `--regex`; gasfit's `filter_by` uses literal substrings. Exported case
IDs omit the fixture-format component found in pytest node IDs. A full pytest
regex therefore needs to account for `blockchain_test` and
`blockchain_test_engine`, or it may collect no tests.

Preserve all generated shards and command logs. To assemble the final workload:

1. Use the exporter JSON envelope with schema v2, Osaka, and the actual generator
   image revision/seed. Do not reimplement expected EVM outcomes.
2. Include the ready count-point cases from the selected shards, replacing the
   ordinary maximum-permutation KECCAK points with its special grid.
3. Retain the inventory's unsupported variants explicitly. Do not erase an
   unexpected generation or preflight failure; investigate it before capture.
4. Reject duplicate case IDs, mixed fork/version/provenance, and inconsistent
   transaction allowances. Sort cases for reproducibility.
5. Write a new `workload.json` and a `build.json` alongside it, including the
   workload SHA-256, generator identity, contributing shard hashes, and commands.
6. Run a complete diagnostic pass on the final union. Every ready case must
   satisfy its oracle and have the expected positive semantic target count.

Reference generation, especially high-round BLAKE2F, can be slow. Generation
runtime is not benchmark timing. Use fresh output directories, keep failed
attempts, and do not run generation concurrently with measurements.

### Known exclusions and gas constraints

Expected unsupported variants at the source pins:

- `test_clz_diff`: no fixed-count generator.
- `test_p256verify_uncachable`: no fixed-count generator.
- MODEXP `mod_even_1024b_exp_1024` and `mod_odd_1024b_exp_1024`: one successful
  call exceeds the transaction gas cap. This is not missing evm2 MODEXP support.

Osaka bounds each MODEXP operand to 1,024 bytes, but size-valid operands can
still exceed the transaction's gas allowance. For the two excluded inputs,
base/modulus are 1,024 bytes and the all-ones exponent is 128 bytes:

```text
complexity = 2 × (1024 / 8)^2 = 32,768
iterations = 16 × (128 - 32) + 255 = 1,791
gas        = 32,768 × 1,791 = 58,687,488
```

That is above the 16,777,216 transaction cap before surrounding work. Do not
raise the cap or change the gas table. Relevant specifications:
[EIP-7823](https://eips.ethereum.org/EIPS/eip-7823),
[EIP-7825](https://eips.ethereum.org/EIPS/eip-7825), and
[EIP-7883](https://eips.ethereum.org/EIPS/eip-7883).

A negative test that correctly rejects oversized input is distinct from
benchmarking a successful modular exponentiation. Preserve that distinction.

## 7. Analysis configuration and campaign scheduling

### Recreate models from the new corpus

Do not assume the default preset catalog covers every selected variant. Known
issues include BLS map-to-G1/G2 address mismatches and a BN128 addition selector
matching a pairing variant. Osaka map addresses are 0x10/0x11. Use the exported
`parameters.target_count_key` for addressed precompile counts.

For a workload-only measurement trial, create one model per fixed input variant
by removing the terminal `-opcount_...` parameter from fresh case IDs. Keep the
target operation and all other input-shape parameters fixed. Each model uses an
intercept and one target-count slope. Use unique `WORKLOAD_*` parameter names,
with `new_params` values of null, rather than pretending the full-workload slope
is an isolated opcode price.

The following creates that baseline analysis config directly from a newly
assembled workload. Set `WORKLOAD` to the final union produced in section 6:

```bash
export WORKLOAD="$RUN_HOME/corpus/workload.json"
python3 - "$WORKLOAD" "$RUN_HOME/analysis-workload.yaml" <<'PY'
import hashlib
import json
import re
import sys
from pathlib import Path

workload = json.loads(Path(sys.argv[1]).read_text())
variants = {}
for case in workload["cases"]:
    if case["status"] == "ready":
        variant = re.sub(r"-opcount_[^\]]+", "", case["id"])
        variants.setdefault(variant, case)
models = []
new_params = {}
for variant, case in sorted(variants.items()):
    suffix = hashlib.sha256(variant.encode()).hexdigest()[:12]
    param = f"WORKLOAD_{case['target_operation']}_{suffix}"
    assert param not in new_params
    new_params[param] = None
    model = {
        "test_name": variant.split("::")[-1].split("[")[0],
        "target_operation": case["target_operation"],
        "filter_by": [variant[:-1] + "-"],
        "model_params": {"target_coef": param},
    }
    count_key = case["parameters"].get("target_count_key", "")
    if count_key.startswith("PRECOMPILE_"):
        model["target_operation_count_source"] = count_key
    models.append(model)
config = {
    "version": 1, "clients": ["evm2"], "gas_costs": {"fork": "osaka"},
    "output": {"plots": False},
    "modeling": {"bootstrap_iterations": 1000, "random_seed": 20260922},
    "glue_adjustment": {"enabled": False},
    "qualification": {
        "confidence_level": 0.95, "max_condition_number": 1e8,
        "max_residual_curvature_r2": 0.1, "max_relative_uncertainty": 0.5,
        "max_holdout_error": 0.25, "min_sessions": 4,
        "enforce_fit_quality": True, "block_unqualified": True,
    },
    "campaign": {
        "eligible_phases": ["qualification"],
        "eligible_statuses": ["executed"], "require_correctness_passed": True,
    },
    "models": {"custom": models}, "new_params": new_params,
}
with Path(sys.argv[2]).open("x") as f:
    json.dump(config, f, indent=2)
    f.write("\n")
print(f"Created {len(models)} workload-only models; glue remains disabled.")
PY
```

This is a diagnostic baseline, not the final pricing model. For real prices,
complete section 9 and replace these per-variant recipes with validated gas
parameter mappings and input-dependent formulas.

### Full measurement policy

| Setting | Value |
| --- | --- |
| Fork | Osaka |
| Transaction allowance | 16,777,216 gas, fixed across count points |
| Sessions | 4 fresh qualification processes |
| Pilot repetitions | 1 per executable count point per session |
| Warmup repetitions | 1 per executable count point per session |
| Qualification repetitions | 5 per executable count point per session |
| Seed | 20260922 |
| Bootstrap | 1,000 session-cluster resamples; 95% confidence intervals |
| Condition number | <= 1e8 |
| Residual curvature R2 | <= 0.1 |
| Relative confidence-interval width | <= 0.5 |
| Holdout error | <= 0.25 |
| Minimum sessions | 4 |
| Fit-quality gates / block unqualified prices | Enabled |

A count point gets **4 × 5 = 20 qualification measurements**, not four
measurements total. A normal five-count variant gets 100. Individual rows are
fit to `runtime = intercept + slope × actual target count`; this is not merely
averaging four runs. Whole sessions are resampled for uncertainty, preserving
within-session dependence.

Create a new full run config after generation and preflight:

```bash
python3 - "$ROOT" "$RUN_HOME" "$WORKLOAD" "$CPU" "$MEMORY" <<'PY'
import json
import os
import subprocess
import sys
from pathlib import Path
root, out, workload = map(lambda p: Path(p).resolve(), sys.argv[1:4])
cpu = int(sys.argv[4])
assert cpu in os.sched_getaffinity(0), f"CPU {cpu} is unavailable"
assert workload.is_file()
analysis = out / "analysis-workload.yaml"
assert analysis.is_file()
def image(tag):
    return subprocess.check_output([
        "docker", "image", "inspect", "--format", "{{.Id}}", tag
    ], text=True).strip()
config = {"compute": {
    "id": "osaka-" + out.name,
    "workload": str(workload), "results_dir": str(out),
    "container_runtime": "docker",
    "worker_image": image("benchmarkoor-compute-worker:smoke"),
    "analyzer": {"image": image("benchmarkoor-compute-analyzer:smoke"),
                 "config": str(analysis)},
    "seed": 20260922, "sessions": 4, "pilot_repetitions": 1,
    "warmup_repetitions": 1, "repetitions": 5, "timeout": "3h",
    "resource_limits": {"cpuset": [cpu], "memory": sys.argv[5], "swap_disabled": True},
    "source_paths": {
        "benchmarkoor": str(root / "benchmarkoor"), "evm2": str(root / "evm2"),
        "execution_specs": str(root / "execution-specs"),
        "evm_gasfit": str(root / "evm-gasfit"),
    },
}}
with (out / "compute.yaml").open("x") as f:
    json.dump(config, f, indent=2)
    f.write("\n")
PY
cd "$ROOT/benchmarkoor"
set -o pipefail
"$BENCHMARKOOR_BIN" run --config "$RUN_HOME/compute.yaml" \
  2>&1 | tee "$RUN_HOME/run.log"
```

Use a persistent terminal session for remote execution. This is a finite
campaign, not a long-running node. A nonzero exit needs investigation even if
some artifacts exist. Keep failed attempts and start a new run instead of
silently reusing sample IDs. There is no implicit resume contract.

## 8. Verification, retention, and reanalysis

The controller logs a run directory under `$RUN_HOME/runs/compute-<uuid>/`.
For the schedule above, if the new corpus contains R ready cases and U
unsupported cases, expect:

```text
qualification records = 20 × R
total terminal records = 29 × R + U
```

The 29 records per ready case are one diagnostic plus four sessions each with
one pilot, one warmup, and five qualification measurements. Derive R/U from the
new workload; do not import an old expected result ledger.

Verify the following before trusting analysis:

- Exactly one terminal result for each frozen request, with matching sample,
  session, case, repetition, phase, and schema identifiers.
- Every ready case passes the exported oracle. Every unsupported case has an
  explicit reason. No failed sample is hidden by a retry.
- Diagnostic target counts match the target opcode or addressed-precompile
  counter, not the generator's requested count or aggregate STATICCALL count.
- Every timed row has positive duration and no diagnostic counters.
- Baseline, prepared-state, and commitment hashes are valid and stable per case.
- Declared/charged gas evidence is present and consistent with the allowance.
- Worker/analyzer exit codes, OOM state, complete logs, and missing-record
  reconciliation are inspected.
- Manifest hardware, resource limits, source state, image identities, lock
  hashes, workload identity, execution boundary, and analysis policy are correct.
- Every planned model has a qualification decision. Review both `status` and
  `adjusted_estimate_status`, not just whether the CLI exited successfully.

`qualified` means the timing model passed configured checks. `inconclusive`
means it failed a gate or lacked enough evidence to evaluate one. Neither means
an EVM correctness failure. An inconclusive fit must not produce a recommended
price when `block_unqualified` is enabled.

Retain the complete new run: `workload.json`, `requested-samples.jsonl`,
`samples.jsonl`, `manifest.json`, `campaign.json`, `config.json`,
`exclusions.jsonl`, session requests/logs, analyzer inputs, and reports. Store
experiment outputs outside Git. Preserve image archives if exact binary reuse
is important; mutable base-image rebuilds are not sufficient for that guarantee.

To change analysis configuration without rerunning execution, first write a
complete revised config on this machine, then use:

```bash
"$BENCHMARKOOR_BIN" analyze --run "$RUN_DIR" \
  --analysis-config "$RUN_HOME/revised-gasfit.yaml"
```

Set `RUN_DIR` to the newly created run directory. This creates a separate
analysis attempt and updates the run's analysis pointer while preserving raw
observations. Do not edit archived measurements or manifests.

Reanalysis uses the analyzer image stored in `campaign.json`; there is no
image-override flag. If changing analyzer code, build a new image and run its
existing CLI on the new run's exported inputs, writing a new output directory
and recording the new image/source identity:

```text
evm-gasfit run --config CONFIG --runtimes RUNTIMES_CSV \
  --opcounts OPCOUNTS_JSON --manifest MANIFEST_JSON --out NEW_OUTPUT_DIRECTORY
```

These are files generated by the new campaign, not files to transfer from the
previous machine. Different hardware invalidates a software-only A/B comparison;
never rewrite a manifest to conceal the hardware difference.

## 9. Remaining development before final gas pricing

### Glue adjustment is the first substantive gap

Gasfit already estimates supported glue operations, detects their count ratios,
subtracts their contribution from target slopes, and propagates uncertainty.
Reuse this machinery. Do not invent a second glue implementation.

The current target-only corpus lacks these required driver fixtures:

| Driver | Glue estimates affected |
| --- | --- |
| `test_calldatasize` | CALLDATASIZE |
| `test_memory_access` | MLOAD; also mixed-tier MSTORE/MSTORE8 |
| `test_ext_account_query_warm` | STATICCALL |

Simply changing `glue_adjustment.enabled` to true fails input validation.
Additional absent mixed-tier drivers cover CALLDATACOPY, CALLDATALOAD,
RETURNDATASIZE, and SELFBALANCE. POP and STOP have no driver in the current
estimator and are optional/unpriced. Lack of a hard error does not imply all
supporting work was accounted for.

Resolve how to obtain supporting calibration measurements without silently
expanding the compute target corpus. A separate, explicitly typed
calibration-only lane is a possible approach, but it requires a scope/design
decision. Do not bypass the allowlist or import another machine's glue timings.

Check driver/family coverage, fit quality, count-correlated contributions, and
shared-session bootstrap alignment. An adjusted interval marked
`glue_interval_conditional: true` is not sufficient for a recommended price.
Prefer collecting target and glue evidence in a coordinated session design.

### Model mappings and input dependence

The default catalog does not cover all collected variants. Known mismatches
include BLS map-to-G1/G2 addresses and BN128 addition/pairing selection.
Use explicit, tested selectors and exported addressed count keys. EXP, KECCAK,
MODEXP, pairing, and MSM require appropriate input-dependent coefficients or
formulas. A fixed-input slope is not a universal precompile price.

The workload-only recipes in section 7 deliberately avoid current-gas baselines.
Do not make them official gas estimates merely by renaming `WORKLOAD_*` labels.
Use appropriate setup/baseline controls where needed, and do not subtract a
supporting contribution twice.

### Calibration and qualification

After supporting-work correction, the working normalization is:

```text
proposed gas = adjusted runtime in ns × 0.5
```

The supported top-level config field is `anchor_rate: 500000000`. Named
scenarios use `pricing_scenarios` entries with `name`, `anchor_rate`, and
`margin_pct`. A final safety-margin and rounding policy has not been selected.
If using zero margin for exploration, label it explicitly rather than implying
that it is the final policy.

Keep all qualification gates armed. Investigate curvature, poor holdouts,
uncertainty, and insufficient count ranges instead of weakening thresholds to
make reports green. Single-count expensive MODEXP needs a different defensible
measurement design if a slope is required.

A qualified subset is not automatically a worst-case bound: inconclusive input
variants may be more expensive. Report adjusted intervals and unresolved
coverage, and do not turn exclusions into artificially cheap proposed prices.

### Recommended development order

1. Reproduce the installation smoke on the new machine.
2. Inspect and confirm the exporter/worker/analyzer contracts in the pinned
   source, especially model selection and glue driver requirements.
3. Decide and implement the calibration-only measurement design while preserving
   the target allowlist and production semantics.
4. Validate model mappings and input parameterization against freshly generated
   fixtures and diagnostic counts.
5. Prove a small real glue-enabled end-to-end analysis, including uncertainty
   propagation and rejected-price behavior.
6. Freeze the full generation selections, calibration controls, resource policy,
   model recipes, 500M gas/s anchor, aggregation, margin, and rounding policy.
7. Generate and preflight the full corpus; then capture the reference-machine
   measurements and retain complete artifacts.
8. Investigate inconclusive models and publish only justified adjusted estimates
   with coverage limitations. Do not claim completion at the workload-only stage.

## 10. Source map and development verification

Paths below are relative to the sibling repositories:

| Area | Entry points |
| --- | --- |
| Controller | `benchmarkoor/cmd/benchmarkoor/compute.go`, `analyze.go` |
| Orchestration and provenance | `benchmarkoor/pkg/compute/campaign.go`, `worker.go`, `manifest.go` |
| Gasfit export and reanalysis | `benchmarkoor/pkg/compute/export.go`, `analysis.go` |
| Protocol | `benchmarkoor/pkg/compute/protocol.go`, `schema/` |
| Exporter | `execution-specs/packages/testing/src/execution_testing/benchmark/workload.py` |
| Fill integration | `execution-specs/packages/testing/src/execution_testing/cli/pytest_commands/plugins/filler/filler.py` |
| Benchmark definitions | `execution-specs/tests/benchmark/compute/` |
| Native worker | `evm2/crates/cli/src/compute/main.rs`, `worker.rs`, `protocol.rs` |
| Config and presets | `evm-gasfit/src/evm_gasfit/config.py`, `defaults/models.py` |
| Glue | `evm-gasfit/src/evm_gasfit/glue/required.py`, `detect.py`, `estimate.py`, `adjust.py` |
| Models and qualification | `evm-gasfit/src/evm_gasfit/modeling/estimate.py`, `qualification.py`, `nnls.py` |

Read each repository's own contribution instructions before modifying it.
For execution-specs changes, follow its test/fork skills and run its prescribed
lint workflow before committing. Preserve revm semantics when changing evm2.
No compatibility layers or old Prague/NewL1 integration paths are needed.

With the corresponding development tools installed, the existing verification
entry points include:

```text
benchmarkoor:
  go test -tags 'exclude_graphdriver_btrfs,exclude_graphdriver_devicemapper,containers_image_openpgp' ./pkg/config ./pkg/compute
  bash scripts/compute/smoke.sh

evm-gasfit:
  pytest
  ruff check .
  ruff format --check .

evm2:
  cargo cl
  cargo fmt --all
  cargo nextest run
```

The Docker-only setup above intentionally does not install host Rust, Go, or
Python development environments. Use repository-supported toolchains when
moving from operating the campaign to implementation work. Do not install
unrelated global dependencies merely to run the operator workflow.

## 11. Optional revm comparison

The evm2 repository has an independent bundled comparison suite:

```bash
# From the evm2 checkout, with its native build prerequisites installed:
EVM2_BENCH_REVM=1 cargo bench -p evm2-cli --bench evm
```

The pinned checkout resolves revm **43.0.2**; the inspected NewL1 checkout used
revm **38.0.0**. Comparing those bundled fixtures is not comparing against
NewL1's exact revm version. The suite uses mixed forks, including Berlin for
MODEXP. The transaction harness commits evm2 changes while revm returns changes
without committing, so it is not a pure interpreter-only comparison.

No comparison outputs need to be transferred. Run it afresh if useful, separate
from timed compute-campaign execution. Neither suite establishes full-node
throughput.

## 12. Troubleshooting and completion criteria

| Symptom | Action |
| --- | --- |
| Docker socket denied | Re-login after group membership change; verify access as the intended user |
| `--build-context` unavailable | Install buildx/use BuildKit |
| Go VCS-status failure in build container | Keep `-buildvcs=false`; source provenance is recorded separately |
| Invalid cpuset | Choose a CPU online and available to the process |
| OOM or unavailable swap control | Investigate host cgroups/resource settings; do not hide the failure |
| No tests selected by regex | Include the fixture-format component of pytest IDs |
| Fixture output already populated | Use a fresh directory and preserve the earlier attempt |
| MODEXP cannot fit | Respect the protocol cap and report the exclusion |
| Missing glue drivers | Resolve section 9; an enabled flag is not a substitute for evidence |
| Inconclusive fit | Inspect the recorded reason and model/count design; do not confuse it with correctness failure |
| Interrupted campaign | Preserve the run and start a new one; do not fabricate successful retries |

The measurement workflow is successful when the new corpus is independently
generated, every requested sample is accounted for, ready cases pass correctness
and count checks, timed rows are valid, provenance is complete, and analysis
finishes with explicit model decisions.

The gas-pricing workflow is successful only after supporting-work correction,
model parameterization, uncertainty propagation, worst-case coverage, and the
selected calibration policy are justified. The previous trial did not meet
that latter completion criterion.

Handoff preparation verified the pinned sources' remote availability, the
containerized controller build, the real installation smoke, and fresh selected
exports with ready/unsupported cases. Installing Ubuntu on the future host and
performing its full reference capture remain actions for that machine. This
document carries the decisions and implementation context; it carries no old
benchmark inputs or results.
