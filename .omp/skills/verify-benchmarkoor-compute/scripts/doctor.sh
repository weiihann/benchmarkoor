#!/usr/bin/env bash
# Read-only readiness check. Exit 0 means this checkout's controller is current
# and no compute container occupies the host; exit 1 lists every blocker.
# Usage: doctor.sh
set -uo pipefail
root="$(git -C "$(dirname -- "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
blockers=()

head="$(git -C "$root" rev-parse --short HEAD)"
printf 'checkout: %s (%s), %s uncommitted path(s)\n' "$root" "$head" \
    "$(git -C "$root" status --porcelain | wc -l)"

if [[ -x "$root/bin/benchmarkoor" ]]; then
    built="$("$root/bin/benchmarkoor" version | awk '/commit:/ {print $2}')"
    printf 'controller: bin/benchmarkoor built from %s\n' "$built"
    [[ "$built" == "$head" ]] || blockers+=("bin/benchmarkoor is from $built, checkout is $head: run build.sh")
else
    blockers+=("bin/benchmarkoor missing: run build.sh")
fi

if docker info >/dev/null 2>&1; then
    printf 'docker: reachable\n'
    if analyzer="$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-analyzer:verify 2>/dev/null)"; then
        printf 'analyzer image: %s\n' "$analyzer"
    else
        blockers+=("benchmarkoor-compute-analyzer:verify missing: run build.sh")
    fi
    # Compute containers pin the timed CPU; another campaign's samples would be
    # perturbed, and this run's evidence would share a core with them.
    busy="$(docker ps --format '{{.Names}}' | grep -E '^benchmarkoor-compute-(worker|analyze)-' || true)"
    if [[ -n "$busy" ]]; then
        printf 'busy compute containers:\n%s\n' "$busy"
        blockers+=("a compute container is running; wait for it to exit, never stop one you did not start")
    else
        printf 'compute containers: none running\n'
    fi
else
    blockers+=("docker daemon unreachable")
fi

if ((${#blockers[@]})); then
    printf 'NOT READY:\n'
    printf '  - %s\n' "${blockers[@]}"
    exit 1
fi
printf 'READY\n'
