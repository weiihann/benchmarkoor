#!/usr/bin/env bash
# Builds the controller binary and the in-tree analyzer image from this checkout.
# Usage: build.sh
set -euo pipefail
root="$(git -C "$(dirname -- "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
log="$(mktemp)"
make -C "$root" build-core >"$log" 2>&1 || { cat "$log" >&2; rm -f "$log"; exit 1; }
rm -f "$log"
docker build -q -f "$root/Dockerfile.compute-analyzer" -t benchmarkoor-compute-analyzer:verify "$root" >/dev/null

printf 'controller: %s\n' "$("$root/bin/benchmarkoor" version | head -1)"
printf 'analyzer image: %s (benchmarkoor-compute-analyzer:verify)\n' \
    "$(docker image inspect --format '{{.Id}}' benchmarkoor-compute-analyzer:verify)"
