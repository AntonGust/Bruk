# Sprint 2 — Confidential GPU Serving (SEV-SNP + CC-mode GPU + Attestation)

**Duration:** 1 week (5 working days) · **Level:** advanced · **Team size:** 1–3 per GPU box

> Parallel student track of the Bruk build, part 2. In Sprint 1 you served a model from inside a
> hardware-isolated **microVM** (Kata + VFIO GPU passthrough). Sprint 2 makes that microVM a
> **confidential** guest: its memory is encrypted by the CPU (**AMD SEV-SNP**), the GPU runs in
> **confidential-compute (CC) mode** (encrypted CPU↔GPU traffic), and you pull **cryptographic
> attestation** proving both — a report signed by AMD's silicon and by the GPU's own device keys.
>
> This is the actual differentiator of a sovereign inference platform: **the operator of the machine
> cannot read the model weights or the user's data**, and a remote party can *prove* that before
> sending either.
>
> Read first: `docs/adr/0004-confidential-serving-snp-spt-is-next-phase.md` (why this is the phase and
> the staged 1→5 shape), `docs/adr/0005-h100-pilot-hardware.md` (the RTX PRO 6000 → H100 move and what
> ports unchanged), `CONTEXT.md` (glossary), and your own Sprint 1 `RUNBOOK.md`.

---

## The point of this sprint

Sprint 1 proved *isolation* — the workload runs in its own kernel, with a whole GPU, under Kubernetes.
But a microVM still trusts the host: the hypervisor can read guest RAM, and PCIe traffic to the GPU is
in the clear. **Confidential computing removes that trust.** SEV-SNP encrypts and integrity-protects
guest memory against the host; NVIDIA CC mode encrypts the CPU↔GPU link and locks the GPU until it has
proven its identity. And **attestation** turns "trust us, it's confidential" into a signed, verifiable
measurement chain.

The catch — and the reason this is a whole sprint, not an afternoon — is that the confidential stack is
**bleeding-edge and fails in ways the non-CC path doesn't**: firmware that silently doesn't reserve the
right memory, a QEMU build literally named `-snp-experimental`, container images that pull *inside* the
encrypted guest, and a GPU that won't do a single FLOP until attestation unlocks it. You will hit these.
Learning to diagnose them **is** the sprint.

## Win condition (Definition of Done)

A single `curl` returns a real chat completion from a model running inside a **SEV-SNP confidential
guest** with a **CC-mode GPU**, and you can produce **attestation evidence** for both layers — all
reproducible from **committed files**.

You must be able to show, live:
1. `dmesg` **inside the serving guest** shows `Memory Encryption Features active: AMD SEV SEV-ES
   SEV-SNP` and `SNP running at VMPL0` → it's a confidential guest, not just a microVM.
2. An **AMD-PSP attestation report** you pulled from the guest, with a nonce *you* chose in it, and its
   VCEK signature + measurement fields shown.
3. The GPU reports **CC mode ON** (`nvidia_gpu_tools.py --query-cc-mode`), and your GPU **device
   attestation** step succeeds (SPDM / nvtrust).
4. `curl .../v1/chat/completions` returns coherent tokens from a **small** model running in the
   confidential deployment — and you report its **tokens/sec vs your Sprint-1 non-CC baseline** (the cost
   of encryption). *(A small model is the target on purpose — getting a large model's image+weights into
   a confidential guest is a platform problem, not a sprint task. See Day 4 and the roadmap.)*
5. `git clone <repo> && <documented steps>` reproduces it.

Partial credit is real and explicit — see the rubric. **Reaching Day 2 (a valid PSP attestation report
from a bare confidential guest) is already a solid pass;** a fully attested confidential vLLM with a
measured perf delta is the distinction-level finish. Confidential GPU serving is genuinely hard and
version-fragile — do not treat a partial as failure.

---

## Prerequisites (have these before Day 1)

- **A working Sprint-1 setup** — the non-CC serving skeleton (k3s + Cilium + GPU Operator VFIO/Kata +
  vLLM-in-Kata) must already run. Sprint 2 builds directly on it. If you're moving to a new (e.g. H100)
  box, budget Day 1 to rebuild the Sprint-1 stack there first.
- **A CC-capable NVIDIA GPU.** Validated: **H100** (SXM or PCIe/NVL) and **RTX PRO 6000 Blackwell
  Server Edition** — both support **SPT (Single-GPU Pass-Through) CC mode**, which is exactly one GPU
  per confidential guest (matches your one-GPU-per-worker topology). Consumer cards do **not** do CC.
- **An AMD EPYC ≥ Genoa CPU** (SEV-SNP). *(An Intel box would be TDX — a different attestation flow and
  toolchain, out of scope for this sprint.)*
- **BIOS/BMC access** — you will enable SEV-SNP in firmware (SSH cannot do this).
- **Host kernel ≥ 6.11** for SEV-SNP *host* support (on 24.04 use HWE: you already did this in Sprint 1
  for the ≥6.13 iommufd requirement; HWE 6.17 covers both).
- **No fTPM / no host TPM needed.** Guest attestation (this sprint) uses the AMD-PSP + the GPU's own
  device certs — neither needs `/dev/tpm0`. *Host* attestation (proving the node's boot chain) is a
  separate, later thing and is explicitly **out of scope**.
- Your Sprint-1 Git repo (extend it — Sprint 2 is more committed files).

---

## Day-by-day

Each day ends with a **checkpoint** you can verify yourself. Don't advance until it's green — the
confidential stack is unforgiving of a faked earlier step.

### Day 1 — Turn on SEV-SNP (host) + rebuild the stack + record your non-CC baseline

**Goal:** the host reports SEV-SNP live, your Sprint-1 serving stack runs on the CC target box, and you
have a **recorded non-CC tokens/sec** number (you need it for the Day-5 perf delta).

1. **BIOS/BMC — enable the SEV-SNP firmware set** (AMD CBS). This is *more* than Sprint 1's IOMMU:
   - **SMEE = Enabled**.
   - **SEV-ES ASID Space Limit Control = Manual**, **SEV-ES ASID Space Limit = N** (>0, e.g. 100) —
     the ASID pool must be split or SEV-ES/SNP get zero ASIDs and stay off.
   - **(NBIO Common Options) SEV-SNP Support = Enabled**.
   - ⚠️ **(NBIO) SNP Memory (RMP Table) Coverage = Enabled** — **this is the one everyone misses.**
     `SEV-SNP Support` *alone does not reserve the RMP table*; without RMP coverage the kernel logs
     `SEV-SNP: Memory for the RMP table has not been reserved by BIOS` and SNP stays **off** (base
     SEV/ES still work, which is why it looks "almost right"). Both toggles are under **NBIO**.
   - Keep **`iommu=pt` OFF** on the kernel cmdline (from Sprint 1) — it blocks SNP.
2. **Kernel ≥ 6.11** (HWE 6.17 from Sprint 1 is fine). Reboot after the CBS change.
   - ⚠️ **Expect a SLOW reboot — 20-30+ minutes** on a large-RAM box. The AMD PSP initializes the RMP
     table over *all* system RAM during firmware POST, before the OS starts. SSH will show
     `Connection refused` the whole time — this looks exactly like a hang but usually isn't. **Budget
     ~30 min before concluding it's stuck.**
3. **Rebuild the Sprint-1 stack on this box** if it isn't already here: stripped k3s + Cilium + GPU
   Operator (VFIO/Kata mode) + kata-deploy. This should be muscle memory now — lean on your Sprint-1
   `RUNBOOK.md`.
4. **Stand up the non-CC vLLM deployment and record tokens/sec.** This is your baseline. On a new GPU,
   note what changed from Sprint 1 (e.g. H100 is **sm_90**: FlashAttention-3 is native/recommended, and
   VRAM differs — re-check `--gpu-memory-utilization`). **Measure a warm ≥200-token request**, not a
   cold short one (cold requests are dominated by CUDA-graph warmup and lie).

**Checkpoint 1 (must be green to proceed):**
- `sudo dmesg | grep -i "SEV-SNP enabled"` → `kvm_amd: SEV-SNP enabled (ASIDs 1-99)` (numbers vary).
- `cat /sys/module/kvm_amd/parameters/sev_snp` → `Y`.
- `sudo dmesg | grep -i "IOMMU SNP"` → `AMD-Vi: IOMMU SNP support enabled`.
- Non-CC vLLM returns a completion; **baseline tok/s recorded** in your report.

### Day 2 — A bare confidential guest + your first attestation report

**Goal:** run a SEV-SNP confidential guest (no GPU yet) and pull a **real AMD-PSP attestation report**.
De-risk the confidential-guest mechanics before adding GPU complexity.

1. Deploy a minimal pod with `runtimeClassName: kata-qemu-snp` (the confidential runtimeclass —
   `confidential_guest = true`, CoCo guest image). No GPU request.
2. Confirm from inside it's genuinely a confidential guest (see checkpoint).
3. Pull an attestation report with **`snpguest`** (from the `virtee/snpguest` releases). The report
   takes a 64-byte **`report_data`** nonce — supply your own so you can prove freshness.

**Checkpoint 2 (a solid pass on its own):**
- Inside the guest: `dmesg | grep -iE "SEV-SNP|VMPL|sev-guest"` → shows `Memory Encryption Features
  active: AMD SEV SEV-ES SEV-SNP`, `SNP running at VMPL0`, `sev-guest: Initialized SEV guest driver`.
- `snpguest report report.bin report_data.bin` succeeds, and `snpguest display report report.bin` shows
  a non-zero **Chip ID**, a **VCEK** signature (R/S), a **Measurement**, and **your nonce** in the
  Report Data field.

> **Heads-up (two gotchas you *will* hit):** an unprivileged container does **not** get `/dev/sev-guest`
> — you need a privileged pod and to create the node yourself, and the device numbers are a trap. See
> the instructor key if you're stuck for more than a few minutes; the point is to *find* it, not suffer.

### Day 3 — Put the GPU(s) in CC mode → make it a CC node

**Goal:** the GPU is in confidential-compute mode and the node is correctly reconfigured as an
**all-CC node**.

1. **Understand the constraint first (it will save you an outage):** GPU CC mode is a **per-GPU
   persistent hardware state**, but the NVIDIA GPU Operator gates confidential pods on a **single
   node-level label** (`nvidia.com/cc.ready.state`), and the CC and non-CC runtimeclasses require
   *opposite* values of it. **So a node is either all-CC or all-non-CC — you cannot cleanly mix.**
2. **Use the operator-supported path:** set `ccManager.defaultMode=on` (Helm). The operator drains its
   own GPU components, flips **every** GPU to CC-on, resets them, and sets `cc.ready.state=true`.
   - ⚠️ **Scale down / stop all GPU workloads first.** The operator does **not** drain your pods, and if
     a workload is holding a GPU during the flip you can deadlock the reset and take an outage. Treat
     this as a maintenance window.
3. Verify both the physical GPU state and the node labels (checkpoint).

**Checkpoint 3:**
- `nvidia_gpu_tools.py --query-cc-mode` (from `NVIDIA/gpu-admin-tools`) → **`CC mode is on`** for each GPU.
- Node labels: `nvidia.com/cc.mode.state=on`, `nvidia.com/cc.ready.state=true`; `ClusterPolicy` `ready`;
  `nvidia.com/pgpu` still advertises your GPU count.

> ⚠️ **Do not hand-flip one GPU while leaving the operator configured for the other mode.** It "works"
> until anything restarts `nvidia-cc-manager` (a k3s/containerd restart, a reboot, a pod eviction) —
> then it force-reverts your GPU *and* rebinds every GPU-operator VFIO component node-wide, which can
> break unrelated running workloads. We took a ~19-minute outage learning this. Keep the operator's
> config and the real GPU state **in agreement**.

### Day 4 — Serve a SMALL model inside the confidential guest

**Goal:** prove the *capability* — a model actually serving inside a SEV-SNP guest with a CC-mode GPU.
**Use a small model on purpose.** (Getting a *large* model's image+weights into a confidential guest is
a genuine platform problem — read the box below before you burn a day fighting it.)

1. Copy your working vLLM manifest to a CC variant: change `runtimeClassName` to
   **`kata-qemu-nvidia-gpu-snp`**, and swap in a **small, ungated, permissive model** (e.g.
   `Qwen2.5-0.5B-Instruct`). Drop the PVC (see the storage box).
2. Deploy it and get it to `Ready`. The confidential-container differences that bite:
   - **Confidential containers are *guest-pulled*** — the image is pulled and unpacked **inside the
     encrypted guest**, not on the host + shared in. Slower, and it consumes **guest RAM** (unpacks into
     a RAM-backed filesystem). Raise the kubelet container-start timeout and give the guest lots of RAM.
   - **`kubectl logs` is empty for confidential pods** — the guest's stdout is inside the TEE by design.
     Diagnose via the container's `lastState` (`exitCode`/`message`) and host kata/qemu logs instead.

> ### ⚠️ Why "small model," and the wall behind it (the honest part)
> On our reference box, serving the **24B** model confidentially hit a wall that is **architecture, not
> tuning** — and it's the real lesson of Day 4:
> - **The confidential GPU runtime is `shared_fs = "none"`** (no virtiofs — threat model + a QEMU snp-v3
>   bug). So the **weights PVC from Sprint 1 cannot be mounted.** The "host holds plaintext weights and
>   shares them in" model is *structurally illegal* in a TEE.
> - **The image guest-pulls into RAM.** The vLLM image is ~13 GiB compressed / **~35 GB unpacked**; it
>   overflows the guest's RAM tmpfs unless you throw 100 GB+ of RAM at it, and it re-pulls every start.
> - Net: a big model's image+weights simply don't fit the RAM-backed guest-pull path.
>
> **The production answer (ADR-0006) is not "more RAM":** put image+weights on an **attached block
> device** (encrypted or dm-verity, on NVMe), pull the image from a **local registry mirror** (not
> docker.io per-start), and — for at-rest weight confidentiality — release the decryption key only to an
> attested guest (KBS/Trustee). That's a **platform sub-project**, deliberately **out of scope for this
> week**. Day 4's job is to prove the *path* with a small model; the large-model storage pipeline is the
> roadmap (see "Stretch / roadmap").

**Checkpoint 4 (the win):** `curl .../v1/chat/completions` against the confidential (small-model)
deployment returns a coherent completion. *(If even the small model is blocked by guest-pull/registry
issues on your box, documenting the exact wall + the ADR-0006 fix path earns most of the credit — this
is a known-hard area.)*

### Day 5 — Attestation (GPU + PSP verify) + perf delta + document

**Goal:** prove — cryptographically — that the confidential path is real, quantify its cost, and make
it reproducible.

1. **GPU device attestation.** Use NVIDIA's attestation tooling (nvtrust / the local verifier /
   `nvidia_gpu_tools.py`) to confirm the GPU is genuinely in CC mode and passes its SPDM / device-cert
   check. On many stacks the GPU stays **locked (no CUDA) until attestation completes** — so a working
   vLLM is itself partial evidence; make the check explicit.
2. **Verify the PSP report** from Day 2 against AMD's cert chain (VCEK → ASK → ARK via AMD KDS). Show it
   *validates*, not just that it parses.
3. **Perf delta.** Measure the confidential (small-model) deployment's tokens/sec and compare to a
   non-CC run of the **same small model** (a fair apples-to-apples baseline — don't compare a 0.5B CC run
   to your 24B non-CC number). Encrypted CPU↔GPU (bounce buffers) has a real cost — **quantifying it is
   the deliverable, not "fixing" it.** Published reference: ~12% overhead on a 7B, ~15% on a 70B — expect
   a usable-rate cost, not a cliff.
4. **Everything declarative in Git**, write **`CC-SPIKE-REPORT.md`**, a short decision log, and demo the
   five win-condition proofs live. 10-minute retro.

---

## Deliverables (what you hand in)

| Artifact | What it must contain |
|---|---|
| **Git repo** | SNP CBS notes, all CC manifests (`kata-qemu-snp` probe, `kata-qemu-nvidia-gpu-snp` vLLM), the CC-mode-flip procedure, any k3s/operator config changes. Reproducible. |
| `RUNBOOK-CC.md` | Bare-box → confidential serving, step by step. Another team must be able to follow it. |
| `CC-SPIKE-REPORT.md` | The 5 win-condition proofs (guest `dmesg`, PSP report, GPU CC + attestation, confidential completion, **CC vs non-CC tok/s**) + a "what broke / how we fixed it" section. |
| Attestation evidence | The saved PSP report (+ its verification output) and the GPU attestation result. |
| Decision log | 3–6 ADR-style bullets (e.g. surgical vs operator CC flip, guest memory sizing, model/weight choices). |
| Live demo | The 5 proofs, run in front of the group. |

---

## Grading rubric (100 pts)

| Band | Pts | Bar |
|---|---|---|
| SNP host + baseline | 20 | Checkpoint 1: `SEV-SNP enabled` on the host, non-CC vLLM serving, baseline tok/s recorded. |
| Confidential guest + PSP report | 25 | Checkpoint 2: a bare `kata-qemu-snp` guest proven confidential (`dmesg`) **and** a valid PSP report with your nonce. |
| CC node + confidential serving | 30 | Checkpoint 3 + 4: GPU(s) in CC mode, node consistent, and a **small** model returns real tokens from the `-snp` deployment. **Full marks also for** getting blocked on the guest-pull/storage wall (Day 4 box) *and* correctly documenting it + the ADR-0006 fix path — this area is known-hard and diagnosing it is the skill. |
| Attestation + perf delta | 15 | Day 5: GPU device attestation succeeds, PSP report verified against AMD's chain, CC-vs-non-CC tok/s measured (same small model both sides). |
| Reproducibility + communication | 10 | Fresh team can rebuild from committed files + `RUNBOOK-CC.md`; clear report, decision log, demo. |

Pass = 60. **Reaching "Confidential guest + PSP report" (45 pts) is the intended floor for a successful
week** — that alone proves you can stand up and attest a confidential TEE. Serving a small model in the
CC guest is the stretch that separates strong teams; a measured perf delta is the distinction. **Do not
target large-model confidential serving** — that's a platform sub-project (see the Day 4 box + roadmap),
not a sprint deliverable, and chasing it will sink your week.

---

## Known pitfalls (we hit these — learn from our scars)

- **`SEV-SNP Support = Enabled` is not enough.** Without **SNP Memory (RMP Table) Coverage = Enabled**
  (a separate NBIO toggle), the kernel logs `SEV-SNP: Memory for the RMP table has not been reserved by
  BIOS` and SNP stays off while base SEV still works — so it *looks* almost right. Enable both.
- **The post-SNP reboot is brutally slow (20-30+ min) and looks like a hang.** The PSP scrubs/initializes
  the RMP table over all RAM during POST, before the OS. `Connection refused` on SSH the whole time is
  normal. Don't declare it dead before ~30 min. This recurs on **every** reboot while SNP is on.
- **`/dev/sev-guest` isn't in an unprivileged container**, and its device numbers are a trap: it's a
  **misc** device → **major 10**, and the number in `/proc/misc` (e.g. `257`) is the **minor**. Use a
  privileged pod and `mknod /dev/sev-guest c 10 257` (verify the minor from `/proc/misc`).
- **You cannot mix CC and non-CC GPUs on one node** with this operator — `cc.ready.state` is a single
  node label the two runtimeclasses disagree on. Make the node all-CC. And **keep the operator's
  `ccManager.defaultMode` in agreement with the real GPU state**, or a `cc-manager` restart will
  force-revert your GPU and rebind VFIO node-wide (we lost ~19 min of a live service to this).
- **Confidential containers guest-pull the image into guest RAM.** The vLLM image is ~13 GiB compressed
  / **~35 GB unpacked**; guest-pull keeps both in the guest's RAM-backed tmpfs → `No space left on
  device` at container start unless the guest has 100 GB+ RAM (and it re-pulls every start). More RAM is
  a stopgap for a *small* model, not the real fix (that's block-device storage — ADR-0006). Also **raise
  the kubelet container-start timeout** (guest-pull blows the ~2-min default).
- **`shared_fs = "none"` on the confidential GPU runtime → your weights PVC won't mount.** No virtiofs
  (threat model + a QEMU snp-v3 bug). The Sprint-1 "host PVC of weights, virtiofs-shared" pattern is
  *structurally* unavailable in a TEE. For the sprint, use a small model with no PVC (weights download
  into the guest). For real weights, that's the ADR-0006 block-device / attested-artifact roadmap.
- **In-guest image-rs may fail to reach the registry** even when the host and normal pods can (we saw
  `error sending request for url https://index.docker.io/...` from the confidential guest while the host
  got a clean `401`). The guest-pull path (CDH/image-rs) has its own egress/DNS and can be flaky; the
  robust fix is a **local registry mirror** (the error's "mirror/mapping locations" wording is the hint).
- **`kubectl logs` is empty for confidential pods — by design.** The guest's stdout is inside the TEE.
  Don't chase "no logs" as a bug; read the container `lastState` message and host `journalctl` (kata/qemu).
- **The SNP+GPU QEMU is literally `-snp-experimental`.** Expect version fragility across kernel + QEMU +
  Kata + NVIDIA CC driver + attestation libs; failures are often silent (boots, GPU quietly non-CC).
  The attestation step (Day 5) exists precisely to catch a "confidential" path that isn't.
- **A CC-mode GPU may be locked until attestation.** If CUDA init fails in an otherwise-healthy
  confidential guest, the GPU may be waiting to be unlocked by the attestation/ready-state flow — not a
  vLLM bug.

---

## Stretch / roadmap (only if Checkpoint 4 is green early)

The first two are the **large-model production path** — deliberately out of Sprint 2's required scope
(they're a platform sub-project, see the Day 4 box and `docs/adr/0006-confidential-weights-delivery-storage.md`).
Treat them as roadmap you can *start*, not finish, this week:

- **Block-device storage for image + weights** (ADR-0006): attach an encrypted (`dm-crypt`, guest-key) or
  dm-verity **virtio-blk** device on NVMe so the image/weights live on disk, not RAM tmpfs — this is what
  actually unblocks a *large* model. Pair it with a **local registry mirror** so guest-pull doesn't
  depend on docker.io per-start.
- **Attested key release for weights (KBS/Trustee):** deliver weights as an encrypted artifact whose
  decryption key is released by a Key Broker Service **only after** the guest's SEV-SNP + GPU attestation
  verifies — the full "operator can never read the weights" guarantee.
- **Bind attestation to the request path:** gate the endpoint so it only serves after a fresh PSP + GPU
  attestation verifies (a minimal "attest-then-serve" check).
- **Run both GPUs as two confidential workers** and show the node hosting two independent TEEs.
- **Automate the perf-delta harness** (batched throughput, not just single-stream).

## Safety / etiquette

- Real machines, real firmware. **Record every CBS change** so you can revert it.
- The post-SNP slow reboot is expected — coordinate reboots; don't power-cycle a box that's just doing
  RMP init.
- Never restart k3s/containerd or reboot while the operator's CC config and the real GPU state disagree.
- Don't expose the serving endpoint to the public internet during the sprint.
