# Debug: Day-3 Kata GPU passthrough fails — host NVIDIA driver must not be installed

**Date:** 2026-07-07 · **Node:** `bruk-fe4` (student box, 2× H100 NVL, Ubuntu 24.04, kernel
6.17.0-35, kata-deploy 3.29.0 / QEMU 10.2.1 kata-static) · **Diagnosed against:** the working
reference box (identical hardware/software, boots this exact workload daily).

## Symptom

Day-3 probe pod (`kata-gpu-probe`, runtimeClass `kata-qemu-nvidia-gpu`, `nvidia.com/pgpu: 1`)
schedules but sticks in `ContainerCreating`. Kata shim log shows a burst of:

```
qemu-system-x86_64: IOMMU_IOAS_MAP failed: Bad address, PCI BAR?
vfio_container_dma_map(...) = -14 (Bad address)
0000:21:00.0: PCI peer-to-peer transactions on BARs are not supported.
```

then ~3 s later:

```
Unable to connect to unix socket (/run/vc/vm/<sandbox>/qmp.sock)
Failed to connect to QEMU instance
```

QEMU dies with no visible fatal error; the VM never boots.

## Red herring — read this before chasing BAR errors

The three `IOMMU_IOAS_MAP` / `vfio_container_dma_map` / "peer-to-peer" lines are **normal**.
The healthy reference box prints the identical lines (the P2P one even at `level=error`) on
**every successful launch** — QEMU tries to IOMMU-map the GPU BARs for device-to-device DMA,
the kernel declines, QEMU downgrades gracefully and boots. Do not resize BARs because of these
lines (the validated config runs the H100 NVL with stock BARs: 16M / **128G** / 32M). Any
Day-3 debugging that starts from these lines is chasing noise; the real error is whatever
comes *after* them, or — as here — a silent kill from outside QEMU.

## Root cause

**The Ubuntu host NVIDIA driver stack was installed on the node.** Found on `bruk-fe4`:

```
nvidia-dkms-580-server, nvidia-headless-580-server, nvidia-utils-580-server,
nvidia-kernel-common-580-server, libnvidia-compute-580-server, … (580.159.03)
```

On this architecture the NVIDIA driver exists **only inside the guest VM** (it ships in the
workload image). The host GPUs are bound to `vfio-pci` and must stay driverless. The
**reference box has zero `nvidia*` packages** (verified: `dpkg -l | grep '^ii.*nvidia'` is
empty).

Observable blast on the student box:

- udev's NVIDIA rules fire `modprobe nvidia-drm / nvidia-uvm / nvidia-modeset` **every
  second**; NVRM loads, finds both GPUs owned by vfio-pci ("GPU 0000:21:00.0 is already bound
  to vfio-pci … No NVIDIA devices probed"), unloads, repeats — dmesg is a wall of this loop.
- `nvidia.ko` ends up wedged half-loaded (`lsmod` shows refcount −2, libkmod errors reading
  `/sys/module/nvidia/holders`).
- The continuous PCI-driver register/unregister churn lands in QEMU's VFIO device-setup
  window → QEMU dies silently mid-launch → shim's QMP dial fails → pod stuck
  `ContainerCreating`.

How it likely got there: someone wanted `nvidia-smi` on the host, or the GPU Operator was
initially configured for container workloads (see secondary finding) and a host driver was
installed to match. Both instincts are wrong on a passthrough node.

## Secondary finding — ClusterPolicy divergence

`bruk-fe4` ClusterPolicy: `sandboxWorkloads: {enabled: true, mode: kata, defaultWorkload:
"container"}`. The validated values (`install/helm-values/gpu-operator.yaml`) set
`defaultWorkload: vm-passthrough` (plus the k3s containerd paths for the toolkit). The
container default is also why a `gfd-*` pod exists on the student box — the reference box
runs none. Align the operator install with the committed values file.

## Fix

```bash
# 1. Remove the host driver stack entirely (also removes the udev rules driving the storm)
sudo apt-get purge 'nvidia-*' 'libnvidia-*'
sudo apt-get autoremove

# 2. Reboot — mandatory; nothing less clears the wedged nvidia.ko
sudo reboot

# 3. Verify clean host state
lsmod | grep -E '^nvidia|^nouveau'        # expect: empty
sudo dmesg | grep -c NVRM                 # expect: 0 (or only pre-purge boot noise)
lspci -nnk -d 10de: | grep 'driver in use' # expect: vfio-pci on every GPU

# 4. Known post-reboot race (hit on the reference box 2026-07-07): the kata sandbox
#    device plugin can scan before vfio binding completes -> node stays at nvidia.com/pgpu: 0
#    and pods stay Pending forever. Check and, if 0, restart the plugin pod:
kubectl get node <node> -o jsonpath='{.status.allocatable.nvidia\.com/pgpu}'
kubectl delete pod -n gpu-operator -l app=nvidia-kata-sandbox-device-plugin-daemonset
# pgpu flips to 2 within ~1 min

# 5. Re-align gpu-operator with install/helm-values/gpu-operator.yaml
#    (defaultWorkload: vm-passthrough, mode: kata, k3s toolkit paths)

# 6. Re-run the Day-3 probe. Ignore IOMMU_IOAS_MAP / peer-to-peer warnings in the shim
#    log — they are part of a healthy launch.
```

## Runbook hardening (do alongside the fix)

Add to the Day-1 preflight (host-setup / day-1 check):

```bash
if dpkg -l | grep -qE '^ii.*nvidia'; then
  echo "FAIL: host NVIDIA driver packages installed — the driver lives IN-GUEST on this stack."
  echo "      Purge them: sudo apt-get purge 'nvidia-*' 'libnvidia-*' && reboot"
  exit 1
fi
```

And document in the Day-3 section: the `IOMMU_IOAS_MAP`/P2P lines are expected noise on
successful launches; diagnose from the *first* error after them (or from dmesg) instead.

## Diagnostic trail (for reference)

1. Kernel, cmdline, vfio bindings, BAR layout, kata/QEMU versions all matched the reference
   box → ruled out kernel (6.17 ✓), `iommu=pt` (absent ✓), BAR resize (stock ✓), version
   drift (3.29.0/10.2.1 ✓), runtimeClass (`kata-qemu-nvidia-gpu` ✓), scheduling (pgpu: 2 ✓).
2. Reference-box journal showed the same "fatal-looking" P2P lines on successful launches →
   reclassified as noise.
3. Student-box shim log: warning burst → QMP dial failure 3 s later, **no QEMU fatal, no
   kata `level=error` beyond the dial** → looked for an external killer.
4. dmesg: 1 Hz NVRM/nvlink modprobe loop against vfio-bound GPUs; `lsmod` showed wedged
   `nvidia` module; journal attributed the modprobes to udev workers; `dpkg -l` showed the
   580-server driver set. Reference box: zero nvidia packages.
