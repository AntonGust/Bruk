#!/usr/bin/env bash
# Base64-encode manifests/registry/initdata.toml for the Kata cc_init_data pod annotation.
# (ADR-0006 Part 1, plan Step 4/5.) No hand-maintained base64 blob — always regenerate from the TOML.
#
# Usage:
#   manifests/registry/build-initdata.sh                 # prints the base64 blob
#   manifests/registry/build-initdata.sh --annotation    # prints the full annotation line to paste
#
# The annotation key is io.katacontainers.config.hypervisor.cc_init_data.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
toml="$here/initdata.toml"
[ -f "$toml" ] || { echo "missing $toml" >&2; exit 1; }

# Kata expects the cc_init_data annotation to be base64(GZIP(toml)) — the shim gzip-decompresses it
# (a plain base64(toml) fails with "initdata create gzip reader error: gzip: invalid header").
# Single-line base64 (annotation value must not wrap).
b64="$(gzip -c "$toml" | { base64 -w0 2>/dev/null || base64 | tr -d '\n'; })"

if [ "${1:-}" = "--annotation" ]; then
  printf 'io.katacontainers.config.hypervisor.cc_init_data: "%s"\n' "$b64"
else
  printf '%s\n' "$b64"
fi
