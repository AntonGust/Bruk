# Sprint 2 — Instructor Answer Key

> **Instructor-only.** Concrete commands + manifests for every checkpoint of the confidential-serving
> sprint, so you can unblock a stuck team fast. Pairs with `sprint-2-confidential-serving.md`.
>
> **Confidence labels** — be honest about what's proven:
> - ✅ **VERIFIED** — run end-to-end on our reference box (`anton-bruk`, **2× H100 NVL 94 GB**, dual AMD
>   **EPYC 9224 Genoa**, Ubuntu 24.04, kernel HWE **6.17.0-35**). Copy with confidence.
> - 🟡 **PARTIAL** — the mechanism is proven and the failure modes are diagnosed on our box, but the
>   final green wasn't captured before this was written. Validate on one box and record the result here.
> - 🟠 **EXPECTED** — the pre-agreed correct approach, **not yet executed** on our box. Validate before
>   turning a class loose; record fixes back here.
>
> Hardware will vary. The CC flow is validated on **H100** and applies equally to **RTX PRO 6000
> Blackwell SE** (both SPT-validated); treat addresses/ids/sizes as placeholders. Our box's GPUs are at
> `21:00.0` and `81:00.0`, device id `10de:2321`.
>
> **Meta-teaching note:** this sprint is where the platform stops being copy-paste. Several steps failed
> the first time *for us*; the answer key includes the failures on purpose. The skill being taught is
> **diagnosing a confidential stack**, so let teams struggle a little before you hand them the fix.

---

## Day 1 — SEV-SNP on the host + stack rebuild + baseline ✅ VERIFIED

### BIOS/CBS — the SNP set (BMC/console; not over SSH)
`AMD CBS →`:
- **SMEE = Enabled**.
- `CPU Common Options →` **SEV-ES ASID Space Limit Control = Manual**, **SEV-ES ASID Space Limit = 100**
  (any N>0; splits the ASID pool — without it `dmesg` shows `SEV-SNP disabled (ASIDs 0-0)`).
- `NBIO Common Options →` **SEV-SNP Support = Enabled**.
- `NBIO Common Options →` **SNP Memory (RMP Table) Coverage = Enabled**. ⚠️ **THE GOTCHA.** `SEV-SNP
  Support` alone does not reserve the RMP table. Symptom if missed: `SEV-SNP: Memory for the RMP table
  has not been reserved by BIOS` in dmesg, SNP stays off, base SEV/ES still work.
- Confirm from Sprint 1: **IOMMU = Enabled**, and cmdline has **no `iommu=pt`** (it blocks SNP).

> Our verified end-state (2026-06-30): `kvm_amd: SEV-SNP enabled (ASIDs 1-99)`, `SEV-ES enabled (ASIDs
> 1-99)`, `SEV enabled (ASIDs 100-1006)`, `sev_snp=Y`, `AMD-Vi: IOMMU SNP support enabled`, ccp
> `SEV-SNP API:1.55`. Full recipe: `docs/h100-bios-cbs-checklist.md`.

### ⚠️ The slow reboot (warn every team)
After the CBS change, the first (and every subsequent) reboot takes **20-30+ min** — the PSP initializes
the RMP table over all RAM during POST, before the OS. SSH shows `Connection refused` the whole time.
**Verified on our box:** OS boot itself is ~4 min (`systemd-analyze`); the extra ~22 min is pure
firmware POST, confirmed structural (every post-CBS reboot: 15-48 min; the one pre-CBS reboot: 1m43s).
Scales with RAM (our box has 501 GiB). **Tell teams: budget 30 min, don't power-cycle a "hung" box.**

### Checkpoint 1 verifier
```bash
sudo dmesg | grep -iE "SEV-SNP enabled|IOMMU SNP"     # expect "SEV-SNP enabled (ASIDs ...)" + "IOMMU SNP support enabled"
cat /sys/module/kvm_amd/parameters/sev_snp            # Y
uname -r                                               # >= 6.11 for SNP host (ours: 6.17.0-35-generic)
```
Then rebuild the Sprint-1 stack (k3s + Cilium + GPU Operator + kata-deploy — identical to Sprint 1;
pin the same versions: k3s `v1.35.5+k3s1`, Cilium `1.19.5`, GPU Operator `v26.3.2`, kata-deploy
`3.29.0`) and stand up the **non-CC** vLLM to record a baseline.

> **H100 (sm_90) manifest deltas vs Sprint-1 RTX PRO 6000 (sm_120)** — see
> `manifests/h100-vllm.yaml` and ADR-0005: `FLASH_ATTN` now resolves to **FlashAttention-3** (native,
> not the sm_120 FlashInfer workaround); VRAM is 94 GB (H100 NVL) so re-confirm
> `--gpu-memory-utilization=0.90`. **Verified non-CC baseline: ~100 tok/s** warm (400-tok request),
> vs ~33.8 on the RTX PRO 6000. First cold request read 4.4 tok/s — CUDA-graph warmup, ignore it; use a
> warm ≥200-token request.

---

## Day 2 — Bare confidential guest + PSP attestation report ✅ VERIFIED

### `snp-probe.yaml` (privileged so we can reach /dev/sev-guest)
```yaml
apiVersion: v1
kind: Pod
metadata: { name: snp-probe, labels: { app: snp-probe } }
spec:
  runtimeClassName: kata-qemu-snp          # confidential guest (confidential_guest=true, CoCo image)
  restartPolicy: Never
  containers:
    - name: probe
      image: busybox:1.36
      command: ["sleep","3600"]
      securityContext: { privileged: true }   # unprivileged containers don't get /dev/sev-guest
      resources:
        limits:   { memory: 2Gi, cpu: "2" }
        requests: { memory: 2Gi, cpu: "1" }
```
```bash
kubectl apply -f snp-probe.yaml
kubectl wait --for=condition=Ready pod/snp-probe --timeout=120s   # booted in ~20s on our box
```

### Prove it's a confidential guest
```bash
kubectl exec snp-probe -- sh -c 'dmesg | grep -iE "Memory Encryption|VMPL|sev-guest|confidential"'
# VERIFIED output:
#   Memory Encryption Features active: AMD SEV SEV-ES SEV-SNP
#   SEV: SNP running at VMPL0.
#   sev-guest sev-guest: Initialized SEV guest driver (using VMPCK0 communication key)
#   systemd[1]: Detected confidential virtualization sev-snp
```

### The /dev/sev-guest gotcha (device numbers)
`/dev/sev-guest` is absent in the container even when privileged. It's a **misc** device: **major 10**,
minor from `/proc/misc`. On our box `/proc/misc` lists `257 sev-guest` — **257 is the MINOR, not the
major** (easy to misread and `mknod c 257 0`, which fails with `No such device or address`).
```bash
kubectl exec snp-probe -- sh -c 'grep sev-guest /proc/misc'          # e.g. "257 sev-guest"
kubectl exec snp-probe -- sh -c 'mknod /dev/sev-guest c 10 257 && ls -l /dev/sev-guest'   # major 10, minor 257
```

### Pull + display the report (snpguest)
```bash
# snpguest static binary from virtee/snpguest releases (we used v0.10.0):
kubectl exec snp-probe -- sh -c '
  wget -qO /tmp/snpguest https://github.com/virtee/snpguest/releases/download/v0.10.0/snpguest &&
  chmod +x /tmp/snpguest &&
  dd if=/dev/urandom of=/tmp/report_data.bin bs=1 count=64 2>/dev/null &&
  /tmp/snpguest report /tmp/report.bin /tmp/report_data.bin &&
  /tmp/snpguest display report /tmp/report.bin'
```
### Checkpoint 2 (grade this)
`display report` shows: **Version 5**, a non-zero **Chip ID**, **Measurement**, **Report Data** = your
nonce, signing key **vcek**, and a non-zero **Signature R/S**. VERIFIED on our box 2026-07-01 (report
was 1184 bytes; VMPL 1; TCB microcode 88 / SNP 27 / bootloader 10; API 1.55.49). Full cryptographic
verification against AMD's cert chain is **Day 5** (deferred with GPU attestation, per ADR-0004).

---

## Day 3 — GPU(s) into CC mode → CC node ✅ VERIFIED

### The constraint (teach this before they touch anything)
`nvidia.com/cc.ready.state` is a **single node-level label**. The runtimeclasses disagree on it:
`kata-qemu-nvidia-gpu` (non-CC) requires `=false`; `kata-qemu-nvidia-gpu-snp` (CC) requires `=true`.
**⇒ a node is all-CC or all-non-CC.** CC mode itself is a **per-GPU persistent HW state** (survives
reboot), but the operator gates scheduling on that one node label.

### Supported path — flip the whole node via the operator
```bash
# Scale down ALL GPU workloads first (the operator does NOT drain them):
kubectl scale deployment vllm --replicas=0
# confirm no qemu left:  ps aux | grep '[q]emu-system'   (expect none)

# Durable flip via Helm (operator reconciles: evict operands -> set both GPUs CC-on -> reset -> reschedule):
helm upgrade gpu-operator nvidia/gpu-operator --version v26.3.2 -n gpu-operator \
  --reuse-values --set ccManager.defaultMode=on
```
On our box this converged in **~1 minute** (no drama, because nothing was holding a GPU).

### Checkpoint 3 verifier
```bash
# per-GPU physical state (tool: git clone https://github.com/NVIDIA/gpu-admin-tools):
cd gpu-admin-tools
sudo python3 nvidia_gpu_tools.py --devices 21:00.0 --query-cc-mode 2>&1 | grep "CC mode is"   # "CC mode is on"
sudo python3 nvidia_gpu_tools.py --devices 81:00.0 --query-cc-mode 2>&1 | grep "CC mode is"   # "CC mode is on"
# node view:
kubectl get node <node> -o jsonpath='{.metadata.labels.nvidia\.com/cc\.mode\.state}/{.metadata.labels.nvidia\.com/cc\.ready\.state}'  # on/true
kubectl get clusterpolicy    # ready
```
VERIFIED 2026-07-01: both GPUs `CC mode is on`, `cc.mode.state=on cc.ready.state=true`, `pgpu: 2`,
`ClusterPolicy ready`, both GPUs still on `vfio-pci`. To revert: `--set ccManager.defaultMode=off`.

### ⚠️ The incident to warn teams about (we lived it)
The **surgical** per-GPU flip (`nvidia_gpu_tools.py --set-cc-mode=on` on one BDF) is real and works, and
is the right tool when you truly need per-device control. But if you leave the operator configured for
the *other* mode, you create a mismatch. **Any restart of `nvidia-cc-manager`** — a k3s/containerd
restart, a node reboot, a pod eviction — makes it reconcile, detect the mismatch, and **force-revert the
GPU via a live reset**, which **evicts + rebinds every GPU-operator VFIO component node-wide**. That
renumbered `/dev/vfio/devices/*`, broke a *running non-CC* pod, spawned an orphaned sandbox that
deadlocked `vfio-manager`'s unbind, and cost **~19 min of outage**. **Recovery:** delete the stuck pod →
`vfio-manager` finishes unbind/rebind → GPUs back on vfio-pci → fresh pod comes up. **Lesson for the
class: keep `ccManager.defaultMode` and the real GPU state in agreement; go all-CC via the operator.**
(Also: this is why `iommu=pt` and hand-editing operator-managed state are discouraged.)

---

## Day 4 — serve a SMALL model in the confidential guest 🟡 PARTIAL (large model blocked → ADR-0006)

> **Hard-won status (2026-07-01):** large-model (24B) confidential serving is **blocked by guest-storage
> + guest-registry logistics, not by the CC security primitives.** We chased it to ground so the class
> doesn't have to. **Scope Day 4 to a SMALL model** (proves the path); the large-model pipeline is
> ADR-0006 roadmap. Full detail: `docs/h100-bringup-status.md` (Step 3 timeline). What we learned, in
> order — this is the diagnosis ladder to teach:
> 1. `runtime-request-timeout=20m` fixes the 2-min container-create timeout (Gotcha A). ✅
> 2. But guest-pull unpacks the **~35 GB** image into a RAM tmpfs → OOM even at 96Gi (Gotcha B). Bigger
>    RAM only delays it; not the fix.
> 3. And `shared_fs="none"` means the **weights PVC can't mount at all** (Gotcha D) — the whole non-CC
>    weights model is unavailable.
> 4. A small-model smoke test (no PVC) then hit a **second** wall: the in-guest image-rs **couldn't reach
>    docker.io** (Gotcha E) while host+normal-pods could.
> The two walls (RAM storage + registry egress) are exactly why ADR-0006 says: **block-device storage +
> local registry mirror**, not guest-pull-into-RAM-from-docker.io.

### The manifests
- `manifests/h100-vllm-cc.yaml` — the 24B CC variant (runtimeClassName `kata-qemu-nvidia-gpu-snp`,
  memory 96Gi). **This one hits the wall** — keep it as the teaching artifact of what *doesn't* work yet.
- `manifests/h100-vllm-cc-smoke.yaml` — the **small-model proof-of-path**: Qwen2.5-0.5B (ungated,
  Apache-2.0), **no PVC** (weights download into the guest), `memory: 160Gi` (to fit the ~48GB
  image-in-RAM). This is the one to run for Checkpoint 4.

### Gotcha A — container start times out at ~2 min (`context deadline exceeded`)
Confidential containers are **guest-pulled**: the image is pulled + unpacked *inside* the encrypted
guest (nydus + attestation-agent), which is far slower than the host-side containerd pull the non-CC
path uses. It blows kubelet's default `runtime-request-timeout` (2 min) → `StartContainer ... context
deadline exceeded`, crash-loop. **Symptom signature:** the *sandbox VM stays up* while container-create
retries at an exact ~2-min cadence. **Fix — raise the timeout on k3s:**
```bash
# add to the k3s server ExecStart (or config), then restart k3s:
#   '--kubelet-arg=runtime-request-timeout=20m'
sudo systemctl daemon-reload && sudo systemctl restart k3s
ps -eo args | tr ' ' '\n' | grep runtime-request-timeout   # confirm active
```
> ⚠️ **Only restart k3s once the node is a consistent CC node (Day 3 done).** Restarting k3s restarts
> containerd *and* `nvidia-cc-manager`; if the operator config and GPU state disagree, this re-triggers
> the Day-3 incident. On a consistent all-CC node the cc-manager restart is a no-op (both GPUs already
> on, matches config) — safe.

### Gotcha B — `No space left on device` (exit 128, StartError) during guest CopyFile
The guest-pulled image unpacks into the guest's **RAM-backed tmpfs** (ceiling ~50% of guest RAM). The
vLLM image is **13 GiB compressed / ~35 GB unpacked** (verified: host overlayfs snapshot = 35 GB); the
guest keeps compressed + unpacked ≈ **48 GB** in tmpfs. At 32Gi it OOM'd in ~2 min; **at 96Gi (~52 GB
tmpfs) it STILL OOM'd** at ~20 min, right at the finish. **More RAM is a stopgap, not a fix** — enough
for a *small* model, but the real answer is block-device storage (ADR-0006). For the small-model smoke
test we used 160Gi (→ ~84 GB tmpfs) so the ~48 GB image fits with headroom.

### Gotcha C — `kubectl logs` is EMPTY for confidential pods (by design)
The guest's stdout is inside the TEE and not surfaced to the host. Diagnose via:
```bash
kubectl get pod -l app=vllm-cc -o json | python3 -c 'import json,sys; \
 cs=json.load(sys.stdin)["items"][0]["status"]["containerStatuses"][0]; \
 print("restarts:",cs.get("restartCount")); \
 t=cs.get("lastState",{}).get("terminated",{}); print("exit:",t.get("exitCode"),"msg:",(t.get("message") or "")[:300])'
# and host-side:
sudo journalctl --since "5 min ago" | grep -iE 'kata\[|qemu' | grep -iE 'error|space|fail'
```
The `exitCode`/`message` on `lastState.terminated` is how we found the gotchas (the QEMU
`IOMMU_IOAS_MAP failed: Bad address, PCI BAR?` / `PCI peer-to-peer ... not supported` lines are a
**red herring** — they appear identically in the *working non-CC* pod and are harmless per NVIDIA's
docs; don't chase them).

### Gotcha D — `shared_fs="none"`: the weights PVC cannot be mounted
The confidential GPU runtime (`configuration-qemu-nvidia-gpu-snp.toml`) has **`shared_fs = "none"`** — no
virtiofs (threat model + a QEMU snp-v3 bug per Kata's SNP docs; `virtio_fs_daemon` commented out). So the
non-CC path's `vllm-models` **PVC (virtiofs) simply will not mount** in the confidential guest. There is
no "share the weights in from the host" — that's the whole point of a TEE. For the sprint: small model,
**no PVC**, weights download into the guest. For real weights: ADR-0006 (block device / attested artifact).

### Gotcha E — in-guest image-rs can't reach the registry (even when host can)
The small-model smoke test failed **fast** (~34 s, not an OOM) with
`[CDH] Image Pull error: ... error sending request for url (https://index.docker.io/...)`, consistently
across retries. **Cross-check proved it's guest-specific:** from the host, `curl .../manifests/v0.11.1`
returns a clean `401` (auth challenge = reachable); a plain busybox pod (default runtime) resolves DNS
and gets `401` too; CoreDNS healthy. Only the **confidential guest's** CDH/image-rs pull fails. The
guest-pull path has its own egress/DNS and is flaky here. **Fix (ADR-0006): a local registry mirror** —
the error's own "from all mirror/mapping locations" wording is the hint that CoCo image-rs supports
registry mapping. (This, plus Gotcha B, is *why* guest-pull-from-docker.io-into-RAM is the wrong path.)

### Checkpoint 4 verifier (small model)
```bash
POD=$(kubectl get pods -l app=vllm-cc-smoke -o jsonpath='{.items[0].metadata.name}')
kubectl exec $POD -- curl -s http://localhost:8000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen-0.5b","messages":[{"role":"user","content":"In one sentence, what is confidential computing?"}]}' | jq .
```
> **If it's blocked by Gotcha B/E** (likely until ADR-0006 lands): that's an acceptable Day-4 outcome —
> grade on a correct diagnosis + the documented fix path, not a forced green. Do **not** sink the week
> brute-forcing RAM or fighting docker.io.

---

## Day 5 — Attestation (GPU + PSP verify) + perf delta ✅ VERIFIED (2026-07-02)

> Executed on the reference box 2026-07-02. The committed, re-runnable suite is
> **`manifests/attestation/`** (pod manifest + `verify-psp.sh` + `verify-gpu.sh` + `bench.py` +
> the non-CC twin manifest) — grade students against that. Real output below.

### Verify the PSP report against AMD's chain (`verify-psp.sh`)
- Flow: fresh 64-byte nonce → `snpguest report` → `snpguest fetch ca pem <dir> genoa` +
  `snpguest fetch vcek` → `snpguest verify certs` → `snpguest verify attestation` → compare
  `Report Data` to the nonce. VERIFIED output:
  ```
  The AMD ARK was self-signed!
  The AMD ASK was signed by the AMD ARK!
  The VCEK was signed by the AMD ASK!
  Reported TCB Boot Loader from certificate matches the attestation report.
  Reported TCB TEE from certificate matches the attestation report.
  Reported TCB SNP from certificate matches the attestation report.
  Reported TCB Microcode from certificate matches the attestation report.
  VEK signed the Attestation Report!
  ```
  Report Data == our nonce, byte-identical (freshness — not a replay).
- **Gotcha (will hit):** busybox has **no CA certificate bundle**, so `snpguest fetch` fails TLS to
  `kdsintf.amd.com` with "self-signed certificate in certificate chain" (an empty trust store makes
  *every* chain look untrusted — nothing is wrong with KDS). Fix: `kubectl cp` the host's
  `/etc/ssl/certs/ca-certificates.crt` into the pod and `export SSL_CERT_FILE=…`.

### GPU device attestation (nvtrust — `verify-gpu.sh`)
- Run inside the running CC vLLM pod: `pip install nv-local-gpu-verifier`, then
  `python3 -m verifier.cc_admin --allow_hold_cert`. VERIFIED result (driver 590.48.01, VBIOS
  96.00.74.00.11): cert chain + OCSP revocation ✓, SPDM nonce ✓, report signature ✓, driver+VBIOS
  RIMs fetched from the NVIDIA RIM service and signature-verified ✓, **runtime measurements match
  golden** ✓, "GPU is in expected state", ready state READY → `GPU Attestation is Successful.`
- Cheap explicit cross-checks first: `nvidia-smi conf-compute -f` → `CC status: ON`,
  `-grs` → `ready`, `-e` → `CC Environment: PRODUCTION` (explicit beats inferring from vLLM working).
- **Gotcha (will hit):** the CC guest resolves **IPv6-first but has no IPv6 route** — pip/requests
  fail `Network is unreachable` while IPv4 egress works. Fix: pin IPv4 A records for `pypi.org`,
  `files.pythonhosted.org`, `rim.attestation.nvidia.com`, `ocsp.ndis.nvidia.com` in the pod's
  `/etc/hosts` (the script automates it).
- **Deprecation:** `nv-local-gpu-verifier` reaches EOL **2026-09-15**; the successor is NVIDIA's C++
  attestation-sdk (github.com/NVIDIA/attestation-sdk). Fine for teaching; migrate for production.

### Perf delta ✅ measured (Qwen2.5-0.5B, same image digest/args both sides, 400-tok warm)
| | non-CC | CC | overhead |
|---|---|---|---|
| single-stream mean (n=5) | 460.7 tok/s | 398.6 tok/s | **13.5 %** |
| batched ×8 aggregate | 3293.7 tok/s | 2937.1 tok/s | **10.8 %** |

- Right in the published **~12–15 %** band (Phala). **The number is the deliverable, not a regression
  to fix.** Compare **same model both sides** — never against the 24B ~100 tok/s baseline.
- The non-CC side requires flipping the node (`ccManager.defaultMode=off`, RUNBOOK §2) — **scale all
  GPU pods to 0 and confirm no qemu holds a GPU first** (the mixed-state force-reset is the classic
  ~19-min-outage trap). A clean flip is ~1 min per direction; flip back to CC and re-verify when done.

---

## Fast triage table

| Symptom | Most likely cause | Fix |
|---|---|---|
| `SEV-SNP: Memory for the RMP table has not been reserved by BIOS`; SNP off, base SEV on | RMP coverage not enabled in CBS | BMC: **NBIO → SNP Memory (RMP Table) Coverage = Enabled** (separate from SEV-SNP Support) |
| `kvm_amd: SEV-SNP disabled (ASIDs 0-0)` | ASID pool not split | BMC: SEV-ES ASID Space Limit Control=Manual, N>0 |
| Post-CBS reboot: `Connection refused` for 20-30 min | PSP RMP init over all RAM during POST (normal) | wait ~30 min; do **not** power-cycle; it recurs every reboot while SNP is on |
| `snpguest`: `unable to open /dev/sev-guest ... No such device (os error 6)` | wrong device node numbers | `mknod /dev/sev-guest c 10 257` — **major 10**, minor from `/proc/misc` |
| `/dev/sev-guest` absent in pod | unprivileged container | `securityContext.privileged: true`, then mknod |
| confidential pod: `StartContainer ... context deadline exceeded`, ~2-min crash cadence, sandbox stays up | guest-pull slower than kubelet default timeout | `--kubelet-arg=runtime-request-timeout=20m`, restart k3s (only on a consistent CC node) |
| confidential pod: exit 128 `No space left on device` during CopyFile | guest-pulled ~35 GB image overflows guest RAM tmpfs | stopgap: raise `memory` (96Gi→160Gi) for a *small* model; real fix: block-device storage (ADR-0006). NOT solved by the PVC — see next row |
| confidential pod: weights PVC won't mount / no virtiofs | `shared_fs="none"` on the CC GPU runtime (by design + QEMU snp-v3 bug) | can't host-share weights into a TEE; small model with no PVC for the sprint; block device / attested artifact (ADR-0006) for real |
| confidential pod: `[CDH] Image Pull error ... error sending request for url https://index.docker.io/...`, fast fail | in-guest image-rs can't reach registry (host+normal-pods CAN) | local registry mirror (ADR-0006); cross-check host `curl` gets `401` = reachable, so it's guest-specific |
| `kubectl logs` empty on a confidential pod | guest stdout is inside the TEE (by design) | read `lastState.terminated.message` + host `journalctl` (kata/qemu) |
| QEMU `IOMMU_IOAS_MAP failed: PCI BAR?` / `peer-to-peer ... not supported` | **red herring** — harmless iommufd P2P-BAR warning (also in the working non-CC pod) | ignore; look for the *real* error in `lastState`/journal |
| running non-CC pod dies + VFIO devices renumber after a restart | `cc-manager` force-reverting a GPU/operator-state mismatch, rebinding node-wide | make the node all-CC via `ccManager.defaultMode=on`; keep config == real state; delete stuck pod to recover |
| confidential vLLM up but CUDA init fails | CC GPU locked until attestation/ready-state | run GPU attestation (nvtrust); confirm CC ready flow, not a vLLM bug |
| confidential pod `Pending`, node-affinity/selector unmatched | `cc.ready.state` label ≠ what the `-snp` runtimeclass needs | flip node to CC via operator (Day 3); don't hand-patch and leave it |

---

## Instructor prep checklist (do this before Day 1)

- [x] **Day 1 (SNP host) + Day 2 (bare guest + PSP report) + Day 3 (CC node)** — VERIFIED on the
      reference box 2026-06-30/07-01. Re-validate on a student-class box if CPU/board differs.
- [ ] **Day 4 (small-model CC serving)** — validate the small-model smoke path to green and record tok/s.
      Known walls on our box: guest-pull OOM (Gotcha B) and in-guest registry egress (Gotcha E) — you may
      need a **local registry mirror** (ADR-0006) before it goes green. Do **not** target 24B; that's
      roadmap. Capture the same-model CC-vs-non-CC perf delta.
- [x] **Day 5 (attestation)** — VERIFIED on the reference box 2026-07-02; real output pasted in the
      Day-5 section above; re-runnable suite in `manifests/attestation/`.
- [ ] **Read `docs/adr/0006-confidential-weights-delivery-storage.md`** — it's the "why Day 4 is scoped to
      a small model" backing; the large-model image/weights pipeline (block device + mirror + KBS) is a
      platform sub-project, not a sprint task.
- [ ] **Confirm each class box's CBS has BOTH `SEV-SNP Support` AND `SNP Memory (RMP Table) Coverage`** —
      the #1 Day-1 wall, and it looks "almost working" (base SEV up) when only the first is set.
- [ ] **Warn every team about the 20-30 min post-SNP reboot** up front, or you'll field "my box is
      hung" tickets that are just RMP init.
- [ ] **Pin versions** exactly as Sprint 1 (k3s/Cilium/GPU Operator/kata-deploy) + `snpguest` v0.10.0 +
      a fixed `NVIDIA/gpu-admin-tools` checkout — float = divergent, silent CC failures.
- [ ] **Stand up a local registry mirror** (strongly recommended, not just for bandwidth): confidential
      guest-pull re-pulls the ~13 GiB image *into each guest* from docker.io and we saw it fail from
      inside the guest (Gotcha E). A mirror also dodges Docker Hub rate limits across N teams.
- [ ] **Decide the CC-flip policy:** teams go all-CC via the operator (recommended, safe) — do **not**
      let them hand-flip one GPU and leave the operator mismatched (the Day-3 incident).
- [ ] Confirm class GPUs are **CC-capable** (H100 or RTX PRO 6000 Blackwell SE; not consumer cards).
