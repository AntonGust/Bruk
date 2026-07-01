#!/usr/bin/env bash
# host-setup.sh — the HOST layer above BIOS (kernel + VFIO + sanity checks).
# Run ONCE on a fresh node AFTER the BIOS SEV-SNP settings are done (see install/README.md §BIOS).
# Ends by requiring a reboot. Then run install/bootstrap.sh.
#
# What this does NOT do: BIOS/SEV-SNP firmware (per-machine, manual via BMC — the one irreducibly-manual
# step) and bare-metal imaging (a fleet-plane/MAAS concern). Everything here is automatable and is.
#
# Reference box: 2x H100 NVL (10de:2321) on AMD EPYC 9224, Ubuntu 24.04. Adjust GPU_DEVICE_ID for other
# GPUs (find it: `lspci -nnk | grep -iA2 nvidia` → the [10de:XXXX] id).
set -euo pipefail

GPU_DEVICE_ID="${GPU_DEVICE_ID:-10de:2321}"   # H100 NVL; override for other cards
HWE_META="linux-generic-hwe-24.04"            # -> kernel 6.17 on 24.04 (SEV-SNP host + iommufd GPU passthrough)

log() { printf '\n=== %s ===\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)"; exit 1; }

log "1/5 HWE kernel (>=6.11 for SEV-SNP host + iommufd GPU passthrough)"
if dpkg -l "$HWE_META" >/dev/null 2>&1; then
  echo "$HWE_META already installed ($(uname -r) running; reboot picks up newest)"
else
  apt-get update -y
  apt-get install -y --install-recommends "$HWE_META"
fi

log "2/5 VFIO bind + nouveau/nvidiafb blacklist (/etc/modprobe.d/vfio-h100.conf)"
cat > /etc/modprobe.d/vfio-h100.conf <<EOF
# Bind NVIDIA GPUs to vfio-pci and keep host drivers off them (for Kata VFIO passthrough).
blacklist nouveau
blacklist nvidiafb
options vfio-pci ids=${GPU_DEVICE_ID}
softdep nouveau pre: vfio-pci
softdep nvidiafb pre: vfio-pci
EOF

log "3/5 vfio modules in initramfs"
for m in vfio vfio_iommu_type1 vfio_pci; do
  grep -qxF "$m" /etc/initramfs-tools/modules 2>/dev/null || echo "$m" >> /etc/initramfs-tools/modules
done
update-initramfs -u

log "4/5 kernel cmdline sanity (AMD: NO iommu= flag; iommu=pt BREAKS SEV-SNP)"
if grep -q "iommu=pt" /etc/default/grub 2>/dev/null; then
  echo "!! WARNING: 'iommu=pt' present in /etc/default/grub — it blocks SEV-SNP. Remove it, then update-grub."
  echo "   (AMD needs no iommu= flag once IOMMU is enabled in firmware; VFIO builds its own domains.)"
else
  echo "ok: no iommu=pt on the cmdline"
fi

log "5/5 boot-order note (box-specific)"
cat <<'EOF'
If this node PXE-boots first (e.g. a MAAS provisioning leftover) and hangs on a dead PXE server, set
disk-first ONCE with efibootmgr (entry IDs are per-machine — inspect first):
    sudo efibootmgr                       # find the 'Ubuntu' (disk) entry id, e.g. 0000
    sudo efibootmgr -o <disk>,<pxe...>    # e.g. 0000,0002,0003
A generic fresh machine usually does not need this.
EOF

log "DONE — REBOOT REQUIRED"
cat <<'EOF'
  sudo reboot
⚠️ On a SEV-SNP box with large RAM the reboot takes 20-30 min (PSP RMP-table init during POST, before
   the OS). SSH shows 'Connection refused' the whole time — this is normal, not a hang.

After reboot, VERIFY, then run install/bootstrap.sh:
  sudo dmesg | grep -i "SEV-SNP enabled"                 # kvm_amd: SEV-SNP enabled (ASIDs 1-99)
  cat /sys/module/kvm_amd/parameters/sev_snp             # Y
  for d in 21:00.0 81:00.0; do lspci -nnks $d | grep "driver in use"; done   # vfio-pci
EOF
