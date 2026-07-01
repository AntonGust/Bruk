---
status: accepted
---

# H100 pilot hardware (2× H100 on AMD EPYC 9224): the SEV-SNP plan ports unchanged; the sm_120 attention workaround does not

The pilot hardware changes from **4× RTX PRO 6000 Blackwell SE (sm_120, workstation Blackwell)** to
**2× H100 (sm_90, Hopper datacenter) on an AMD EPYC 9224**. We decided this is a **low-risk, mostly
favourable** move: the confidential-serving plan (ADR-0004) ports directly because the CPU is AMD
**Genoa** (SEV-SNP, not Intel TDX), and the H100 *removes* the sm_120 attention-backend constraints
that ADR-0003 was written to work around. The single-GPU-per-TEE topology is unchanged. The old box
(`secure-puppy`) is decommissioned; an as-built snapshot is in `docs/asbuilt-secure-puppy/`.

The decision turned on one question that gated everything else — **what CPU is in the H100 box** — and
the answer (AMD EPYC 9224) is the good one: the entire SEV-SNP attestation stack carries over.

## Findings that settled this (verified 2026-06-29)

- **AMD EPYC 9224 is Genoa (Zen 4, socket SP5), and it has SEV-SNP + SEV-ES** (24c/48t, PCIe Gen 5,
  12-channel DDR5). This meets ADR-0004's "AMD SEV-SNP ≥ EPYC Genoa" requirement, so the
  Kata+VFIO+SEV-SNP+nvtrust stack ports **with no architectural change**. Had the box been Intel Xeon,
  confidential serving would have had to pivot to **Intel TDX** — a different attestation flow,
  toolchain, and guest kernel/QEMU config. It does not.
- **H100 confidential-compute mode is single-GPU-per-TEE**, for the same reason as SPT on the
  RTX PRO 6000: **NVLink traffic is not encrypted in CC mode**, so multiple H100s cannot form a
  hardware-encrypted shared memory domain inside one TEE. This *matches* Bruk's one-GPU-per-worker
  topology — confidential serving needs no topology change. Encrypted multi-GPU-in-one-TEE still waits
  for **Blackwell MPT CC** (NVSwitch-encrypted NVLink) on B200/B300 — the same plan as before.
- **H100 is sm_90 (Hopper datacenter).** FlashAttention-3 is **native and recommended**; FlashInfer
  ships real prebuilt sm_90 cubins; TensorRT-LLM is viable. The sm_120 hard ceiling that drove
  ADR-0003 (FA3 architecturally impossible — TMEM absent on workstation Blackwell) **does not apply.**
- **GPU device attestation (nvtrust / SPDM) is more mature on H100** than on the RTX PRO 6000 SE
  (H100 was the first CC GPU; go-nvtrust and NVIDIA NRAS list it as a primary target; no R580+ driver
  floor). Fewer rough edges expected on step 4 of the ADR-0004 bring-up, not more.

## Ports unchanged from ADR-0004

- The staged confidential-serving bring-up (steps 1→5) and the Kata+VFIO+SEV-SNP+nvtrust stack.
- **1 GPU per confidential guest.** With 2 H100s the box supports up to **two** concurrent confidential
  guests (was four on the RTX PRO 6000 box); the pilot needs one serving worker, so this is non-binding.
- Host attestation remains out of scope and gated on the fTPM.

## What changes

- **Attention backend — ADR-0003's rationale is void on H100.** The deliberate `FLASH_ATTN` pin was a
  *sm_120* decision. On sm_90, `FLASH_ATTN` resolves to **FA3** (so the env value may even stay), but the
  reasoning is no longer "FA2 is the ceiling" — it's "FA3 is the native default," and **FlashInfer /
  TRT-LLM are now real options**. This **fires the ADR-0003 revisit trigger** ("moves to datacenter
  GPUs"). A new H100 serving manifest must drop the sm_120 comment block in
  `manifests/day4-vllm.yaml` and re-point it at the ADR-0003 update.
- **Less VRAM, not more: H100 = 80 GB vs RTX PRO 6000 = 96 GB.** Mistral-Small-3.1-24B FP8 (~24 GB
  weights) still fits comfortably with large KV-cache headroom, but the manifest's "~96 GB VRAM" comment
  and `--gpu-memory-utilization=0.90` assumption must be re-checked for 80 GB. (H100 NVL = 94 GB if that
  SKU is chosen.)
- **GPU count 4 → 2**, and an older/more-proven NVIDIA driver stack is acceptable (no R580+ floor).

## Open items (do not block this decision)

- **H100 form factor — SXM vs PCIe — is unconfirmed.** Its *only* consequence is NVLink for
  **non-confidential** tensor-parallel serving (SXM = NVLink ~900 GB/s; PCIe = none). It does **not**
  affect confidential serving, where NVLink is unusable (unencrypted) regardless. Confirm before any
  non-CC multi-GPU perf work.

## Must be redone on the new box (not portable, captured for reference)

- **BIOS/CBS SEV-SNP enablement + the ASID split** — firmware state, not on disk. The new EPYC 9224
  needs the same CBS pass from scratch. See `docs/asbuilt-secure-puppy/asbuilt-state.txt` for the
  working end-state to reproduce.
- **fTPM** — still absent on the old box; (re)enable on the new box's BIOS pass to unblock host
  attestation later.

## Consequences

- **ADR-0004's hardware context is amended** ("RTX PRO 6000 SE / SPT" → "H100 / single-GPU CC on
  EPYC 9224 Genoa"); its plan body stands unchanged.
- **ADR-0003 is superseded for H100 deploys** (kept as the record for the RTX PRO 6000 era). Its revisit
  trigger is now fired; update it when the H100 backend is chosen.
- A **new H100 serving manifest** is needed — `day4-vllm.yaml`'s VRAM and backend assumptions are
  sm_120/96 GB-specific and should not be deployed verbatim on H100.

## Revisit triggers

- **fTPM lands** on the new box → host-boot attestation + preflight become buildable.
- **H100 form factor confirmed SXM** → NVLink available for non-CC tensor-parallel serving experiments.
- **HGX B300 arrives (Oct 2026)** → Blackwell MPT CC (encrypted NVLink) makes multi-GPU-in-one-TEE and
  Dynamo disaggregation representative; re-open both. See the multi-node testbed timeline.
