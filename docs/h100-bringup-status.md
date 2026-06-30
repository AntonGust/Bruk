# H100 bring-up status (`anton-bruk`, ssh ubuntu@77.87.121.15)

Snapshot of the 2× H100 NVL box bring-up as of 2026-06-30. **The box is one supervised reboot away
from GPUs-live serving.** Resume point: get a BMC/tech standby, reboot, verify, then `kubectl apply`.

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

## Staged, pending ONE supervised reboot ⏳
Applied to the box but **not yet active** (we deliberately did NOT reboot — no BMC/tech recovery access
right now):
- **#3 boot-order fix** — `efibootmgr -o 0000,0002,0003` (disk-first; PXE entries kept). Root cause of
  the earlier "stuck boots" was **PXE-first order** (MAAS leftover) hanging on the dead MAAS server.
  Restore PXE-first with `efibootmgr -o 0002,0000,0003`, or MAAS forces PXE via IPMI override.
- **#5 VFIO/nouveau** — `/etc/modprobe.d/vfio-h100.conf` (blacklist nouveau+nvidiafb, `vfio-pci
  ids=10de:2321`); vfio modules in initramfs; initrd rebuilt. Kata knobs already correct in 3.29.0
  (`vfio_mode=guest-kernel`, `cold_plug_vfio=root-port`, `pcie_root_port=8`, `hot_plug_vfio=no-port`).

**That one reboot activates all of it:** boots from disk in <1 min (verifies #3) → nouveau gone, both
H100s on `vfio-pci` → GPU-operator `vfio-manager` OK → `ClusterPolicy ready` → `nvidia.com/pgpu: 2`.
Until then: GPUs on `nouveau`, `ClusterPolicy notReady` (expected/inert).

## Next
- **#7** — `kubectl apply -f manifests/h100-vllm.yaml` (+ `hf-token` secret) → serve Mistral-Small-3.1
  FP8, record tok/s (vs ~33.8 on RTX PRO 6000).
- **#8** confidential serving (ADR-0004, `-snp` runtimeclass) · **#9** ADR/doc follow-ups · **#10** TPM.

## Operational notes
- **Reboots:** prefer **cold power-cycle via BMC**. No BMC/tech access at present → don't reboot until
  standby is available; `6.8.0-124` is the GRUB fallback if 6.17 ever misbehaves.
- **MAAS left intact** (user wants it back when the MAAS server returns): cloud-init + the
  `maas_hardware_sync.timer` loop still run (latter accounts for the high idle load average) — harmless;
  PXE boot entries preserved.
