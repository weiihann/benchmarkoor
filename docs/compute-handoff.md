# Osaka compute campaign: second-machine handoff

Date: 2026-09-23.

## 1. Decision and scope

Run the **same completed standalone Osaka EVM2 compute campaign on a second machine**, retain both datasets, and compare them before deciding how to proceed with compute gas-schedule changes. No production gas constants should change as part of this rerun.

This handoff supersedes the earlier 500M gas/s, document-only transfer, fresh-generation, and glue-disabled instructions. The current anchor is **600,000,000 gas/s**, with zero discretionary margin, no decreases, and a qualified lower-bound discrepancy of at least 2× to trigger an increase. The second-machine capture has not yet run.

For a hardware comparison, reuse the frozen workload and executable artifacts. Generate **new diagnostics and every new timing sample** on the second machine. Do not regenerate different bytecode, rebuild against newer dependencies, or use the built-in `cargo bench` suite or Reth integration instead.

Read [METHODOLOGY.md](../METHODOLOGY.md) for the full experiment design and [the first-machine report](compute-gas-pricing-600m.md) for its results. The first machine produced 403 qualified raw workload models out of 433 runnable target variants. None of the isolated, supporting-cost-adjusted estimates qualified. Nine provisional precompile-parameter increases came from the separate whole-workload budget analysis. These outcomes are baseline observations, not required outcomes on the second host.

### Frozen experiment contract

| Setting | Required value |
| --- | --- |
| Fork and engine | Osaka, standalone EVM2 interpreter, JIT disabled |
| Target families | Arithmetic, bitwise, comparison, stack, control flow, KECCAK256, precompiles |
| Target variants | 437 selected: 433 runnable, four unsupported |
| Runnable count-point cases | 2,138 target + 94 calibration = 2,232 |
| Workload records | 2,236 including unsupported records |
| Transactions | One per runnable case; gas limit 16,777,216 |
| Seed | 20260922 |
| Schedule | Eight sessions; one pilot, one warmup, five qualification repetitions |
| Worker processes | 17 sequential processes: one diagnostic, eight pilot, eight qualification |
| Worker resources | One deliberately selected logical CPU, 24 GiB limit, swap disabled |
| Capture timeout | Eight hours per session |
| Timing boundary | `evm2_transaction_execution`: validation, execution, settlement, in-memory commit |
| Analysis | Frozen configuration, qualification-only eligibility, 1,000 session bootstrap draws, unchanged gates |
| Pricing | 600M gas/s; zero margin; no decreases; 2× trigger; upward two-significant-digit rounding |

Changes to paths, host label, run UUIDs, and deliberately chosen CPU IDs are necessary host adaptations. Hardware, operating system, kernel, CPU frequency behavior, and virtualization must be recorded as comparison factors. Any other departure from this contract must be documented before capture and treated as a different experiment, not silently called an exact replay.

## 2. Authoritative artifacts

On the first machine, the completed campaign root is:

```text
/server/weihan/benchmarkoor/results/osaka-pricing-600m-20260922T163024Z/reviewed
```

Its full capture is `runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2`; its original analysis attempt is `ba6bcc97-7b65-4be1-828e-8321e28b3c79`.

| Artifact | Immutable identity |
| --- | --- |
| `corpus/workload.json` SHA-256 | `5051433bf7c8f0bc7c2e5ff197a00138706e9f53e76cf88be417d28d656edae7` |
| `analysis-gasfit.yaml` SHA-256 | `58f5e86a6671b41b6c1fb1e7aa1046aeb6f20f7deb1c481e00d6ce9f95008f47` |
| Controller binary SHA-256 | `c5c29e2ab617353974298026086ba4e5b403a33616ce79c637783cca5dd1f191` |
| Generator image | `sha256:cabf253b75e8ba745f68cbad0c54f4a141dd5db178c140027f679ceef8ad2f22` |
| Worker image | `sha256:5e2dda70a14ad0cf6fdab1a5784019fa58bc321dad004ebd3c242cc2fb15e0a5` |
| Capture analyzer image | `sha256:dd75e2bf0310efbe2cdea1da504e49af67868921d347e9d0f39f9dca66fea919` |
| Final recommendation image | `sha256:93b31045e31659d7ccd0bba0d0856c198fd2a8463d40312e5072404ab4213d42` |

These are local Docker image IDs, not registry pull references. Transfer them with `docker image save` and `docker image load`. All four images were verified as Linux/amd64 artifacts on the first machine.

### Why source pins are insufficient

The base revisions were:

| Repository | Base revision |
| --- | --- |
| `weiihann/benchmarkoor` | `fda9738ed7e8524945ab5b8c2f407388b11ce03f` |
| `weiihann/execution-specs` | `6ee9c7854ca22cb8f5259bac2fa20892d6dfab06` |
| `weiihann/evm2` | `c0dfb22c27044e82e83f42f299525f740b16c1a3` |
| `weiihann/evm-gasfit` | `c40409e3c83e082d8629d0545e268d109833ef27` |

The generator, controller, and analyzer included local modifications. EVM2's ignored `Cargo.lock` also matters to a rebuild. A fresh checkout of these revisions, or the current feature-branch heads, does not reconstruct the captured executables.

The replay below therefore uses the exact controller and images. Original source and lock identities remain in `baseline/recommendations-reviewed/provenance.json` and the original run manifest. It does not fabricate second-host source-checkout provenance. Rebuilding or developing the pipeline is a separate task requiring the corresponding source snapshots and lockfiles.

## 3. Export from the first machine

Do this before the first machine or its Docker image store becomes unavailable. Updating this document alone does not transfer the binaries, images, or ignored results.

The archive below includes the whole reviewed campaign so the first machine's measurements remain available for comparison. That directory was approximately 646 MiB when this handoff was prepared, excluding image exports. Allow additional space for the image archive, loaded images, and new capture outputs.

Run these commands in Bash on the first machine. The destination must be new; do not overwrite an earlier transfer.

```bash
set -euo pipefail
export SOURCE_REPO=/server/weihan/benchmarkoor
export FROZEN="$SOURCE_REPO/results/osaka-pricing-600m-20260922T163024Z/reviewed"
export BUNDLE="$HOME/osaka-replay-20260922"
python3 - <<'PY'
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

repo = Path(os.environ['SOURCE_REPO'])
frozen = Path(os.environ['FROZEN'])
bundle = Path(os.environ['BUNDLE'])
provenance = json.loads((frozen / 'recommendations-reviewed/provenance.json').read_text())

def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(chunk)
    return result.hexdigest()

assert digest(repo / 'bin/benchmarkoor') == provenance['controller_sha256']
assert digest(frozen / 'corpus/workload.json') == provenance['workload_sha256']
assert digest(frozen / 'analysis-gasfit.yaml') == '58f5e86a6671b41b6c1fb1e7aa1046aeb6f20f7deb1c481e00d6ce9f95008f47'
images = [*provenance['capture_images'].values(), provenance['postprocessor_image']]
for image in images:
    actual = json.loads(subprocess.check_output(['docker', 'image', 'inspect', image]))[0]
    assert actual['Id'] == image
    assert (actual['Os'], actual['Architecture']) == ('linux', 'amd64')

bundle.mkdir()  # Refuse to overwrite an existing transfer.
(bundle / 'bin').mkdir()
(bundle / 'docs').mkdir()
shutil.copy2(repo / 'bin/benchmarkoor', bundle / 'bin/benchmarkoor')
shutil.copytree(frozen, bundle / 'baseline')
for name in ('CONTEXT.md', 'METHODOLOGY.md'):
    shutil.copy2(repo / name, bundle / name)
for name in ('compute-handoff.md', 'compute-gas-pricing-600m.md', 'compute.md'):
    shutil.copy2(repo / 'docs' / name, bundle / 'docs' / name)
subprocess.run(['docker', 'image', 'save', '--output', str(bundle / 'images.tar'), *images], check=True)
with (bundle / 'SHA256SUMS').open('x') as output:
    for path in sorted(bundle.rglob('*')):
        if path.is_file() and path != bundle / 'SHA256SUMS':
            output.write(f'{digest(path)}  {path.relative_to(bundle)}\n')
print(f'Transfer the complete directory: {bundle}')
PY
```

The completed directory contains:

```text
osaka-replay-20260922/
  SHA256SUMS
  images.tar
  bin/benchmarkoor
  CONTEXT.md
  METHODOLOGY.md
  docs/
  baseline/                 original reviewed campaign, unchanged
```

Transfer the entire directory over your normal authenticated SSH/rsync/SCP channel. Do not commit generated results or the image archive. Copying only the Markdown files is insufficient. The copied report contains original workspace-relative links; use `baseline/` for the transferred artifacts, not the old absolute `/server/weihan/...` paths.

## 4. Prepare and verify the second machine

Use native **x86_64 Linux**, Python 3.9 or newer, Bash, Docker Engine, and the `taskset`, `lscpu`, `free`, `df`, `sha256sum`, and `tee` utilities. Install Docker through its [official Linux installation instructions](https://docs.docker.com/engine/install/). The operator must have permission to use its local Docker daemon. Docker access is root-equivalent; do not change permissions on the socket to bypass setup.

Do not use QEMU architecture emulation. On another architecture, or if the original worker cannot execute on the target CPU, stop and record the incompatibility rather than silently rebuilding. A fresh Go/Rust/Python analysis development environment is not required: the host controller is transferred and engine/analysis dependencies are in the images.

A host with at least 32 GiB RAM is recommended for the 24 GiB worker limit. Ensure Docker's data volume and the results filesystem have enough space. Do all installation, image loading, and other heavy preparation before timing starts.

Set `BUNDLE` to the received directory. `ROOT` is a separate workspace for new outputs:

```bash
set -euo pipefail
export BUNDLE="$HOME/osaka-replay-20260922"
export ROOT="$HOME/osaka-compute"
export HOST_ID=machine-b
export RUN_HOME="$ROOT/results/${HOST_ID}-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$ROOT/results"
(cd "$BUNDLE" && sha256sum --check SHA256SUMS)
docker image load --input "$BUNDLE/images.tar"
python3 - <<'PY'
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess

bundle = Path(os.environ['BUNDLE']).resolve()
assert platform.system() == 'Linux' and platform.machine() == 'x86_64'
p = json.loads((bundle / 'baseline/recommendations-reviewed/provenance.json').read_text())
controller_hash = hashlib.sha256()
with (bundle / 'bin/benchmarkoor').open('rb') as stream:
    for chunk in iter(lambda: stream.read(1024 * 1024), b''):
        controller_hash.update(chunk)
assert controller_hash.hexdigest() == p['controller_sha256']
for image in [*p['capture_images'].values(), p['postprocessor_image']]:
    info = json.loads(subprocess.check_output(['docker', 'image', 'inspect', image]))[0]
    assert info['Id'] == image
    assert (info['Os'], info['Architecture']) == ('linux', 'amd64')
print('Controller and image identities match the first machine.')
PY
"$BUNDLE/bin/benchmarkoor" run --help
"$BUNDLE/bin/benchmarkoor" analyze --help
lscpu
lscpu -e=CPU,CORE,SOCKET,NODE,ONLINE
free -h
df -h "$ROOT"
docker info
```

### CPU and host controls

Inspect topology and the CPUs available to this process. Choose one worker logical CPU and a controller CPU on a **different physical core**, not its SMT sibling. CPU 14 and CPU 0 were first-host choices, not requirements to copy blindly.

The values below are examples. Replace them based on the new machine's topology before continuing:

```bash
export WORKER_CPU=2
export CONTROL_CPU=0
```

Keep the worker's SMT sibling idle where operationally possible, and record whether it is actually reserved. Docker affinity does not reserve a CPU or its sibling. Keep builds, generation, other benchmarks, and maintenance away from the timed capture. Record CPU governor/turbo policy, NUMA placement, virtualization, kernel, firmware where available, and any host interference. Do not silently change system-wide policies.

The new manifest will record host and resource information. Add an operator note for controls it cannot establish, such as sibling isolation, cloud tenancy, thermal conditions, or missing frequency controls.

## 5. Create host-specific configurations

Keep `baseline/` immutable. Create a fresh run home and copy only inputs into it. The script below changes paths, IDs, source-checkout metadata, and CPU affinity. It does not change workload bytes, the analysis configuration, the frozen policy, images, schedule, or statistical gates.

`source_paths` is deliberately empty because this is an executable-artifact replay without local source checkouts. The adaptation record points to original source provenance; it does not claim to have inspected the first machine's paths on this host. The original `policy.json` still describes CPU 14/0 and fresh generation on the first machine. Keep it unchanged as historical evidence; `host-adaptation.json` and the new capture configuration describe this replay.

Do not run the old `pricing_campaign.py config` recipe unchanged. Its generation defaults reserve first-host CPUs 14/30, its paths refer to the old workspace, and recreating analysis configuration could introduce drift. The archived mixed-lane smoke inputs are already available.

```bash
python3 - <<'PY'
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

bundle = Path(os.environ['BUNDLE']).resolve()
base = bundle / 'baseline'
home = Path(os.environ['RUN_HOME']).resolve()
worker, control = int(os.environ['WORKER_CPU']), int(os.environ['CONTROL_CPU'])
assert worker != control
assert {worker, control} <= os.sched_getaffinity(0)
# CPU numbers alone are insufficient: reject SMT siblings on the same core.
rows = subprocess.check_output(['lscpu', '-p=CPU,CORE,SOCKET'], text=True).splitlines()
cores = {int(row.split(',')[0]): tuple(row.split(',')[1:])
         for row in rows if row and not row.startswith('#')}
assert cores[worker] != cores[control], 'Controller and worker share a physical core'
home.mkdir()  # Refuse to reuse a prior experiment home.
(home / 'config').mkdir()
(home / 'corpus').mkdir()
(home / 'host').mkdir()
for name in ('analysis-gasfit.yaml', 'policy.json', 'reviewed-resolved-inputs.json'):
    shutil.copy2(base / name, home / name)
shutil.copy2(base / 'corpus/workload.json', home / 'corpus/workload.json')
for name in ('smoke-workload.json', 'smoke-gasfit.yaml'):
    shutil.copy2(base / 'config' / name, home / 'config' / name)
for name, smoke in [('compute.yaml', False), ('smoke-compute.yaml', True)]:
    config = json.loads((base / 'config' / name).read_text())
    c = config['compute']
    c['id'] = os.environ['HOST_ID'] + ('-smoke' if smoke else '-full')
    c['workload'] = str(home / ('config/smoke-workload.json' if smoke else 'corpus/workload.json'))
    c['analyzer']['config'] = str(home / ('config/smoke-gasfit.yaml' if smoke else 'analysis-gasfit.yaml'))
    c['results_dir'] = str(home / ('smoke-results' if smoke else 'capture'))
    c['resource_limits']['cpuset'] = [worker]
    c['source_paths'] = {}
    (home / 'config' / name).write_text(json.dumps(config, indent=2) + '\n')

adaptation = {
    'mode': 'frozen-artifact-replay', 'host_id': os.environ['HOST_ID'],
    'baseline_root': str(base), 'worker_cpu': worker, 'controller_cpu': control,
    'original_source_provenance': str(base / 'recommendations-reviewed/provenance.json'),
    'local_source_checkouts_inspected': False,
    'controller_sha256': hashlib.sha256((bundle / 'bin/benchmarkoor').read_bytes()).hexdigest(),
    'workload_sha256': hashlib.sha256((home / 'corpus/workload.json').read_bytes()).hexdigest(),
    'analysis_config_sha256': hashlib.sha256((home / 'analysis-gasfit.yaml').read_bytes()).hexdigest(),
}
(home / 'host-adaptation.json').write_text(json.dumps(adaptation, indent=2) + '\n')
for name, argv in [('lscpu.txt', ['lscpu']), ('topology.txt', ['lscpu', '-e=CPU,CORE,SOCKET,NODE,ONLINE']),
                   ('kernel.txt', ['uname', '-a']), ('memory.txt', ['free', '-h']),
                   ('docker-info.txt', ['docker', 'info'])]:
    (home / 'host' / name).write_text(subprocess.check_output(argv, text=True))
print(f'New campaign home: {home}')
PY
```

Write the operator's environmental notes under `$RUN_HOME/host/` before capture. If returning in another shell, restore `BUNDLE`, `RUN_HOME`, `WORKER_CPU`, and `CONTROL_CPU` to these exact values rather than creating a new timestamp accidentally.

## 6. Run the frozen mixed-lane smoke

Use the transferred controller and existing images. Do not use `scripts/compute/smoke.sh`: that script rebuilds images and generates its own small corpus, rather than exercising these frozen artifacts.

```bash
set -euo pipefail
taskset -c "$CONTROL_CPU" "$BUNDLE/bin/benchmarkoor" run \
  --config "$RUN_HOME/config/smoke-compute.yaml" \
  2>&1 | tee "$RUN_HOME/smoke.log"
```

The archived smoke uses four sessions, two qualification repetitions, one pilot, and one warmup, with the strict analysis contract and glue enabled. Its expected accounting is **2,703 executed records** from 159 cases. It has 13 target models and 94 calibration cases. Qualification may be inconclusive; do not relax thresholds to make the smoke look successful.

Before proceeding, check its run under `$RUN_HOME/smoke-results/runs/`:

- Every requested sample has exactly one terminal result and every executed result passes correctness.
- All nine worker sessions have observed, zero-code, non-OOM exits.
- The analyzer exits zero without OOM, writes its reports, and records `succeeded` or `inconclusive`, not `failed`.
- Timed samples have positive durations and no operation counters; diagnostics have target and opcode counts.

A smoke failure, illegal instruction, missing library, wrong image, unsupported resource control, or correctness failure must be investigated before the full capture. Preserve failed outputs. Do not silently switch worker builds or relax validation.

## 7. Run the full second-machine capture

Stop other heavy work first. Run once in a terminal session that will survive an SSH disconnect, or a supervised job with retained logs. Do not run generation, builds, tests, or another benchmark concurrently.

```bash
set -euo pipefail
taskset -c "$CONTROL_CPU" "$BUNDLE/bin/benchmarkoor" run \
  --config "$RUN_HOME/config/compute.yaml" \
  2>&1 | tee "$RUN_HOME/full-capture.log"
```

This creates a new `$RUN_HOME/capture/runs/compute-<UUID>/`, performs fresh diagnostics, pilots, warmups, and qualification measurements, then automatically analyzes **only this new run** with the frozen capture analyzer and analysis configuration.

Do not copy original sample records into this directory or reuse the first host's run UUID. Deterministic sample/session names may match across hosts; the new run UUID and host identity distinguish them.

If capture fails, keep the complete attempt. Do not resume it by replacing missing measurements with a later successful run. Investigate and document the failure before an explicit new full attempt in a fresh output location. Do not select whichever repeated campaign happens to be fastest.

If only analysis fails after a complete capture, preserve the failed attempt and create a new immutable analysis attempt without remeasuring:

```bash
# Set NEW_RUN to the exact completed second-host run directory from the log.
"$BUNDLE/bin/benchmarkoor" analyze --run "$NEW_RUN"
```

Do not pass `--analysis-config` for this replay. The command uses the run's archived configuration. Record the selected analysis attempt explicitly if there is more than one; do not choose one by sorting UUIDs.

## 8. Audit the full capture

Successful process exit is insufficient. The expected accounting is:

| Phase | Executed | Unsupported |
| --- | ---: | ---: |
| Diagnostic | 2,232 | 4 |
| Pilot | 17,856 | 0 |
| Warmup | 17,856 | 0 |
| Qualification | 89,280 | 0 |
| Total | 127,224 | 4 |

Each runnable case has 57 executed records, including 40 qualification records. The qualification split is 85,520 target and 3,760 calibration records. There must be 17 successful worker processes, no missing or duplicate requested samples, and no correctness failures.

The following read-only audit checks the normal single-capture, single-analysis path, then writes a summary outside the run. If you have explicitly repeated analysis, select and record the intended attempt instead of weakening its identity checks. It compares diagnostics and execution identities with the first host, not runtime values.

```bash
python3 - <<'PY'
from collections import Counter
import hashlib
import json
import os
from pathlib import Path

home = Path(os.environ['RUN_HOME']).resolve()
base = Path(os.environ['BUNDLE']).resolve() / 'baseline'
old = base / 'runs/compute-dbb11b32-98ad-4634-99ea-4d6a89821aa2'
runs = sorted((home / 'capture/runs').glob('compute-*'))
assert len(runs) == 1, 'Select an explicitly documented capture; do not pick the fastest'
run = runs[0]
attempts = sorted((run / 'analysis').glob('*/status.json'))
assert len(attempts) == 1, 'Select and document the intended analysis attempt'
attempt = attempts[0].parent

def rows(path):
    with path.open() as stream:
        for line in stream:
            if line.strip():
                yield json.loads(line)

def sha(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(chunk)
    return result.hexdigest()

assert sha(run / 'workload.json') == '5051433bf7c8f0bc7c2e5ff197a00138706e9f53e76cf88be417d28d656edae7'
assert sha(attempt / 'config.yaml') == '58f5e86a6671b41b6c1fb1e7aa1046aeb6f20f7deb1c481e00d6ce9f95008f47'
assert sha(run / 'requested-samples.jsonl') == sha(old / 'requested-samples.jsonl'), 'Sample schedule drift'
requests = list(rows(run / 'requested-samples.jsonl'))
requested = {r['sample_id']: r for r in requests}
assert len(requests) == len(requested) == 127228
reference = {r['case_id']: r for r in rows(old / 'sessions/diagnostic-00/samples.jsonl')}
seen, phases, statuses, per_case, qualified = set(), Counter(), Counter(), Counter(), Counter()
identity_fields = ('baseline_hash', 'prepared_hash', 'commitment_hash', 'declared_gas', 'charged_gas', 'execution_boundary')
for sample in rows(run / 'samples.jsonl'):
    sid = sample['sample_id']
    assert sid not in seen and sid in requested
    seen.add(sid)
    for key in ('case_id', 'session_id', 'phase', 'repetition'):
        assert sample[key] == requested[sid][key], (sid, key)
    phases[sample['phase']] += 1
    statuses[sample['status']] += 1
    expected = reference[sample['case_id']]
    if sample['status'] == 'unsupported':
        assert sample['phase'] == 'diagnostic' and expected['status'] == 'unsupported'
        continue
    assert sample['status'] == 'executed' and sample['correctness_passed'] is True
    assert expected['status'] == 'executed'
    for key in identity_fields:
        assert sample[key] == expected[key], (sid, key)
    per_case[sample['case_id']] += 1
    if sample['phase'] == 'diagnostic':
        assert sample.get('execution_duration_ns') is None
        assert sample['target_count'] == expected['target_count']
        assert sample['opcode_counts'] == expected['opcode_counts']
    else:
        assert sample['execution_duration_ns'] > 0
        assert sample.get('target_count') is None and sample.get('opcode_counts') is None
    if sample['phase'] == 'qualification':
        qualified[sample['case_id']] += 1
assert seen == set(requested)
assert statuses == {'executed': 127224, 'unsupported': 4}
assert phases == {'diagnostic': 2236, 'pilot': 17856, 'warmup': 17856, 'qualification': 89280}
assert len(per_case) == 2232 and set(per_case.values()) == {57}
assert len(qualified) == 2232 and set(qualified.values()) == {40}
exits = list((run / 'sessions').glob('*/exit.json'))
assert len(exits) == 17
for path in exits:
    state = json.loads(path.read_text())
    assert state['observed'] and state['exit_code'] == 0 and not state['oom_killed'] and not state['error']
status = json.loads((attempt / 'status.json').read_text())
assert status['status'] in ('succeeded', 'inconclusive')
assert status['exit_code'] == 0 and not status['oom_killed']
assert status['analyzer_image'] == 'sha256:dd75e2bf0310efbe2cdea1da504e49af67868921d347e9d0f39f9dca66fea919'
for name in ('results.csv', 'qualification.csv', 'analysis_status.json'):
    assert (attempt / 'reports' / name).is_file()
summary = {'run': str(run), 'analysis': str(attempt), 'records': len(seen),
           'phases': dict(phases), 'statuses': dict(statuses),
           'diagnostics_match_first_host': True, 'analysis_status': status['status']}
with (home / 'capture-audit.json').open('x') as stream:
    json.dump(summary, stream, indent=2)
print(json.dumps(summary, indent=2))
PY
```

Also review the new `manifest.json`, `campaign.json`, `host-adaptation.json`, analysis eligibility/exclusions, and host notes. Confirm the configured worker image, `[WORKER_CPU]`, `24g`, disabled swap, schedule, seed, and execution boundary. Do not rewrite the new manifest to make it match first-host hardware or source metadata.

The original staging auditor has first-host generation/CPU assumptions. The portable checks above do not require running generation stages on the second host. They establish capture integrity and semantic consistency, not model quality or an approved price schedule.

## 9. Generate provisional recommendation data

The capture analyzer and final recommendation postprocessor are **different frozen images**. Keep both identities unchanged. The original campaign received recommendation-only corrections after capture; the final image is `sha256:93b310...`, not an earlier tag or a host-mounted Python source override.

The audit writes the exact new run and analysis paths into `$RUN_HOME/capture-audit.json`. Use them to run the final postprocessor. Its inputs are the new host's reports and diagnostics plus the unchanged workload and resolved-input sidecar:

```bash
python3 - <<'PY'
import json
import os
from pathlib import Path
import subprocess

home = Path(os.environ['RUN_HOME']).resolve()
audit = json.loads((home / 'capture-audit.json').read_text())
run = Path(audit['run']).relative_to(home)
attempt = Path(audit['analysis']).relative_to(home)
argv = [
    'docker', 'run', '--rm', '--network', 'none',
    '--cpuset-cpus', os.environ['CONTROL_CPU'], '-v', f'{home}:/work',
    '--entrypoint', 'python',
    'sha256:93b31045e31659d7ccd0bba0d0856c198fd2a8463d40312e5072404ab4213d42',
    '-m', 'evm_gasfit.recommendations', 'build',
    '--workload', '/work/corpus/workload.json',
    '--analysis', f'/work/{attempt}/reports',
    '--diagnostics', f'/work/{run}/sessions/diagnostic-00/samples.jsonl',
    '--resolved-inputs', '/work/reviewed-resolved-inputs.json',
    '--out', '/work/recommendations-provisional',
]
with (home / 'recommendations.command.json').open('x') as stream:
    json.dump(argv, stream, indent=2)
with (home / 'recommendations.log').open('x') as log:
    subprocess.run(argv, stdout=log, stderr=subprocess.STDOUT, check=True)
print(home / 'recommendations-provisional')
PY
```

Expected output files are `recommendations.json` and `recommendations.csv`. The command does **not** create the human-reviewed `reviewed-schedule.json` from the first-machine report. A short CLI summary showing zero isolated increase candidates does not describe all workload-budget evidence. Review the two analyses separately.

Use a fresh output directory for any explicitly requested reanalysis. Never overwrite the first-machine recommendations or copy its nine decisions into the second-machine results.

## 10. Return both datasets and compare before deciding

Preserve the transfer bundle and the complete new `$RUN_HOME`, including unsuccessful attempts. Return the new run home to the analysis workspace using the same authenticated transfer process and a checksummed archive or directory manifest.

The return set must include:

- Host metadata, operator notes, adaptation record, effective configurations, and controller/image identities.
- Full workload, requested schedule, raw samples, diagnostics, per-session requests/exits/logs, manifests, and capture log.
- Every analysis attempt, its input hashes, CSV/count inputs, reports, status, and analyzer log.
- The unchanged pricing sidecar and policy, provisional recommendation files, command/log, and capture audit.
- Explicit selected run/analysis IDs, plus reasons for any retries or deviations.

Use stable host labels in the comparison. The original machine was a Xeon Platinum 8559C under KVM with worker CPU 14, controller CPU 0, and a 24 GiB worker limit. Its SMT sibling was not host-reserved. Hardware is intentionally different in this experiment; do not claim it is a software-only controlled comparison.

Compare matching variant/count identities and independently fitted per-host models. Include raw slope, 95% interval, intercept, qualification status/reasons, supporting-cost qualification, marginal charged-gas credit, and provisional price requirement. Report variants qualified on both hosts separately from variants blocked on either host. Do not report only successful intersections or drop unsupported cases from coverage accounting.

Do not concatenate the two hosts' timing rows and treat equal-named sessions such as `qualification-00` as one bootstrap cluster. Do not average runtimes, select the faster host, select the slower host, or combine confidence bounds into a final gas policy without an explicit subsequent decision. First establish how the evidence differs.

The first host's 403 raw qualifications, zero adjusted qualifications, and nine reviewed increases need not recur. Differences in timing-derived outcomes are legitimate results. Differences in workload bytes, operation counts, charged gas, correctness, or execution commitments must be resolved before interpreting a runtime difference as a hardware effect.

The process-global short-input KECCAK cache, unresolved supporting-cost estimates, unsupported variants, limited input coverage, and absence of full-node/state-root timing remain limitations. A second machine does not remove them automatically.

The worker still does not expose an active gas-schedule fingerprint. The original and new manifests may therefore fail native comparison compatibility checks, both because hardware differs and because this fingerprint is unavailable. Keep that evidence. Do not fabricate a fingerprint or weaken comparison gates to obtain a green status; document the exact-image, exact-workload hardware comparison separately.

Completion means a verified second-machine capture with retained evidence for both hosts and an explicit comparison ready for review. It does not mean a merged or finalized gas-schedule change.
