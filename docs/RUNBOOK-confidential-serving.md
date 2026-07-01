# RUNBOOK — Confidential serving on the H100 box (reproduce from scratch)

Sequences the whole confidential-serving path on the Bruk H100 box (`ubuntu@77.87.121.15`, node
`anton-bruk`): from firmware to a model serving **inside a SEV-SNP guest with a CC-mode H100**, with the
container image pulled from a **local registry mirror** (so guest-pull doesn't depend on docker.io).

> **Status (2026-07-01):** small-model confidential serving is proven end-to-end via this runbook. The
> **24B** model is NOT yet servable confidentially — its 35 GB image + weights don't fit the guest RAM
> tmpfs and `shared_fs=none` blocks the virtiofs PVC; that needs block-device storage (ADR-0006 Part 2).
>
> **This is NOT all Helm.** The stack is layered: host firmware/kernel → k3s (+ a systemd flag) → Helm
> (Cilium, GPU Operator, kata-deploy, the CC-mode flip) → kubectl (registry, workloads) → a scripted
> initdata step. Follow the sections in order.

Pinned versions: k3s `v1.35.5+k3s1`, Cilium `1.19.5`, GPU Operator `v26.3.2`, kata-deploy `3.29.0`,
guest-components `de3f6ff` (what kata 3.29.0 pins), `snpguest` v0.10.0. See ADRs 0004/0005/0006.

---

## 0. Host: firmware + kernel + VFIO  (see the detailed docs; not repeated here)
- **BIOS/CBS SEV-SNP** — SMEE, SEV-ES ASID split, **(NBIO) SEV-SNP Support**, **(NBIO) SNP Memory (RMP
  Table) Coverage** ⚠️(the gotcha). Full recipe: `docs/h100-bios-cbs-checklist.md`.
- **Kernel** HWE ≥6.11 (we run 6.17); **boot-order** disk-first; **VFIO/nouveau** blacklist. Details +
  verification: `docs/h100-bringup-status.md` (Hardware/firmware + Staged-fixes sections).
- ⚠️ **Reboots take 20-30 min** on this box (PSP RMP init over 501 GiB during POST). Don't assume a hang.
- **Verify:** `sudo dmesg | grep -i "SEV-SNP enabled"` → `kvm_amd: SEV-SNP enabled (ASIDs 1-99)`;
  `cat /sys/module/kvm_amd/parameters/sev_snp` → `Y`.

## 1. Serving stack (k3s + Cilium + GPU Operator + kata-deploy)
Reproduce exactly as Sprint 1 (`docs/teaching/sprint-1-instructor-answer-key.md`, Days 2-3): stripped
k3s, Cilium (Helm), GPU Operator v26.3.2 in `sandboxWorkloads.mode=kata`, kata-deploy 3.29.0.
**Additionally**, for confidential guest-pull (slow), raise the kubelet container-start timeout on k3s:
add `'--kubelet-arg=runtime-request-timeout=20m'` to the k3s server `ExecStart`, then
`sudo systemctl daemon-reload && sudo systemctl restart k3s`.
⚠️ Only restart k3s when the node's CC state is consistent (Section 2 done) — a mismatched
`nvidia-cc-manager` restart force-resets GPUs node-wide (see the incident in `h100-bringup-status.md`).
- **Verify:** `kubectl get nodes` Ready; `kubectl get clusterpolicy` ready; `nvidia.com/pgpu: 2`.

## 2. Make it a CC node (both GPUs confidential)
The GPU Operator gates confidential pods on a single node label; a node is all-CC or all-non-CC. Flip
both GPUs via the operator (scale down any GPU workloads first):
```bash
kubectl scale deployment vllm --replicas=0            # if running; ensure no qemu holds a GPU
helm upgrade gpu-operator nvidia/gpu-operator --version v26.3.2 -n gpu-operator \
  --reuse-values --set ccManager.defaultMode=on
```
- **Verify:** node labels `nvidia.com/cc.mode.state=on` + `nvidia.com/cc.ready.state=true`; per-GPU
  `nvidia_gpu_tools.py --devices <BDF> --query-cc-mode` → `CC mode is on` (tool:
  `git clone https://github.com/NVIDIA/gpu-admin-tools`). Revert with `--set ccManager.defaultMode=off`.

## 3. Local registry mirror  (ADR-0006 Part 1)
Guest-pull happens inside the encrypted guest via image-rs and **bypasses host containerd** — so the
mirror must be configured for the in-guest image-rs, and the workload image is pulled from a local
registry instead of (flaky, rate-limited) docker.io.

```bash
# 3a. Deploy the registry (ClusterIP only — never hostNetwork/NodePort; unauth'd HTTP registry).
kubectl apply -f manifests/registry/registry.yaml
kubectl rollout status deploy/registry

# 3b. Record the vLLM image DIGEST (integrity anchor) and seed the mirror (crane Job, host-pulls docker.io).
#     The digest is already pinned in seed-job.yaml + the workload manifest; re-confirm if the tag moves:
#       crane digest docker.io/vllm/vllm-openai:v0.11.1
kubectl apply -f manifests/registry/seed-job.yaml
kubectl wait --for=condition=complete job/registry-seed --timeout=15m
```
- **Verify:** the seed Job log ends with a catalog listing `vllm/vllm-openai` + `library/busybox` and
  `vLLM digest in mirror: sha256:d5b12dfb…`.
- **Note:** seed uses image `gcr.io/go-containerregistry/crane:debug` (the `:latest`/distroless variant
  has no `/bin/sh`).

## 4. Redirect the confidential guest's image-rs to the mirror (initdata)
The mirror config is delivered via a Kata **`cc_init_data`** annotation carrying a `cdh.toml` with an
`[image.registry_config]` mirror block. Two non-obvious requirements (both baked into the files):
- The annotation value must be **`base64(gzip(toml))`** (plain base64 → `gzip: invalid header`).
- The mirror config must live in **`cdh.toml`**, NOT a standalone `registries.conf` (the latter is
  silently dropped — only `aa.toml`/`cdh.toml`/`policy.rego` are recognized initdata keys, and image-rs
  never reads `/etc/containers/registries.conf`). Verified against guest-components `de3f6ff`.

Source of truth: `manifests/registry/initdata.toml` (uses the registry Service DNS FQDN — portable;
verified the guest resolves it). Encode + inject at apply time (never hand-maintain the base64):
```bash
B64=$(bash manifests/registry/build-initdata.sh)
```

### 4a. (Optional) validate the mechanism cheaply on a bare-SNP guest first
```bash
sed "s|__INITDATA_B64__|$B64|" manifests/registry/snp-mirror-test.yaml | kubectl apply -f -
# Proof: the registry access log shows the guest GET-ing busybox (useragent oci-client), host = the mirror:
kubectl logs -l app=registry --since=2m | grep "GET .*busybox"
kubectl delete pod snp-mirror-test
```

## 5. Serve the confidential workload via the mirror
```bash
# Workload image is digest-pinned (image-rs verifies content regardless of the insecure mirror).
sed "s|__INITDATA_B64__|$B64|" manifests/h100-vllm-cc-smoke.yaml | kubectl apply -f -
kubectl rollout status deploy/vllm-cc-smoke --timeout=20m
```
- **Verify — pulled from the mirror, not docker.io:**
  `kubectl logs -l app=registry --tail=100000 | grep -c "GET /v2/vllm/vllm-openai"` → >0 (34 blobs + the
  digest-pinned manifest `sha256:d5b12dfb…`).
- **Verify — genuinely confidential:** `POD=$(kubectl get po -l app=vllm-cc-smoke -o
  jsonpath='{.items[0].metadata.name}')`; `kubectl exec $POD -- sh -c 'dmesg | grep "Memory Encryption"'`
  → `AMD SEV SEV-ES SEV-SNP`. (Note: `kubectl logs` is EMPTY for CC pods — stdout is inside the TEE; use
  `exec` and the container `lastState` for diagnosis.)
- **Verify — serves:** `kubectl exec $POD -- curl -s localhost:8000/v1/chat/completions -H
  'Content-Type: application/json' -d '{"model":"qwen-0.5b","messages":[{"role":"user","content":"hi"}]}'`
  → a completion. Reference: ~5.5 min to Ready (LAN mirror pull), ~378 tok/s warm (Qwen-0.5B).

---

## Reproducibility caveats
- **Not IaC end-to-end.** Section 0 (firmware/kernel/VFIO) is host state, not captured in any manifest.
  Sections 1-2 are Helm + a k3s systemd flag; 3-5 are kubectl + a scripted initdata step.
- **`initdata.toml` mirror location** uses the registry Service FQDN (portable). If in-guest DNS ever
  fails, swap to the ClusterIP (`kubectl get svc registry -o jsonpath='{.spec.clusterIP}'`).
- **Image digest** (`sha256:d5b12dfb…`) is pinned in `seed-job.yaml` and `h100-vllm-cc-smoke.yaml`; if the
  upstream tag is re-published, re-record it in both.
- **CC-mode is per-GPU persistent firmware state** — survives reboot. After a reboot, re-check
  `nvidia.com/cc.mode.state`; the operator restores it on startup when `ccManager.defaultMode=on`.

## Pointers
- Architecture/decisions: `docs/adr/0006-confidential-weights-delivery-storage.md` (+ 0004, 0005).
- Full chronology + gotchas: `docs/h100-bringup-status.md`.
- Teaching version of this arc: `docs/teaching/sprint-2-*.md`.
- Manifests: `manifests/registry/` (registry, seed-job, initdata + build-initdata.sh, snp-mirror-test),
  `manifests/h100-vllm-cc-smoke.yaml` (small-model, works), `manifests/h100-vllm-cc.yaml` (24B, blocked
  on block storage — kept as the artifact of what needs ADR-0006 Part 2).
