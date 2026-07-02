---
status: accepted
---

# Confidential model delivery: attestation-gated storage on a block device, not host mounts or guest-RAM — Bruk targets the dm-verity/encrypted-volume pattern (Pattern B), KBS key-release is roadmap

Bringing up confidential serving (ADR-0004, on the H100 box per ADR-0005) surfaced a wall the non-CC
path never had: **how do the container image and the model weights get into a confidential guest, when
the host is untrusted?** The Sprint-1/non-CC answer — host holds plaintext weights, shares them into
the Kata guest over virtiofs (a PVC) — is structurally illegal in a TEE. We decided the confidential
path uses **attestation-gated storage on an attached block device**, and specifically that Bruk targets
the **dm-verity / encrypted-volume + memory-encryption + attestation** shape (call it *Pattern B*) as
the achievable single-box target, with the full **KBS/Trustee attested-key-release** shape (*Pattern A*)
as documented roadmap. This was settled by hitting the wall on our box plus a three-thread survey of
what actually ships (2026-07-01).

## What forced the decision (verified on our box, 2026-07-01)

- **The confidential GPU runtime is `shared_fs = "none"`.** `kata-qemu-nvidia-gpu-snp` has no virtiofs
  — both by threat model (host FS is untrusted, so virtiofs-shared layers break confidentiality) and by
  a concrete blocker (Kata's SNP docs: *virtio-fs unsupported due to QEMU snp-v3 bugs*; virtio-9p
  dropped). **So the non-CC weights PVC cannot be mounted.** Not a config to "fix" — the design point.
- **Confidential containers guest-pull the image into guest RAM.** The `vllm/vllm-openai:v0.11.1` image
  is ~13 GiB compressed / **~35 GB unpacked**; guest-pull unpacks it into the guest's RAM-backed tmpfs
  (ceiling ~50% of guest RAM). At `memory: 32Gi` it OOM'd (`No space left on device`) in ~2 min; at
  96Gi (~52 GB tmpfs) it OOM'd at ~20 min, right near the finish. **Guest-pull-into-RAM does not scale
  to real serving images**, let alone tens of GB of weights on top. This is a *known* CoCo limitation:
  RFC #247 ("Trusted Ephemeral Data Storage") exists because the current tmpfs fallback "wastes costly
  guest memory."

## The invariant, and the two accepted patterns (survey 2026-07-01)

**Universal invariant across every vendor/project:** **attestation-before-serving** — the guest presents
its SEV-SNP report **plus** the H100 GPU report, and only then does a key exchange or secret release
happen. A **plaintext host mount is never accepted**; storage lives on an **attached block device, never
RAM tmpfs**.

Two legitimate production shapes for the weights themselves:

- **Pattern A — encrypt-at-rest + attestation-gated key release.** Weights encrypted (encrypted OCI
  image via ocicrypt, or a dm-crypt volume); the key sits in a **KBS** and is released only to an
  attested guest via the **Trustee** stack (Attestation-Agent + Confidential-Data-Hub in-guest; KBS +
  Attestation Service + RVPS remote; Rego policy gates on SEV-SNP TCB **and** GPU-in-CC). Strongest
  at-rest guarantee; most moving parts. Used by CoCo/Red Hat; Tinfoil's KMS variant.
- **Pattern B — integrity-protected (dm-verity) read-only weights disk + runtime memory encryption +
  attestation.** Weights mounted read-only with dm-verity (host can't tamper); confidentiality comes
  from live SEV-SNP RAM encryption + H100 HBM/encrypted-PCIe + an attested channel, **not** from
  encrypting the file at rest. This is what the platforms actually **shipping** H100 confidential LLM
  inference use — **Edgeless Continuum/Privatemode** (vLLM in SEV-SNP CVMs + H100, weights as a
  read-only integrity-checked disk) and **Tinfoil**. Rationale: the untrusted host can't read CVM/GPU
  memory anyway, so at-rest encryption is largely redundant; integrity + memory-encryption + attestation
  is sufficient and far simpler.

**NVIDIA** prescribes the *attestation* mechanics (nvtrust / NRAS, GPU report + CPU report before
workload) and the CC-mode GPU internals (encrypted HBM, signed PCIe bounce buffers) but **no single
weights-delivery mechanism** — it's explicitly left to the stack (CoCo / Azure NCC-H100 / Edgeless).
There is **no turnkey "large model in a confidential container" reference** in the Kata/CoCo/GPU-Operator
docs — a documented gap.

## Decision

1. **Storage goes on an attached `virtio-blk` block device (host NVMe, ~3.2 TB free), never guest RAM
   tmpfs.** This dissolves both the image OOM and the "re-pull 35 GB into RAM every start" cost. CoCo's
   own `kata-agent`/CDH `luks-encrypt-storage` (random guest key) is the sanctioned mechanism.
2. **Bruk targets Pattern B** for the pilot: weights on a dm-verity read-only (or dm-crypt) block
   device, confidentiality from SEV-SNP + H100 CC + attestation-before-serving. It's what ships, it's
   achievable on one box, and our ADR-0004 Days 1-3 foundation (SNP guest + PSP report + CC GPU + node)
   already supports it.
3. **Pattern A (KBS/Trustee attested key release) is roadmap**, not pilot scope — it's a platform
   sub-project (KBS + AS + RVPS + encrypted-artifact packaging). Revisit when there's a fleet-plane
   verifier to host the KBS (ties to the parked host-attestation work and the fTPM).
4. **Weights are never baked into the container image** (a 35 GB image is the wrong unit); they live on
   the mounted volume, which also sidesteps re-pull.
5. **Image large-size handling:** accept guest-pull for now (brute-force guest RAM only for small-model
   smoke tests); the durable fix is the same block-device storage (image cache on NVMe). Nydus is *not*
   a clean win here — its lazy-loading (fscache) mode is the non-confidential path; the CoCo variant
   (tarfs + dm-verity) is integrity-only and **not** lazy, so "lazy-load to dodge the RAM blowup" and
   "keep confidentiality" are at odds today.

## Consequences / accept these

- **Expect a usable perf cost, not a cliff.** Published CC overhead is ~12% (7B) to ~15% (70B, 8×H100)
  (Phala). Our non-CC H100 baseline ~100 tok/s → confidential ~85 tok/s expected (ADR-0004 step 5).
- **The pilot's confidentiality rests on runtime memory encryption + attestation, not at-rest weights
  encryption** (Pattern B). If a customer requires at-rest key custody outside the host, that's the
  Pattern A upgrade.
- **More bleeding-edge fragility** (ADR-0004 already flagged): encrypted-ephemeral block volume is
  implemented in CoCo; trusted *image*-on-block is partly RFC-stage (#123/#247) — verify against the
  current CoCo release before depending on it.
- **A small-model smoke test may brute-force guest RAM** to prove the path before the block-device work
  lands; that is explicitly a stopgap, recorded as such in `docs/h100-bringup-status.md`.

## Part 2 — EXECUTED (2026-07-02): both storage walls solved on the pinned stack

The decision above is implemented; the 24B serves confidentially. Findings from the pinned-source
review (kata `3.29.0`, guest-components `de3f6ff`) + execution on `anton-bruk`:

- **Weights (~90 GB): block-encrypted emptyDir** — RFC #247 shipped as kata PR #10559 (first in
  3.28.0); `emptydir_mode = "block-encrypted"` is the *build-time default* for all CoCo runtime
  configs in 3.29.0. A plain (non-`medium: Memory`) emptyDir becomes: sparse `disk.img` on host
  NVMe → virtio-scsi hotplug → **LUKS2 dm-crypt with AEAD integrity (`hmac-sha256` + `aes-xts`)
  formatted in-guest with a random per-mount key** (detached header on guest tmpfs) → ext4.
  Host sees only ciphertext. Verified empirically before use. NVIDIA's own NIM-TEE CI test uses
  exactly this for its model cache.
- **Image store (~35 GB unpacked): `/dev/trusted_store`** — RFC #123 shipped as kata PR #9999.
  A `volumeMode: Block` PVC attached at the magic devicePath `/dev/trusted_store` is LUKS2-
  formatted in-guest (same ephemeral-key machinery) and mounted over `/run/kata-containers/image`
  **before** guest-pull (`rpc.rs`: trusted-storage handling precedes `add_storages`). Retires the
  160 Gi guest-RAM brute-force (smoke pod now 32 Gi; 24B 64 Gi). Upstream CI runs this shape on
  `qemu-nvidia-gpu-snp` (`k8s-nvidia-nim.bats`). Size the PVC ≥ 2× image size. Backing: LVM LVs
  on the empty data NVMe (`manifests/trusted-storage.yaml`).
  **Hard-won gotcha:** image-rs's default **parallel layer unpack (3-way) reproducibly fails**
  against the trusted store ("Failed to unpack layer to destination" ~11 s into the pull,
  independent of guest RAM, mirror or upstream registry) while small/single-layer images and raw
  bulk I/O all pass. Fix: **`max_concurrent_layer_downloads_per_image = 1`** in the initdata
  `cdh.toml` `[image]` section (committed in `manifests/registry/initdata.toml`). Serial pull
  costs ~1 min extra via the LAN mirror. Candidate upstream image-rs bug — file with the
  reproduction (bisect log in `docs/h100-bringup-status.md`).
- **Weights delivery decision: first-run HF download into the encrypted emptyDir** (simplest that
  ships). Consequences accepted: weights re-download on every *pod* re-creation (~33 min cold
  start, download-dominated; the volume DOES survive *container* restarts within the pod — a
  crash at minute 14 resumed and completed). Pre-staged/verity-pinned delivery is the upgrade.
- **Egress gotcha that bit (fix committed):** the CC guest resolves IPv6-first with no v6 route —
  HF downloads die with `Network is unreachable`. Fix: `/etc/gai.conf` (`precedence
  ::ffff:0:0/96 100`) via ConfigMap subPath (propagates fine into `shared_fs=none` guests).
- **Result:** Mistral-Small-3.1-24B FP8 serves confidentially at **97.6 tok/s single-stream**
  (batched ×8: 755 tok/s) vs ~100 tok/s non-CC — **~2 % overhead**, far under the 12–15 %
  published band (the 0.5B measured 13.5 %: PCIe bounce-buffer cost shrinks relative to compute
  as the model grows). Attestation suite re-run green on the final config.

**Pattern status after Part 2:** what ships is *encrypted + integrity-protected ephemeral storage
with in-guest ephemeral keys* — at-rest confidentiality on the host is actually **stronger** than
plain Pattern-B dm-verity (which protects integrity, not confidentiality). What is still missing
vs the full patterns, verified NOT wired at these pins: **dm-verity read-only pod volumes** (types
exist in kata-types; no agent storage handler, no CDH plugin — the verity-pinned *provenance*
half of Pattern B) and **KBS/`kbs://` key release for volumes** (CDH implements `sourceType:
"encrypted"` + `kbs://` keys; the kata-agent only ever requests `empty`+ephemeral — so Pattern A
stays roadmap). DIY escape hatch if RO-verified weights are wanted before an upgrade: attach a
verity-formatted device as a plain `volumeDevices` path and run `veritysetup open` in-container —
the Privatemode-proven pattern (needs cryptsetup + CAP_SYS_ADMIN in the image).

## Revisit triggers

- ~~CoCo trusted image/ephemeral block storage reaches stable release~~ → **DONE 2026-07-02**
  (both shipped in kata ≤3.29.0; see Part 2 above).
- **Kata release wires dm-verity pod volumes or KBS-keyed secure mount into the agent** → adopt
  for verity-pinned weights provenance (full Pattern B) / attested key release (Pattern A).
- **Fleet-plane verifier / KBS exists** (with host attestation, post-fTPM) → implement Pattern A
  (attested key release) for at-rest weight custody outside the box.
- **Weights re-download cost becomes operationally painful** → pre-stage weights on a dedicated
  block device (DIY verity or wait for the above), or add a persistent in-cluster HF cache.
- **HGX B300 (Oct 2026)** → multi-GPU-in-one-TEE changes the topology; re-check the storage story then.

## References

- ADR-0004 (confidential serving is the phase), ADR-0005 (H100 pilot HW), `docs/h100-bringup-status.md`.
- CoCo: encrypted images, attestation, Trustee — confidentialcontainers.org/docs; github.com/confidential-containers/trustee
- CoCo RFC #247 (Trusted Ephemeral Data Storage), #123 (Trusted Storage) — github.com/confidential-containers/confidential-containers/issues
- Kata SEV-SNP how-to (`shared_fs=none`, virtio-fs unsupported) — github.com/kata-containers/kata-containers docs/how-to
- Edgeless Continuum / Privatemode (Pattern B, shipping) — docs.privatemode.ai/architecture ; edgeless.systems
- Tinfoil (dm-verity weights + attested key release) — docs.tinfoil.sh/verification/attestation-architecture
- NVIDIA confidential computing / nvtrust — docs.nvidia.com/nvtrust ; developer.nvidia.com/blog (H100 CC, zero-trust confidential AI)
- Azure NCC H100 v5 (GA, CPU+GPU attestation before key release) — github.com/Azure/az-cgpu-onboarding
- Perf: Phala CC overhead ~12-15% — phala.com/learn
