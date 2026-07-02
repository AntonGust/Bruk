---
status: accepted
---

# Confidential serving (SEV-SNP + single-GPU SPT) is the next phase; guest attestation ≠ host TPM attestation

> **Still accepted, but read with two later ADRs.** The *decision* (confidential serving is the phase;
> the staged 1→5 bring-up; guest attestation ≠ host TPM attestation) stands and is what we executed.
> The **hardware context** here — RTX PRO 6000 SE / SPT / ~33.8 tok/s baseline — is **amended by
> ADR-0005** (H100 / single-GPU CC on EPYC 9224; ~100 tok/s baseline). How the model image+weights
> actually reach the confidential guest is worked out in **ADR-0006**.
>
> **All five steps are now executed.** Steps 1–3: 2026-06-30/07-01. Steps 4–5: **2026-07-02** —
> PSP report verified against AMD KDS (VCEK→ASK→ARK + signature + nonce) and GPU attestation
> passed (nvtrust: SPDM + cert chain + RIM/measurement match); same-model CC-vs-non-CC delta
> **13.5 % single-stream / 10.8 % batched** (Qwen-0.5B). Suite: `manifests/attestation/`;
> results: `docs/h100-bringup-status.md`.

With the single-node serving skeleton green (ADR-0003, Checkpoint 4 — vLLM serving Mistral-Small-3.1-24B
FP8 in Kata+VFIO at ~33.8 tok/s), we decided the **next phase is confidential-serving bring-up** on the
current box: run that same workload inside a **SEV-SNP** confidential guest with one GPU in **CC SPT
mode**, and pull real attestation. We chose this over the Envoy/Dynamo serving front and over the
preflight/host-attestation scaffolding.

The decision turns on one non-obvious distinction the team's shorthand ("we're blocked on confidential
compute — no TPM") was hiding: **the missing fTPM only blocks *host* attestation (the node proving its
boot chain to a fleet-plane verifier — the *attestation gate*). It does NOT block *guest* attestation.**
A SEV-SNP guest is measured and attested by the **AMD-PSP** (report signed by AMD's root key); the GPU
has its **own** device attestation (SPDM + NVIDIA device certs, verified via nvtrust / a local verifier).
Neither needs `/dev/tpm0`. So the platform's actual differentiator — confidential inference — is
*unblocked today*, even though the fTPM is still absent.

## Findings that unblocked this (verified 2026-06-24)

- **SEV-SNP is LIVE on the box.** The CBS ASID-split pass was done (ASID limit → 100). On `secure-puppy`:
  `kvm_amd: SEV-SNP enabled (ASIDs 1-99)`, `sev_snp=Y`, `AMD-Vi: IOMMU SNP support enabled`, ccp
  `SEV-SNP API:1.58`. The prior `ASIDs 0-0` blocker is resolved.
- **The GPUs support CC.** NVIDIA documents the **RTX PRO 6000 Blackwell SE** as a confidential-computing
  GPU, with **only SPT (Single-GPU PassThrough) mode validated** on this SKU. CPU requirement (AMD
  SEV-SNP ≥ EPYC Genoa) is met. SPT = exactly one GPU per confidential guest — which **matches Bruk's
  one-whole-GPU-per-vLLM-worker topology**, so confidential serving needs no topology change.
- **fTPM still absent** (`NO /dev/tpm*`) — enabled on the next *physical* BIOS pass (already scheduled).

## Phase shape (staged, de-risked in order)

1. Bare SEV-SNP Kata/CoCo guest (no GPU) → pull a PSP attestation report.
2. Add one idle GPU (`41:00.0`/`c2:00.0`; vLLM uses `81:00.0`) in CC SPT mode, pass into the SNP guest.
3. Re-run the Checkpoint-4 Mistral FP8 vLLM workload inside the confidential guest.
4. GPU device attestation (nvtrust / local verifier) + verify the PSP report.
5. Perf delta vs the ~33.8 tok/s non-CC baseline (quantify the encrypted-PCIe cost).

## Considered alternatives

- **Envoy + Dynamo serving front (rejected as the next phase).** Envoy (the OpenAI-contract owner) is
  box-independent and still wanted — but it is not the differentiator and can land any time. **Dynamo
  buys ~nothing on this box now:** vLLM already serves the OpenAI contract, and Dynamo's value
  (KV-aware routing, prefill/decode disaggregation) is a multi-worker/NVLink story. On 4× RTX PRO 6000
  (PCIe-5, **no NVLink**) any disaggregation perf measured now is **throwaway** — it won't transfer to
  NVLink. **Deferred to the HGX B300 (Oct 2026)**, where it is representative. Re-pointing Envoy's
  upstream from `vllm-svc` to a Dynamo frontend later is config, not a rewrite — so deferring costs
  nothing structurally.
- **Preflight + host-attestation scaffolding (parked).** Premised on CC being blocked; it isn't (see
  above). The host-boot slice genuinely needs the fTPM and a fleet-plane verifier shape we haven't
  designed — building it now risks building the wrong thing. **Parked for the next fTPM BIOS pass.**

## Consequences / accept these

- **Bleeding-edge integration risk remains** (it's no longer a *hardware* wall): version-matching across
  SNP guest kernel + QEMU machine type + NVIDIA CC guest driver + CC toolkit + SPDM/attestation libs is
  fragile and tends to fail *silently* (boots, but GPU quietly runs non-CC). Step 4 exists to catch that.
- **Expect a perf cliff.** CC-mode encrypts CPU↔GPU PCIe traffic (bounce buffers); the ~33.8 tok/s
  baseline will drop. Step 5 quantifies it — a usable-rate check, not a regression to fix.
- **Single-GPU only.** SPT means no multi-GPU tensor-parallel inside one TEE on this hardware; bigger
  models / multi-GPU-in-one-TEE wait for B200/B300-class GPUs (Oct 2026).
- **Host attestation is explicitly out of scope** for this phase and remains gated on the fTPM.

## Revisit triggers

- **fTPM lands** (next BIOS pass) → host-boot attestation + preflight become buildable.
- **HGX B300 arrives (Oct 2026)** → Dynamo + multi-GPU-CC become representative; re-open the serving-front
  and disaggregation work. See the multi-node testbed timeline.
- **Step 2 reveals CC-mode isn't usable on the RTX PRO 6000 SE** → confidential GPU serving formally
  waits for B300; the SNP-CPU-only guest (steps 1, 3) may still ship as an interim confidential posture.
