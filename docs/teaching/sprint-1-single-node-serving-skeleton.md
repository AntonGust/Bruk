# Sprint 1 — Single-Node GPU Serving Skeleton

**Duration:** 1 week (5 working days) · **Level:** intermediate · **Team size:** 1–3 per GPU box

> Parallel student track of the Bruk build. You will stand up the same **single-node serving
> skeleton** we are building on the reference box: take a bare GPU machine and end with a
> language model serving real tokens **from inside a hardware-isolated microVM** (Kata + VFIO
> GPU passthrough) on a stripped-down Kubernetes.
>
> Read first: `docs/handoff-2026-06-17.md` (the decisions and the build order),
> `CONTEXT.md` (glossary), `docs/adr/0001`, `docs/adr/0002`.

---

## The point of this sprint

Anyone can `pip install vllm` and serve a model on a host GPU. The hard, interesting thing —
and the foundation of a sovereign inference platform — is serving it with the GPU **passed
through into a confidential-compute-ready microVM**, orchestrated by Kubernetes, declared in
Git. Sprint 1 proves that path end-to-end on one node. Everything later (multi-node, the fleet
plane, attestation) builds on this skeleton.

## Win condition (Definition of Done)

A single `curl` to your cluster returns a real chat completion, where the model is running in a
**Kata microVM** with a **whole GPU bound via VFIO**, and the entire setup is reproducible from
**committed files** — no undocumented manual `kubectl` surgery.

You must be able to show, live:
1. `uname -a` **inside the serving pod** prints a *different* kernel than the host → it's a microVM, not a container on the host kernel.
2. `nvidia-smi` **inside the pod** sees the passed-through GPU.
3. `curl .../v1/chat/completions` returns coherent tokens at a sane rate (report tokens/sec).
4. `git clone <your repo> && <documented steps>` reproduces it on a fresh box.

Partial credit is real and explicit — see the rubric. Reaching Day 3 (GPU visible inside a Kata
pod) is already a solid pass; the vLLM spike is the distinction-level finish.

---

## Prerequisites (have these before Day 1)

- A GPU box you control, with **BIOS/BMC access** (you will change firmware settings).
- An NVIDIA GPU that supports your target model in FP8/FP16, ≥ 24 GB VRAM recommended.
- Ubuntu 22.04/24.04 LTS, UEFI boot, `sudo`, and a working internet connection.
- **A recent host kernel — ≥ 6.13** (on 24.04, install the HWE kernel:
  `sudo apt install --install-recommends linux-generic-hwe-24.04`). The Kata GPU runtime uses the
  modern **iommufd** VFIO backend, which calls an ioctl (`IOMMU_VDEVICE_ALLOC`) that only exists in
  kernel **6.13+**. On older kernels QEMU **segfaults** when it tries to attach the GPU (see Day 3 +
  the pitfalls). This bit us hard on a 6.8 kernel — don't start Day 3 on an old kernel.
- Comfort with: Linux shell, SSH, containers. You do **not** need prior Kubernetes or
  virtualization experience — that's what you're learning.
- A Git repo for your team (this is your deliverable; commit from Day 1).

---

## Day-by-day

Each day ends with a **checkpoint** you can verify yourself. Don't advance until it's green —
later days will not work if an earlier checkpoint is faked.

### Day 1 — Firmware + host prep (open the VFIO path)

**Goal:** the host exposes a working IOMMU and your GPU(s) are bound to `vfio-pci`, with the
NVIDIA host drivers kept *off* the cards.

1. **Inventory** (read-only first — know your machine before you touch it):
   - `lspci -nnk | grep -iA3 nvidia` → record each GPU's PCI address and device id (e.g. `10de:2bb5`).
   - `lscpu | grep -i virt`, `ls /dev/kvm`, `lsmod | grep kvm`.
2. **Enable virtualization firmware in BIOS** (this is the step that blocks everyone — do it first):
   - **IOMMU = Enabled** (AMD: `Advanced → AMD CBS → NBIO Common Options → IOMMU`; Intel: enable **VT-d**). **Required.**
   - While you're in there, also enable the confidential-compute firmware you'll need later, so you
     don't make a second BMC trip: **SEV-SNP Support = Enabled**, set **SEV-ES ASID Space Limit
     Control = Manual** with a nonzero **SEV-ES ASID Space Limit** (the ASID pool must be split or
     SEV-ES/SNP get zero ASIDs and stay off), confirm **SMEE = Enabled**, and enable **fTPM / PTT**.
3. **Set the kernel cmdline** in `/etc/default/grub` → `GRUB_CMDLINE_LINUX_DEFAULT`:
   - **AMD (our box): add nothing.** Once IOMMU is enabled in firmware (step 2), modern kernels
     bring the AMD IOMMU up automatically — VFIO needs no `iommu=` token at all.
   - **Intel: add `intel_iommu=on`** (required — VT-d's kernel IOMMU is off by default). Nothing else.
   - ⚠️ **Leave `iommu=pt` off.** It only selects IOMMU *passthrough* mode (a host-device perf knob).
     VFIO does **not** need it (VFIO builds its own IOMMU domains), and it **breaks SEV-SNP** — the
     kernel prints `AMD-Vi: SNP: IOMMU ... configured in passthrough mode, SNP cannot be supported`.
     Add it *only* on a non-confidential-compute box where you want that perf and will never enable SNP.
   - ⚠️ **Never add `amd_iommu=on`** — it is **not a valid option** on modern kernels; the kernel
     prints `AMD-Vi: Unknown option - 'on'`.
   - `sudo update-grub`, then reboot.
4. **Bind the GPU(s) to `vfio-pci` and keep nouveau/NVIDIA off them:**
   - `/etc/modprobe.d/vfio.conf`:
     ```
     options vfio-pci ids=<your-gpu-device-id>     # e.g. 10de:2bb5
     softdep nouveau pre: vfio-pci
     softdep nvidia  pre: vfio-pci
     ```
   - `/etc/modprobe.d/blacklist-nvidia-nouveau.conf`: `blacklist nouveau` and `blacklist nvidiafb`.
   - Add `vfio`, `vfio_iommu_type1`, `vfio_pci` to `/etc/initramfs-tools/modules`, then `sudo update-initramfs -u` and reboot.

**Checkpoint 1 (must be green to proceed):**
- `ls /sys/kernel/iommu_groups | wc -l` → **> 0**
- `sudo dmesg | grep -i "Found IOMMU"` (AMD) or DMAR lines (Intel) → present
- For each GPU: `lspci -nnks <addr> | grep "driver in use"` → **`vfio-pci`**
- `lsmod | grep nouveau` → **empty** (nouveau did not grab the card)
- **IOMMU group isolation:** your GPU should be **alone** in its IOMMU group (or share only with its own audio/PCI functions). Check the members of `/sys/kernel/iommu_groups/<n>/devices/`. If unrelated devices share the group, passthrough will fail or be unsafe — note it and ask an instructor before forcing ACS overrides.

### Day 2 — Kubernetes platform base (stripped k3s + Cilium)

**Goal:** a healthy single-node cluster that is *effectively vanilla* — we strip k3s's batteries so
it doesn't fight the CNI and runtime choices we make later.

1. Install **k3s** with the bundled networking/ingress disabled:
   ```
   curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--flannel-backend=none \
     --disable-network-policy --disable=traefik --disable=servicelb --cluster-init" sh -
   ```
2. Install **Helm**, then install **Cilium** as the CNI (Helm chart). Wait for it to converge.
3. Sanity: `kubectl get nodes`, `kubectl get pods -A`, `cilium status` (or pod readiness).

**Checkpoint 2:** node is `Ready`, all `kube-system`/Cilium pods `Running`, and a throwaway
`kubectl run test --image=busybox --rm -it -- echo hi` works (pod schedules + networks).

> Capture every command/manifest you used in your repo as you go — Day 5 grades reproducibility,
> not memory.

### Day 3 — GPU Operator in VFIO/Kata mode (GPU into a microVM)

**Goal:** install the NVIDIA GPU Operator configured for **VFIO + Kata**, get the
`kata-qemu-nvidia-gpu` runtimeClass, and prove a Kata pod can **see the GPU**.

1. Install the **NVIDIA GPU Operator** with `vfio-manager` + `kata-manager` enabled (VFIO/Kata
   mode — *not* the default host-driver mode). This installs the Kata runtime and the
   `kata-qemu-nvidia-gpu` runtimeClass and advertises `nvidia.com/pgpu`.
2. Deploy a minimal **probe pod**: `runtimeClassName: kata-qemu-nvidia-gpu`, request
   `nvidia.com/pgpu: 1`, image with `nvidia-smi`.

**Checkpoint 3 (this is the real technical milestone of the week):**
- `kubectl exec` into the probe pod → `uname -a` shows a **microVM guest kernel** (differs from `uname -a` on the host).
- `nvidia-smi` inside the pod **lists your GPU**.

If Checkpoint 3 is green, you have proven hardware-isolated GPU passthrough under Kubernetes —
the foundation everything else needs.

> **If the probe pod hangs `ContainerCreating` → `FailedCreatePodSandBox ... create container
> timeout`, and the journal shows `qemu-system-x86_64: segfault`:** first check your **kernel
> version** (`uname -r`). On kernels **< 6.13** the Kata iommufd GPU path crashes QEMU on attach —
> upgrade the kernel (see Prerequisites) and reboot. This is the single most common Day 3 wall, and
> it is *not* about your GPU's size. The instructor key has the full diagnosis (gdb/strace) if you
> want to see how the real error was found behind the bare "segfault".

### Day 4 — The go/no-go spike (serve real tokens from inside the microVM)

**Goal:** replace the probe with **vLLM** serving an open model, in the same Kata+VFIO pod.

1. Model: **Mistral-Small-3.1 (FP8)** — Apache-2.0, European-origin, single-GPU friendly. (If
   your GPU is small, pick a smaller Apache-2.0 model; record your choice and why.)
2. Deploy vLLM as a Kata pod (`runtimeClassName: kata-qemu-nvidia-gpu`, `nvidia.com/pgpu: 1`),
   exposing the OpenAI-compatible API. Stage weights the simplest way that works for now
   (a host path / PVC is fine for Sprint 1 — the signed-OCI-artifact path comes later).
3. `curl http://<svc>/v1/chat/completions` with a real prompt.

**Checkpoint 4 (the win condition):** a coherent completion comes back. Record **tokens/sec** and
the exact request/response in your spike report.

### Day 5 — Harden, document, demo

**Goal:** turn the working pile into something reproducible and explainable.

1. **Everything declarative in Git.** No setup should exist only in your shell history. A teammate
   should be able to follow your `RUNBOOK.md` on a fresh box.
2. Write **`SPIKE-REPORT.md`**: the four win-condition proofs (host vs guest `uname`, in-guest
   `nvidia-smi`, sample completion, tokens/sec), what broke, and how you fixed it.
3. Write a short **decision log** (3–6 bullets): the real choices you made (model, weight staging,
   any deviations) and why. Mirror the ADR style in `docs/adr/`.
4. **Demo (live) + retro.** Show the four proofs running. 10-minute retro: where did you lose time?

---

## Deliverables (what you hand in)

| Artifact | What it must contain |
|---|---|
| **Git repo** | All host config (vfio.conf, grub notes), k3s/Cilium install steps, GPU Operator values, all k8s manifests. Reproducible. |
| `RUNBOOK.md` | Step-by-step to rebuild from a bare box. Another team must be able to follow it. |
| `SPIKE-REPORT.md` | The 4 win-condition proofs + tokens/sec + a "what broke / how we fixed it" section. |
| Decision log | 3–6 ADR-style bullets on the choices you made. |
| Live demo | The 4 proofs, run in front of the group. |

---

## Grading rubric (100 pts)

| Band | Pts | Bar |
|---|---|---|
| Platform base | 25 | Day 1 + Day 2 checkpoints green: IOMMU live, GPUs on vfio-pci, healthy k3s + Cilium. |
| GPU in a microVM | 30 | Checkpoint 3: Kata pod boots a guest kernel **and** sees the GPU via `nvidia-smi`. |
| Serving spike | 25 | Checkpoint 4: vLLM-in-Kata returns real tokens via the OpenAI API. |
| Reproducibility | 15 | A fresh team can rebuild from your committed files + `RUNBOOK.md`. |
| Communication | 5 | Clear `SPIKE-REPORT.md`, decision log, and demo. |

Pass = 60. Reaching "GPU in a microVM" (55 pts) is the intended floor for a successful week;
the serving spike is the stretch that separates strong teams.

---

## Known pitfalls (we hit these — learn from our scars)

- **The whole thing is blocked in BIOS.** A kernel flag cannot enable an IOMMU the firmware hasn't turned on. If `Found IOMMU` never appears and `/sys/kernel/iommu_groups` is empty → it's a **BIOS** problem, not a kernel-cmdline problem. Fix the firmware first.
- **`amd_iommu=on` is a trap.** It's invalid on current kernels (`Unknown option - 'on'`). The correct cmdline is: **AMD — no `iommu=` flag** (firmware enable is enough); **Intel — `intel_iommu=on`**. Do **not** add `iommu=pt` (next bullet).
- **nouveau will steal your GPU** at boot if you don't blacklist it *and* load `vfio-pci` early (initramfs). If `driver in use` shows `nouveau`, your binding lost the race.
- **IOMMU group hygiene.** A GPU sharing its group with unrelated devices means you'd have to pass them all through together. On server boards each GPU is usually isolated; on consumer boards it often isn't. Check before you build on top.
- **An old kernel silently breaks GPU-in-Kata.** The Kata GPU runtime uses the **iommufd** VFIO
  backend; QEMU 10.2-era code calls `IOMMU_VDEVICE_ALLOC`, an ioctl that **only exists in kernel
  6.13+**. On kernel 6.8 the attach fails (`ENOTTY`) and QEMU **segfaults** with no useful message —
  the pod just times out. We chased this as a "128 GB BAR / MMIO aperture" problem and were **wrong**;
  it was purely the kernel. Be on **≥ 6.13** (HWE on 24.04) before Day 3. It is *not* GPU-size-specific.
- **`iommu=pt` blocks SEV-SNP — so we don't use it.** It only enables IOMMU passthrough mode, which
  VFIO does not need. With SNP enabled in firmware the kernel then refuses to bring SNP up
  (`SNP: IOMMU ... passthrough mode, SNP cannot be supported`). Because Bruk targets confidential
  compute, the cmdline carries **no `iommu=pt`**. (It's only worth adding on a non-CC box that wants
  the passthrough perf — and if you see that SNP message, removing `iommu=pt` is the fix.)
- **Default mode ≠ VFIO mode.** The GPU Operator's *default* install gives you the host NVIDIA driver, **not** VFIO/Kata. You must explicitly enable `vfio-manager` + `kata-manager`. If `nvidia-smi` runs on the *host*, you installed the wrong mode.
- **k3s ships batteries you don't want.** Traefik/servicelb/flannel will quietly conflict with Cilium and your runtime choices. Strip them at install time (Day 2 flags), don't fight them later.
- **Don't mutate the cluster by hand and forget.** Every `kubectl edit`/`apply` that isn't backed by a committed file is reproducibility debt you pay on Day 5.

---

## Stretch goals (only if Checkpoint 4 is green early)

- Put **Dynamo** in front of vLLM (prefill/decode disaggregation).
- A minimal **Envoy front door**: TLS + JWT (static JWKS).
- Swap hand-staged weights for a **Cosign-verified OCI model artifact**.

## Safety / etiquette

- These are real machines with real GPUs. Note firmware changes you make so you can revert them.
- If your GPU's IOMMU group is not isolated, **stop and ask** before applying ACS-override hacks.
- Don't expose the serving endpoint to the public internet during the sprint.
