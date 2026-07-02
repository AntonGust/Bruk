#!/usr/bin/env bash
# H100 GPU device attestation (nvtrust local verifier) inside the confidential guest —
# Day 5 / ADR-0004 step 4. VERIFIED on anton-bruk 2026-07-02 (driver 590.48.01, VBIOS 96.00.74.00.11).
#
# What it proves (explicitly — a working vLLM is only partial evidence):
#   - SPDM GET_MEASUREMENTS with a fresh nonce, report signature verified
#   - Device cert chain valid + revocation (OCSP) checked
#   - Driver + VBIOS RIMs fetched from NVIDIA RIM service, signatures verified,
#     runtime measurements match golden values; GPU ready state READY
#
# Runs INSIDE an existing CC vLLM pod (it has the GPU + driver userspace).
# NOTE (2026-07): nv-local-gpu-verifier is deprecated, EOL 2026-09-15 — migrate to the
# C++ attestation-sdk (github.com/NVIDIA/attestation-sdk) when we productize this check.
set -euo pipefail

POD=${1:-$(kubectl get pods -l app=vllm-cc-smoke -o jsonpath='{.items[0].metadata.name}')}
[ -n "$POD" ] || { echo "usage: $0 [cc-pod-name]"; exit 1; }
echo "Target CC pod: $POD"

echo "== 1. In-guest CC state (nvidia-smi) =="
kubectl exec "$POD" -- sh -c 'nvidia-smi conf-compute -f; nvidia-smi conf-compute -grs; nvidia-smi conf-compute -e'

echo "== 2. IPv4 pin for PyPI + NVIDIA services =="
# The CC guest has IPv4 egress but no IPv6 route; glibc resolves AAAA first and pip/requests
# then fail with 'Network is unreachable'. Pin A records in /etc/hosts (resolved fresh on the host).
HOSTS=""
for h in pypi.org files.pythonhosted.org rim.attestation.nvidia.com ocsp.ndis.nvidia.com; do
  ip=$(getent ahostsv4 "$h" | awk '{print $1; exit}')
  [ -n "$ip" ] || { echo "cannot resolve $h on host"; exit 1; }
  HOSTS="$HOSTS$ip $h\n"
done
kubectl exec "$POD" -- sh -c "printf '$HOSTS' >> /etc/hosts; tail -4 /etc/hosts"

echo "== 3. Install + run the NVIDIA local GPU verifier =="
kubectl exec "$POD" -- sh -c 'pip install --quiet --no-warn-script-location nv-local-gpu-verifier'
kubectl exec "$POD" -- sh -c 'python3 -m verifier.cc_admin --allow_hold_cert'

echo "== GPU ATTESTATION SCRIPT DONE (look for 'GPU Attestation is Successful' above) =="
