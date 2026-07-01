# As-built snapshot — `secure-puppy`

Reference capture of the single-node serving box **before decommission / migration to 2× H100**.
Captured 2026-06-29. Hardware: AMD EPYC + 4× **RTX PRO 6000 Blackwell SE** (sm_120), kernel 6.17.0-35-generic.

This is a **reference snapshot, not a backup**. Everything that constitutes actual source or data is
already safe elsewhere (see below). These files exist so the working stack can be reproduced — most
usefully on the H100 box — without re-deriving the hand-tuned bits.

## Why each file is here

| File | What it is | Why kept |
|------|-----------|----------|
| `asbuilt-state.txt` | Versions + SEV-SNP/IOMMU dmesg + GPU/VFIO binding + cmdline | The reproducibility recipe: exact k3s / Kata / GPU-operator / driver versions that worked |
| `kata-configuration-qemu-nvidia-gpu.toml` | **Active** Kata confidential-GPU runtime config | Hand-earned, **was not in git**. Key values: `image=…confidential.img`, `vfio_mode="guest-kernel"` (the iommufd CC-compatible mode), `kernel_verity_params` root_hash |
| `kata-configuration-qemu-nvidia-gpu-snp.toml` | The SNP variant of the above | Reference for the confidential-serving (SEV-SNP) phase — ADR-0004 |
| `live-vllm-stack.yaml` | The vLLM Deployment + Service + PVC as actually applied | Drift check vs the committed manifest |
| `live-runtimeclass.yaml` | `kata-qemu-nvidia-gpu` RuntimeClass as applied | — |
| `k3s-runtimes.yaml` | k3s auto-deploy RuntimeClass registration | — |
| `cp3-probe.yaml`, `gpu-probe.yaml` | Throwaway GPU-passthrough probe pods | Completeness; trivial |

## Key facts captured at decommission

- **No manifest drift.** The running vLLM stack is byte-for-byte `manifests/day4-vllm.yaml`
  (confirmed via the `last-applied-configuration` annotation). Nothing was tweaked in-cluster.
- **SEV-SNP was live** (`kvm_amd: SEV-SNP enabled`, `AMD-Vi: IOMMU SNP support enabled`, CCP
  `SEV-SNP API:1.58`, ASID split 1–99 / 100–1006). See `asbuilt-state.txt`.
- **`/proc/cmdline` is clean** — no `amd_iommu`/SEV kernel params. SEV-SNP enablement comes entirely
  from **BIOS/CBS** (the ASID split), *not* from the kernel command line.

## Already safe — NOT in this snapshot (and why it didn't need to be)

- Source / manifests / ADRs → in git.
- 120Gi `vllm-models` PVC → just Mistral-Small-3.1-24B FP8 weights, re-downloadable.
- k3s, GPU operator, kata-deploy, Cilium → all re-installable (versions recorded in `asbuilt-state.txt`).
- ~33.8 tok/s baseline, SEV-SNP/ASID facts, attention-backend choice → recorded in `docs/adr/`.

## Cannot be saved from the OS — must be redone on the H100 box

- **BIOS/CBS SEV-SNP enablement + ASID split** — firmware state, not on disk.
- **fTPM** — was still absent.
- ⚠️ **Confirm the H100 box is AMD EPYC.** If it's Intel Xeon, the confidential-serving plan moves from
  SEV-SNP to Intel TDX — a different attestation stack (see the H100 migration analysis / ADR-0004).
