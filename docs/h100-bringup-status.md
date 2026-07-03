# H100 bring-up status (`anton-bruk`, ssh ubuntu@<build-box>)

## ✅ DONE (2026-07-02): the 24B serves CONFIDENTIALLY — ADR-0006 Part 2 executed

**Mistral-Small-3.1-24B FP8 serves inside the SEV-SNP + CC-H100 guest at 97.6 tok/s single-stream
(755 tok/s batched ×8) — ~2 % under the ~100 tok/s non-CC baseline** (the 12–15 % band measured on
the 0.5B collapses as compute dominates PCIe bounce-buffer cost). Attestation suite re-run green on
this config. Manifest: `manifests/h100-vllm-cc.yaml` (rewritten; the PVC/96Gi version it replaced
never worked — see git history).

- **Weights (~90 GB): block-encrypted emptyDir** (`emptydir_mode = "block-encrypted"`, the RFC-#247
  feature, default-on in kata 3.29.0's CoCo runtimes): plain emptyDir → sparse `disk.img` on host
  NVMe → virtio-scsi → **LUKS2 dm-crypt AEAD formatted in-guest with an ephemeral key**. Host sees
  ciphertext only. First-run HF download into it; survives *container* restarts (a crash at min 14
  resumed), lost on pod re-creation (by design). Cold start ~33 min, download-dominated.
- **HF egress from the CC guest** needs the IPv4-first fix: `/etc/gai.conf` via ConfigMap
  (`precedence ::ffff:0:0/96 100`) — the guest resolves IPv6-first with no v6 route.
- **Image store (~35 GB unpacked): on `/dev/trusted_store`** (RFC #123; volumeMode:Block PVC that
  the kata-agent LUKS2-formats in-guest and mounts over `/run/kata-containers/image` *before*
  guest-pull). Backing: LVM LVs on the empty data NVMe (`manifests/trusted-storage.yaml`).
  **Gotcha that cost a day: image-rs's default 3-way-parallel layer unpack consistently fails
  against the store** ("Failed to unpack layer to destination", ~11 s in, ANY guest RAM size,
  mirror or docker.io — while busybox, a 10 GB single-layer untar, and 293 MB/s sustained writes
  all pass). Bisected to concurrency: **`max_concurrent_layer_downloads_per_image = 1` in the
  initdata `[image]` section fixes it** (serial costs ~1 min on the LAN mirror; candidate upstream
  bug to file). Result: smoke pod runs at **32Gi** (was 160Gi), 24B at **64Gi**, image bytes on
  NVMe ciphertext.
- Full mechanism/pattern analysis + what's NOT wired at these pins (dm-verity volumes, KBS keys):
  ADR-0006 "Part 2 — EXECUTED" section.

## ✅ DONE (2026-07-02): Day-5 attestation VERIFIED + CC perf delta recorded (ADR-0004 steps 4–5)

The confidential path is now *cryptographically proven*, not just "boots in SNP". Suite committed as
`manifests/attestation/` (re-run it after any storage/runtime change — it's the regression gate).

- **PSP report verified against AMD KDS** (in a bare `kata-qemu-snp` pod, snpguest v0.10.0): fresh
  64-byte nonce → `snpguest report` → `fetch ca`/`fetch vcek` (Genoa) → **`verify certs`: ARK
  self-signed ✓, ASK←ARK ✓, VCEK←ASK ✓** → **`verify attestation`: all four TCB components match +
  "VEK signed the Attestation Report!"** → report_data == our nonce (byte-identical). Report v5,
  VMPL 1, TCB µcode 88 / SNP 27 / bootloader 10, `Debug Allowed: false`. Two real gotchas: busybox
  has **no CA bundle** (kubectl-cp the host's `ca-certificates.crt`, set `SSL_CERT_FILE`), and
  `/dev/sev-guest` still needs the privileged-mknod dance (major 10, minor 257).
- **GPU device attestation PASSED** (nvtrust `nv-local-gpu-verifier` inside the running CC vLLM pod):
  device cert chain + OCSP revocation ✓, SPDM nonce ✓, attestation-report signature ✓, **driver RIM +
  VBIOS RIM fetched from the NVIDIA RIM service and signature-verified ✓, runtime measurements ==
  golden ✓**, "GPU is in expected state", ready state READY, `CC Environment: PRODUCTION`. Driver
  590.48.01, VBIOS 96.00.74.00.11. Gotcha: the CC guest resolves **IPv6-first but has no v6 route**
  → pip/requests fail "Network is unreachable" while IPv4 works; fix = pin A records in `/etc/hosts`
  (`verify-gpu.sh` automates it). Note: `nv-local-gpu-verifier` is **deprecated, EOL 2026-09-15** —
  migrate to NVIDIA's C++ attestation-sdk when productizing.
- **Same-model CC-vs-non-CC perf delta** (Qwen2.5-0.5B, identical image digest/args/FLASH_ATTN,
  400-tok warm requests, `manifests/attestation/bench.py`):
  | | non-CC | CC (SEV-SNP + CC GPU) | overhead |
  |---|---|---|---|
  | single-stream mean | **460.7 tok/s** | **398.6 tok/s** | **13.5 %** |
  | batched ×8 aggregate | **3293.7 tok/s** | **2937.1 tok/s** | **10.8 %** |
  Right in the published ~12–15 % band (Phala) — a usable-rate cost, as ADR-0004/0006 predicted.
  Method: node flipped non-CC via `ccManager.defaultMode=off` **with all GPU pods scaled to 0 first**
  (clean flip ≈1 min/direction, no collateral), non-CC twin `manifests/attestation/vllm-qwen-noncc.yaml`,
  then flipped back to CC and the smoke pod restored + re-verified.

**ADR-0004 is now fully executed (steps 1–5).** Remaining confidential-serving work is ADR-0006
Part 2 (block storage → 24B) and, later, host attestation (TPM) / Pattern-A key release.

---

## ✅ RESOLVED (2026-07-01): reboot succeeded — GPUs live, ClusterPolicy ready
A warm reboot (`sudo reboot` over SSH) was triggered at 08:36 UTC against this doc's own advice (no
BMC/tech standby was available — proceeded anyway on explicit user instruction, accepting the risk).
Timeline:
- Box went down immediately (08:36:29). SSH then returned `Connection refused` for ~25 minutes —
  looked identical to the historical PXE-hang signature and was treated as a hang requiring
  physical/BMC recovery (see git history of this doc for that assessment).
- **It was not hung — just a very slow boot.** By 09:01 UTC (~25 min after the reboot command) it came
  back on its own: `BootCurrent: 0000` (booted from disk, boot-order fix confirmed working),
  kernel `6.17.0-35-generic`, `SEV-SNP enabled (ASIDs 1-99)`.
- **Both staged fixes are now verified live:** both H100s bound to **`vfio-pci`** (nouveau gone),
  `ClusterPolicy: ready`, `nvidia.com/pgpu: 2`, GPU Operator pods all Running, `sev-snp.amd.com/esids: 99`
  exposed as node capacity.
- **Root cause CONFIRMED (2026-07-01):** the delay is 100% firmware POST, not OS boot. `systemd-analyze`
  shows kernel+userspace boot = 3m50s total; shutdown took ~3min; but the gap between shutdown
  completing and the new kernel's first timestamp was **22 minutes**, all before any OS code runs.
  `dmesg` shows the SEV-SNP RMP table is reserved at kernel timestamp `[0.000000]` — the AMD PSP does
  RMP table init in firmware *before* OS handoff, and this box has **501 GiB RAM**. RMP init time scales
  with RAM size and is a known cost of SEV-SNP on large-memory AMD EPYC systems.
  `journalctl --list-boots` history confirms it's structural, not a fluke: the one reboot transition
  *before* the SEV-SNP/RMP-Table-Coverage BIOS pass (kernel 6.8→6.17 upgrade) took **1m43s**; every
  transition *since* that BIOS pass has taken **15-48 minutes** (3 data points).
- **This WILL happen on every future reboot** as long as SEV-SNP + RMP Memory Table Coverage stay
  enabled (required for the confidential-serving goal — not something to disable). **Budget 20-30+ min
  for any reboot on this box** (kernel updates, config changes, recovery from hangs, etc.) before
  concluding it's actually stuck and needs physical/BMC recovery.

---

Snapshot of the 2× H100 NVL box bring-up as of 2026-06-30 (pre-reboot baseline), now confirmed active
by the reboot above.

## Hardware / firmware foundation ✅
- **2× H100 NVL (H100L 94 GB, sm_90), PCIe** at `21:00.0` / `81:00.0`; **dual 2× AMD EPYC 9224** (Genoa).
- **Kernel HWE 6.17.0-35** (SEV-SNP host support + iommufd GPU passthrough; 6.8 retained in GRUB).
- **SEV-SNP LIVE**: `kvm_amd: SEV-SNP enabled (ASIDs 1-99)`, RMP reserved, IOMMU SNP support. BIOS recipe
  + the RMP-Table-Coverage gotcha: `docs/h100-bios-cbs-checklist.md`.
- IOMMU on (78 groups); 2× 3.5 TB NVMe (~3.3 TB free). **No fTPM** — discrete TPM 2.0 module on order
  (host attestation parked; does not block confidential serving).

## Serving stack installed ✅ (#4 — pinned versions, reproduced from the instructor answer-key)
- **k3s `v1.35.5+k3s1`** stripped (`--flannel-backend=none --disable-network-policy --disable=traefik
  --disable=servicelb --cluster-init`) → node `Ready`, containerd `2.2.3-k3s1`. `KUBECONFIG=~/.kube/config`.
- **Cilium `1.19.5`** (`operator.replicas=1`) → kube-system all Running, NET OK.
- **GPU Operator `v26.3.2`** (`sandboxWorkloads.mode=kata`, `defaultWorkload=vm-passthrough`,
  `ccManager.defaultMode=off`, toolkit env → k3s containerd-v2 template+socket).
- **kata-deploy `3.29.0`** (`k8sDistribution=k3s`, `nfd.enabled=false`) → runtimeclasses
  `kata-qemu-nvidia-gpu` / `-snp` / `-tdx`, `/opt/kata` populated.

## Staged fixes — now ACTIVE ✅ (verified 2026-07-01 post-reboot)
- **#3 boot-order fix** — `efibootmgr -o 0000,0002,0003` (disk-first; PXE entries kept). Root cause of
  the earlier "stuck boots" was **PXE-first order** (MAAS leftover) hanging on the dead MAAS server.
  **Verified:** `BootCurrent: 0000` post-reboot — booted from disk. Restore PXE-first with
  `efibootmgr -o 0002,0000,0003` if MAAS needs it, or MAAS can force PXE via IPMI override.
- **#5 VFIO/nouveau** — `/etc/modprobe.d/vfio-h100.conf` (blacklist nouveau+nvidiafb, `vfio-pci
  ids=10de:2321`); vfio modules in initramfs; initrd rebuilt. Kata knobs already correct in 3.29.0
  (`vfio_mode=guest-kernel`, `cold_plug_vfio=root-port`, `pcie_root_port=8`, `hot_plug_vfio=no-port`).
  **Verified:** both `21:00.0` and `81:00.0` bound to `vfio-pci`, `ClusterPolicy: ready`,
  `nvidia.com/pgpu: 2`.

## #7 DONE ✅ (2026-07-01) — vLLM serving Mistral-Small-3.1 FP8 on H100
- `kubectl apply -f manifests/h100-vllm.yaml` + `hf-token` secret → pod `1/1 Running`, `/health` 200 OK.
- Weight download took ~24 min (1439s) — HF pull of ~90GB, hit transient `Network is unreachable`
  errors in the first ~15s (Cilium datapath not yet converged for the freshly-created Kata VM netns),
  self-recovered via huggingface_hub's built-in retry.
- **Benchmark: ~100 tok/s** (400 completion tokens in 4.00s, warm request) — **~3x the RTX PRO 6000
  baseline (~33.8 tok/s)**. First (cold) request measured only 4.37 tok/s — that's CUDA graph
  warmup/JIT overhead on a short 17-token generation, not representative; use a >=200 token warm
  request for any future comparison.

## #8 IN PROGRESS (2026-07-01) — confidential serving (ADR-0004)
Following the ADR's staged, de-risked order rather than jumping straight to a combined SNP+GPU+vLLM
deploy (the ADR explicitly warns this combo tends to fail *silently* — boots fine, GPU quietly stays
non-CC):
- **Step 1 DONE ✅** — bare `kata-qemu-snp` guest (no GPU), pulled a real AMD-PSP attestation report via
  `snpguest` (from `virtee/snpguest` releases). Confirmed genuine SNP guest: `dmesg` showed
  `Memory Encryption Features active: AMD SEV SEV-ES SEV-SNP`, `SNP running at VMPL0`,
  `sev-guest: Initialized SEV guest driver`. Report had a real Chip ID + VCEK signature (R/S) + matching
  report-data nonce. (Gotcha: `/dev/sev-guest` isn't bind-mounted into an unprivileged container by
  kata-agent — needed `privileged: true` + a manual `mknod`. Also: the correct device node is
  `mknod /dev/sev-guest c 10 257` — **10 is the major** (all misc devices share major 10), **257 is the
  minor** (`/proc/misc` lists `<minor> <name>` pairs) — easy to misread as major:minor=257:0.)
  Full cryptographic chain verification against AMD's KDS is deferred to step 4 per the ADR (done
  together with GPU attestation).
- **GPU CC-mode toggle DONE ✅ — done surgically, NOT via the GPU Operator.** Researched first:
  the GPU Operator's `ccManager`/`nvidia.com/cc.mode` toggle is **node-wide, not per-GPU** (NVIDIA's own
  docs: it evicts operands, unbinds vfio-pci, resets, and explicitly says *"you must ensure no user
  workloads are running on the node before you change the mode"* — it does NOT drain workloads itself).
  With the live non-CC vLLM pod occupying `21:00.0`, using the operator toggle risked disrupting it.
  Instead: cloned `NVIDIA/gpu-admin-tools` (the `nvtrust` "host_tools/python" submodule) and used
  `nvidia_gpu_tools.py` directly against just the idle GPU's PCI BDF — this operates via raw
  MMIO/sysfs, works fine on a vfio-pci-bound device, and needs no k8s-level change:
  ```
  sudo python3 nvidia_gpu_tools.py --devices 81:00.0 --set-cc-mode=on --reset-after-cc-mode-switch
  ```
  Verified: `81:00.0` → CC mode on; `21:00.0` stayed off; the running vLLM pod stayed `1/1 Running`
  with `/health` unaffected throughout. CC mode is a per-GPU persistent hardware/firmware state
  (survives reboot) — this is a one-time flip per GPU, not something to redo each session.
- **Node scheduling gotcha (resolved):** `manifests/h100-vllm-cc.yaml`'s pod requires node label
  `nvidia.com/cc.ready.state=true` (injected by the `-snp` GPU runtimeclass), but this label is a
  **status label owned by `nvidia-cc-manager`, computed once at startup from `ccManager.defaultMode`
  (currently "off") — it does NOT poll live GPU state.** Since we set CC mode out-of-band (bypassing the
  operator), the label was stale/wrong. Confirmed via `nvidia-cc-manager` logs it only reconciles on a
  watched label change, not on a timer, so patching it directly is safe *for now*:
  `kubectl label node anton-bruk nvidia.com/cc.ready.state=true nvidia.com/cc.mode.state=on --overwrite`
  ⚠️ **Standing risk:** if `nvidia-cc-manager` ever restarts (pod eviction, upgrade, node reboot) while
  `ccManager.defaultMode` is still "off", it will re-detect the mismatch and **could force `81:00.0`
  back to CC off via a live GPU reset** — disruptive if the confidential vLLM pod is running on it at
  the time. No fix applied yet (the GPU Operator has no per-GPU declarative config); **after any future
  reboot, re-check `nvidia.com/cc.mode.state` and re-run the surgical `nvidia_gpu_tools.py` flip on
  `81:00.0` if needed, before relying on confidential serving.**
- **Step 3 — re-diagnosed, then an unrelated incident intervened (see below).** First attempt:
  `manifests/h100-vllm-cc.yaml` scheduled, sandbox booted, but `StartContainer` kept timing out
  (`context deadline exceeded`) and crash-looping. Initial suspicion — a fatal QEMU error — was WRONG:
  ```
  qemu-system-x86_64-snp-experimental: IOMMU_IOAS_MAP failed: Bad address, PCI BAR?
  0000:81:00.0: PCI peer-to-peer transactions on BARs are not supported.
  ```
  **Checked the working non-CC pod's boot log — it logs the exact same lines and boots fine.** This is
  a [known iommufd limitation](https://lore.kernel.org/qemu-devel/) (PCI P2P BAR mapping unsupported by
  the modern iommufd VFIO backend) that NVIDIA's own Kata deployment docs confirm is an **expected,
  harmless warning** in the normal case. So it's not what's fatal here. Actual pattern from
  `journalctl`: the sandbox VM stays up across 3 separate container-create attempts exactly **2 minutes
  apart** — the default kubelet `--runtime-request-timeout`. Confidential containers use a different,
  slower **guest-pull** image path (nydus + attestation-agent pulling/decrypting *inside* the encrypted
  guest, invisible to host logs) instead of the normal host-side containerd pull the non-CC pod uses —
  plausibly just needs more than 2 minutes for the ~vllm-openai image. **Fix attempted:** added
  `--kubelet-arg=runtime-request-timeout=20m` to the k3s service and restarted k3s — see incident below;
  this fix itself is unverified because the restart caused a bigger problem first.

### ⚠️ INCIDENT (2026-07-01, ~10:44–11:03 UTC): k3s restart took down the live vLLM service for ~20 min
Restarting k3s to apply the timeout fix above had a much bigger blast radius than expected, and
confirmed the "standing risk" noted above the hard way:
1. Restarting k3s restarted its embedded containerd, which **killed the running non-CC vLLM sandbox**
   (its QEMU process was gone afterward) — contrary to the assumption that existing containers survive
   a k3s service restart independently.
2. This also caused **`nvidia-cc-manager` to restart** (4th restart of that pod). On restart it re-ran
   its full startup reconciliation, found `81:00.0`'s live CC mode ("on") didn't match the configured
   `ccManager.defaultMode` ("off"), and — exactly as the standing-risk note predicted — **forced it back
   to "off" via a live GPU reset**, plus evicted/recreated `nvidia-vfio-manager` and
   `nvidia-sandbox-validator` cluster-wide.
3. That eviction+rebind cycle **renumbered `/dev/vfio/devices/*`**, so kubelet's attempt to reschedule
   the non-CC vLLM pod failed repeatedly (`lstat /dev/vfio/devices/vfio1: no such file or directory`),
   spawning a new sandbox attempt each time.
4. One of those orphaned sandbox attempts got a working device path, actually started a QEMU process
   attached to `81:00.0`, and **that live sandbox then blocked `nvidia-vfio-manager`'s "unbind all"
   init step** (`vfio-pci 0000:81:00.0: No device request channel registered, blocked until released by
   user` / `Relaying device request to user (#10, #20, #30...)` in `dmesg`) — a deadlock between the
   driver-manager trying to reset everything and a live VM refusing to give up its device.
5. **Recovery:** deleted the stuck/erroring pod to release the orphaned sandbox → `vfio-manager`
   completed its unbind/rebind → both GPUs cleanly back on `vfio-pci` → `ClusterPolicy: ready`,
   `nvidia.com/pgpu: 2` restored → a fresh vLLM pod scheduled, came up, verified serving correctly
   (inference request succeeded). **Total service disruption: ~19 minutes** (pod last healthy ~10:44,
   restored and verified ~11:03). `81:00.0` is back to CC mode "off" (cc-manager's forced revert).
   `vllm-cc` deployment left scaled to 0.

**Conclusion: running one CC GPU + one non-CC GPU simultaneously on this GPU Operator version
(`v26.3.2`, `k8s-cc-manager:v0.4.0`) is fragile in a way that goes beyond "the toggle is node-wide."**
Any restart of `nvidia-cc-manager` (pod eviction, k3s/containerd restart, node reboot, or a future
upgrade) will silently detect a mismatch against `ccManager.defaultMode` and force a live reset —
and that reset's blast radius extends to evicting/rebinding *all* GPU Operator VFIO components on the
node, which can collaterally break unrelated running workloads (as it did here). This is a stronger,
now-confirmed version of the earlier "standing risk" note, not just a theoretical concern.
**Recommendation for next attempt at step 3:** either (a) accept a scheduled maintenance window where
the non-CC service is expected to be briefly down, or (b) do the CC-mode flip and confidential-serving
test as the *only* thing running on the box (no concurrent non-CC deployment), removing the collateral-
damage risk entirely. Retrying the `runtime-request-timeout` fix's effectiveness is still open — do it
under one of those safer conditions, not against a live concurrent deployment.

### ✅ RESOLUTION (2026-07-01, ~11:12 UTC): converted the box to a CC node (both GPUs confidential)
The real fix for the incident class above is to **stop mixing CC and non-CC on one node**. Key insight:
`nvidia.com/cc.ready.state` is a **single node-level label**, and the two GPU runtimeclasses have
*opposing* requirements on it (`kata-qemu-nvidia-gpu` needs `=false`, `kata-qemu-nvidia-gpu-snp` needs
`=true`). So this operator fundamentally supports **all-CC or all-non-CC per node**, never a mix. Making
both GPUs CC makes the operator's desired state self-consistent, which eliminates the force-revert:
a `nvidia-cc-manager` restart now sees "both already on, matches config → skip" instead of resetting.
- **Steps taken:** scaled non-CC `vllm` → 0 (clean, no leftover QEMU); `helm upgrade gpu-operator
  --reuse-values --set ccManager.defaultMode=on` (durable; Helm revision 2). Operator did its
  operator-managed flip (evict operands → set both GPUs CC-on → reset → reschedule) in **~1 minute**,
  no drama (vs. the incident, because nothing was running to contend for the GPUs).
- **Verified:** both `21:00.0` & `81:00.0` physically `CC mode is on`; node `cc.mode.state=on
  cc.ready.state=true`; `nvidia.com/pgpu: 2`; `ClusterPolicy ready`; all gpu-operator pods Running;
  both GPUs on `vfio-pci`. `cc.ready.state=true` is now set **legitimately by the operator** — no more
  hand-patched stale label.
- **Consequence (accepted):** the box is now **confidential-only** — the non-CC `vllm` deployment
  (`kata-qemu-nvidia-gpu`) can no longer schedule (its `cc.ready.state=false` selector won't match).
  That's fine: confidential serving is the goal, and the non-CC baseline (~100 tok/s) is already
  recorded above for the perf comparison. To revert to non-CC, `helm upgrade --set
  ccManager.defaultMode=off` and the operator flips back.
- **Bonus:** with state now consistent, restarting k3s/containerd is safe again (cc-manager restart =
  no-op), so the `runtime-request-timeout=20m` fix (already active) can be relied on without re-triggering
  the incident.

- **Step 3 (vLLM in confidential guest) — progressing through distinct failures on the CC node:**
  1. ✅ **Timeout fix worked** — with `runtime-request-timeout=20m`, the container no longer dies at the
     2-min mark; the confidential guest-pull now has time to complete.
  2. ❌ **Then hit `No space left on device` (exit 128, `StartError`) during guest CopyFile.** Root cause:
     confidential containers are **guest-pulled** — the container image is pulled + unpacked *inside* the
     encrypted guest's RAM-backed tmpfs (to keep it confidential), NOT on host disk + virtiofs-shared
     like the non-CC path. The vLLM image is **13 GiB** compressed (~20-25GB unpacked); the guest tmpfs
     ceiling is ~50% of guest RAM; at the non-CC manifest's 32Gi (→40GB guest → ~20GB tmpfs) the image
     doesn't fit. **Also note: `kubectl logs` returns EMPTY for confidential pods** — the guest's stdout
     is inside the TEE and deliberately not surfaced to the host; diagnose via container `lastState`
     `exitCode`/`message` and host kata/qemu logs instead.
  3. **Memory bump 32Gi → 96Gi got further but still OOM'd.** At 96Gi (~52GB tmpfs) the guest-pull ran
     ~20 min and *still* hit `No space left on device` right near the finish. Root cause pinned:
     **unpacked vLLM image = 35 GB**; guest-pull keeps compressed (13GB) + unpacked (35GB) ≈ 48 GB in the
     RAM tmpfs, over the ~52GB ceiling. **More RAM is brute-force, not the answer.**
  4. **The deeper wall — `shared_fs = "none"` on the confidential GPU runtime.** No virtiofs (threat model
     + a real QEMU snp-v3 bug), so the `vllm-models` **PVC cannot be mounted** — the non-CC weights path
     doesn't exist here. Weights would have to be delivered another way entirely (block device / encrypted
     artifact). **This is the architecture fork → see ADR-0006.**
  5. **Small-model proof-of-path attempted (`manifests/h100-vllm-cc-smoke.yaml`, Qwen2.5-0.5B, 160Gi, no
     PVC) → blocked by a SECOND, independent wall:** the confidential guest's **CDH/image-rs cannot pull
     the image from docker.io** (`error sending request for url https://index.docker.io/...`), consistently
     across retries — even though the **host and normal pods reach docker.io fine** (CoreDNS healthy, only
     a normal `401` auth challenge). So guest-pull is failing from two angles at once: storage (RAM) *and*
     in-guest registry egress. **This empirically confirms ADR-0006's conclusion:** guest-pulling a large
     public image into RAM per-start is not viable — you need **block-device storage + a controlled
     registry** (local mirror or encrypted artifact; the error's own "mirror/mapping locations" wording
     confirms CoCo supports registry mapping).
  6. ✅ **RESOLVED (2026-07-01) — confidential small-model serving WORKS via a local registry mirror**
     (ADR-0006 Part 1; plan `start-with-the-mirror`). Registry-egress wall fixed with an in-cluster
     **ClusterIP registry** (`manifests/registry/`), seeded with the **digest-pinned** vLLM image; the
     in-guest image-rs is redirected to it via a **Kata `cc_init_data`** initdata carrying a `cdh.toml`
     `[image.registry_config]` mirror block. Two gotchas cracked: initdata must be **base64(gzip(toml))**
     (not plain base64), and the registry config lives **inside `cdh.toml`** — a standalone
     `registries.conf` initdata key is silently dropped (image-rs never reads `/etc/containers/`; verified
     against guest-components @ `de3f6ff`, the commit kata 3.29.0 pins). **Proven end-to-end:** confidential
     `Qwen2.5-0.5B` pod pulled all 34 image blobs + the digest-pinned manifest `sha256:d5b12dfb…` **from
     the mirror** (registry access log), booted in **~5.5 min** (vs ~20 min via docker.io), `dmesg` shows
     `AMD SEV SEV-ES SEV-SNP` active, GPU `21:00.0` in CC mode, serving at **~378 tok/s warm** (tiny model;
     an earlier 200-tok run read 60 tok/s). The guest reaches the registry **ClusterIP** (`10.43.122.156`)
     — Cilium handles ClusterIP for Kata-VM traffic — over plain HTTP (`insecure=true`), safe because the
     workload pulls **by digest**.
  - **Status: confidential *small-model* serving is DONE ✅ (capability + reliable mirror path proven).**
    Confidential *24B* serving still needs **block-device storage** (35 GB image + weights don't fit the
    RAM tmpfs) — ADR-0006 Part 2, the next effort. Attestation *verify* (Day-5 PSP/GPU) + a same-model
    CC-vs-non-CC perf delta remain as follow-ups.

## What's proven vs what's blocked (updated 2026-07-02)
- ✅ **Proven:** SEV-SNP host live; bare confidential guest + genuine AMD-PSP attestation report; both
  H100s flipped to CC mode via the operator (consistent all-CC node); non-CC vLLM baseline **~100 tok/s**;
  **confidential small-model serving end-to-end via a local registry mirror** (image guest-pulled from the
  mirror by digest, SEV-SNP + CC-GPU, ~378 tok/s warm on Qwen-0.5B); **Day-5 attestation VERIFIED**
  (PSP report validates against AMD KDS; GPU attestation passes via nvtrust; CC overhead **13.5 %**
  single-stream / **10.8 %** batched vs non-CC same-model — see top of this doc).
- ⏭️ **Remaining (logistics, not security):** confidential **large (24B)** serving needs **block-device
  storage** — the 35 GB image + tens of GB of weights don't fit the RAM tmpfs, and `shared_fs=none` kills
  the virtiofs PVC path. That's ADR-0006 **Part 2**. Eventually KBS attested key release (Pattern
  A) for at-rest weight confidentiality, and host attestation once the discrete TPM lands.

## Next
- ✅ **Local registry mirror — DONE** (ADR-0006 Part 1): `manifests/registry/` (registry + seed-job +
  initdata + build-initdata.sh); the `cc_init_data` mirror config is proven end-to-end for confidential
  guest-pull. Small-model confidential serving works.
- ✅ **Day-5 attestation verify — DONE** (2026-07-02): PSP report validated against AMD KDS, GPU
  attestation passed (nvtrust), perf delta recorded (13.5 % / 10.8 %). Suite: `manifests/attestation/`.
- **ADR-0006 Part 2 (the 24B unblock):** attach an encrypted/dm-verity **block device** for image+weights
  (off RAM) so a large model fits; then confidential 24B serving.
- **TPM** (host attestation, on the discrete TPM).

## Operational notes
- **Reboots take 20-30+ min on this box, structurally** — confirmed root cause: SEV-SNP RMP table init
  by AMD PSP firmware over 501 GiB RAM, done during POST before OS handoff (see above). This is
  permanent while SEV-SNP stays enabled, not a one-off. Don't conclude "hung, needs physical recovery"
  before ~30 min of no SSH. Still prefer **cold power-cycle via BMC** when available for the actual
  power-cycle step. `6.8.0-124` remains the GRUB fallback if 6.17 ever misbehaves.
- **MAAS left intact** (user wants it back when the MAAS server returns): cloud-init + the
  `maas_hardware_sync.timer` loop still run (latter accounts for the high idle load average) — harmless;
  PXE boot entries preserved.
