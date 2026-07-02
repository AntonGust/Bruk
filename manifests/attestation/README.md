# Attestation verification suite — Day 5 (ADR-0004 steps 4–5)

Proves the confidential-serving path is *cryptographically real*, not just "boots in SNP",
and records the CC-vs-non-CC perf delta. First executed successfully on `anton-bruk`
2026-07-02 (results in `docs/h100-bringup-status.md`). Re-run this suite after any change
to the storage/runtime config (e.g. ADR-0006 Part 2) — it is the regression gate for the
confidential posture.

## Contents

| File | What it does |
|---|---|
| `snp-attest-pod.yaml` | Bare `kata-qemu-snp` guest (privileged, no GPU) for PSP verification |
| `verify-psp.sh` | SNP report → AMD KDS: VCEK→ASK→ARK chain, report signature, TCB match, nonce freshness |
| `verify-gpu.sh` | nvtrust local verifier inside the CC vLLM pod: SPDM + cert chain + RIM/measurement match |
| `bench.py` | Single-stream + batched tok/s against a vLLM OpenAI endpoint |
| `vllm-qwen-noncc.yaml` | Non-CC twin of `h100-vllm-cc-smoke.yaml` (same model/args/image digest) for the delta |

## Run it

```bash
# 1. PSP attestation (CPU-side confidential guest)
kubectl apply -f snp-attest-pod.yaml
kubectl wait --for=condition=Ready pod/snp-attest --timeout=180s
./verify-psp.sh                      # expect: "PSP ATTESTATION VERIFIED"

# 2. GPU attestation (inside the running CC serving pod)
./verify-gpu.sh                      # expect: "GPU Attestation is Successful"

# 3. Perf, CC side (pod warm)
SVC=$(kubectl get svc vllm-cc-smoke-svc -o jsonpath='{.spec.clusterIP}')
python3 bench.py http://$SVC:8000 qwen-0.5b

# 4. Perf, non-CC side — requires flipping the node (RUNBOOK §2; scale GPU pods to 0 FIRST,
#    verify no qemu holds a GPU, then ccManager.defaultMode=off; revert to =on afterwards)
kubectl apply -f vllm-qwen-noncc.yaml
SVC=$(kubectl get svc vllm-qwen-noncc-svc -o jsonpath='{.spec.clusterIP}')
python3 bench.py http://$SVC:8000 qwen-0.5b
# teardown + flip back on, then re-run steps 1–3 to confirm the restored CC state
```

## Gotchas (all hit for real on 2026-07-02)

- **busybox has no CA bundle** → `snpguest fetch` fails TLS to `kdsintf.amd.com`
  ("self-signed certificate in certificate chain"). Fix: `kubectl cp` the host's
  `/etc/ssl/certs/ca-certificates.crt` in and set `SSL_CERT_FILE` (verify-psp.sh does this).
- **CC guest resolves IPv6-first but has no IPv6 route** → pip/requests die with
  `Network is unreachable` while IPv4 egress works fine. Fix: pin A records in the pod's
  `/etc/hosts` (verify-gpu.sh does this). Same fix will matter for Phase-2 HF downloads.
- `/dev/sev-guest` is misc **major 10**, minor from `/proc/misc` (257 here) — and only
  appears via `mknod` in a **privileged** container.
- `nv-local-gpu-verifier` is **deprecated, EOL 2026-09-15** → migrate to the C++
  attestation-sdk (github.com/NVIDIA/attestation-sdk) when this check is productized.
- The CC-mode flip for step 4 is the **highest-blast-radius operation on the node** —
  never with a GPU pod running (a cc-manager force-reset once cost a ~19-min outage).
  A clean flip (no GPU workloads) takes ~1 min per direction + vLLM restart time.

## Results 2026-07-02 (anton-bruk, H100 NVL, driver 590.48.01)

- **PSP:** ARK self-signed ✓, ASK←ARK ✓, VCEK←ASK ✓, TCB match ✓, VCEK signed report ✓,
  report_data = fresh nonce ✓ (report v5, VMPL 1, TCB µcode 88 / SNP 27 / bootloader 10).
- **GPU:** cert chain + OCSP ✓, SPDM nonce ✓, report signature ✓, driver+VBIOS RIM
  signatures ✓, runtime == golden measurements ✓, ready state READY, environment PRODUCTION.
- **Perf (Qwen2.5-0.5B, 400-tok warm, FLASH_ATTN, gpu-mem-util 0.30):**
  see `docs/h100-bringup-status.md` (CC single-stream mean 398.6 tok/s; delta recorded there).
