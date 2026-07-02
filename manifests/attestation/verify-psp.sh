#!/usr/bin/env bash
# PSP (SEV-SNP) attestation verification against the AMD KDS chain — Day 5 / ADR-0004 step 4.
# VERIFIED on anton-bruk 2026-07-02 (see docs/h100-bringup-status.md).
#
# What it proves (not just "parses"):
#   1. ARK is AMD's self-signed root; ASK signed by ARK; VCEK signed by ASK  (verify certs)
#   2. The VCEK signed THIS attestation report; TCB in cert matches report   (verify attestation)
#   3. The report embeds OUR fresh nonce (report_data) -> not a replay       (byte compare)
#
# Prereqs: kubectl apply -f snp-attest-pod.yaml (pod Ready); guest egress to
# github.com + kdsintf.amd.com (IPv4).
set -euo pipefail

POD=snp-attest
SNPGUEST_URL=https://github.com/virtee/snpguest/releases/download/v0.10.0/snpguest
SNPGUEST_SHA256=70e700465e3523e67dd5104583dc36cd11eef630c6f04c5b9ccafd6ba2e76ca0
PROCESSOR_MODEL=genoa   # EPYC 9224 = Genoa; adjust per box (snpguest fetch ca --help lists models)
HOST_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt

kexec() { kubectl exec "$POD" -- sh -c "$1"; }

echo "== 1. Confirm we are in a genuine SNP guest =="
kexec 'dmesg | grep -iE "Memory Encryption|VMPL|sev-guest"'

echo "== 2. Device node (misc major 10, minor from /proc/misc — 257 here) =="
kexec 'grep sev-guest /proc/misc; [ -e /dev/sev-guest ] || mknod /dev/sev-guest c 10 257; ls -l /dev/sev-guest'

echo "== 3. snpguest v0.10.0 (sha256-pinned; busybox wget does not validate TLS) =="
kexec "[ -x /tmp/snpguest ] || wget -qO /tmp/snpguest $SNPGUEST_URL
echo '$SNPGUEST_SHA256  /tmp/snpguest' | sha256sum -c -
chmod +x /tmp/snpguest && /tmp/snpguest --version"

echo "== 4. Fresh nonce -> signed report =="
kexec 'dd if=/dev/urandom of=/tmp/report_data.bin bs=64 count=1 2>/dev/null
/tmp/snpguest report /tmp/report.bin /tmp/report_data.bin'

echo "== 5. CA bundle for KDS TLS (busybox image has none — borrow the host trust store) =="
kubectl cp "$HOST_CA_BUNDLE" "$POD":/tmp/ca-certificates.crt

echo "== 6. Fetch ARK+ASK and the VCEK for the reported TCB from AMD KDS =="
kexec "export SSL_CERT_FILE=/tmp/ca-certificates.crt
mkdir -p /tmp/certs
/tmp/snpguest fetch ca pem /tmp/certs $PROCESSOR_MODEL
/tmp/snpguest fetch vcek pem /tmp/certs /tmp/report.bin
ls -l /tmp/certs"

echo "== 7. Verify chain + report signature + TCB match =="
kexec '/tmp/snpguest verify certs /tmp/certs
/tmp/snpguest verify attestation /tmp/certs /tmp/report.bin'

echo "== 8. Freshness: report_data in the SIGNED report == our nonce =="
NONCE=$(kexec 'od -A n -t x1 /tmp/report_data.bin' | tr -d ' \n')
REPORTED=$(kexec '/tmp/snpguest display report /tmp/report.bin' |
  awk '/^Report Data:/{f=1;next} f&&NF{print;n++} n==4{exit}' | tr -d ' \n' | tr 'A-F' 'a-f')
if [ "$NONCE" = "$REPORTED" ]; then
  echo "NONCE MATCH: $NONCE"
else
  echo "NONCE MISMATCH!"; echo " sent:     $NONCE"; echo " reported: $REPORTED"; exit 1
fi

echo "== PSP ATTESTATION VERIFIED (chain + signature + TCB + freshness) =="
