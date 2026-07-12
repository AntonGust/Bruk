# Debug: Day-3 Kata GPU probe dies in-guest — H100 Confidential-Compute mode was ON

**Date:** 2026-07-08 · **Node:** `bruk-fe4` (student box, 2× H100 NVL
94GB, Ubuntu 24.04, kernel 6.17.0-35, k3s v1.36.1, GPU Operator v26.3.2, kata-deploy 3.29.0 /
QEMU 10.2 kata-static). **Resolved.** This is the *second* Day-3 wall on this box; the first
(host NVIDIA driver, see `debug-2026-07-07-day3-host-nvidia-driver.md`) was already fixed. Read
that one first — this picks up after the host is clean.

## Symptom

The Day-3 probe (`runtimeClassName: kata-qemu-nvidia-gpu`, `nvidia.com/pgpu: 1`) sticks in
`ContainerCreating`, then `FailedCreatePodSandBox … create container timeout`. The kata shim log
shows the usual `IOMMU_IOAS_MAP failed: Bad address, PCI BAR?` burst, then
`Failed to connect to QEMU instance`. QEMU launches, lives ~8 seconds, and exits with no fatal
line on stderr.

## Root cause

**The H100s' on-board Confidential-Compute (CC) mode was ON.** Sprint 1 uses the *plain*
`kata-qemu-nvidia-gpu` runtime — an ordinary, unencrypted microVM. A GPU in CC mode can only be
initialised inside a confidential (SEV-SNP) guest. In a plain guest the in-VM NVIDIA driver
aborts the GPU attach. Captured from the guest serial console:

```
NVRC::mode: mode: gpu 1 GPU
NVRM: GPU0 confComputeConstructEngine_IMPL: CPU does not support confidential compute.
NVRM: GPU0 nvAssertFailedNoLog: Assertion failed: 0 @ conf_compute.c:161
NVRM: osInitNvMapping: *** Cannot attach gpu
NVRM: GPU 0000:01:00.0: RmInitAdapter failed! (0x22:0x38:878)
```

With the GPU unattached there is no `/dev/nvidia0`, so the NVIDIA guest init **NVRC** (the image's
`/init`) runs `nvidia-ctk -d cdi generate` and gets an invalid spec, then panics — which kills the
whole VM:

```
level=error msg="failed to write spec: invalid CDI Spec: failed add device \"all\": invalid device, empty device edits"
panic: panicked at src/execute.rs:24:9: /bin/nvidia-ctk failed with status: exit status: 1
```

**Why it was ON:** this box was provisioned for the confidential-compute platform (SEV-SNP + GPU
CC mode), and its confidential SNP workload works. Sprint 1 (plain passthrough) is simply the
wrong mode for a CC-enabled GPU. Nothing was broken — it was mis-configured for this exercise.

## Red herrings (do NOT chase these)

- **`IOMMU_IOAS_MAP failed: Bad address, PCI BAR?` / `vfio_container_dma_map = -14` /
  `peer-to-peer … not supported`** — normal noise. Running QEMU by hand (with
  `ulimit -l unlimited`) realises the GPU past these lines and stays up. The real error is
  *in-guest*, after these.
- **Host NVIDIA driver** — already purged (`dpkg -l | grep nvidia` empty, `NVRM` count 0). Fixed
  the *first* Day-3 wall, not this one.
- **memlock** — kata gives QEMU ~62.7 GiB; GPU realisation succeeds regardless.
- **kubelet `runtime-request-timeout`** — this student k3s sets none (unlike the reference box's
  20m), but QEMU dies at ~8 s, not at the 4-minute deadline, so it is not the cause here.
- **Kata itself** — a plain `kata-qemu` pod (no GPU) boots in seconds (guest kernel 6.18.15).

## Fix

Flip the GPUs' CC mode **off** via the GPU Operator's cc-manager. There were no GPU pods running,
so this is a clean ~1-minute operation (cc-manager briefly evicts GPU-operator components, binds
the driver to set the mode, resets each GPU, and hands them back to `vfio-pci`).

```bash
kubectl patch clusterpolicy cluster-policy --type merge \
  -p '{"spec":{"ccManager":{"enabled":true,"defaultMode":"off"}}}'
# cc-manager DaemonSet appears in namespace gpu-operator and logs:
#   Setting CC mode on GPU 0000:21:00.0 from 'on' to 'off'
#   Verified CC mode 'off' on GPU 0000:21:00.0 / 0000:81:00.0
#   Successfully set CC mode to 'off' on all GPUs
```

Wait for the GPU-operator pods to return to Ready and confirm the GPUs are back on `vfio-pci` with
`nvidia.com/pgpu: 2`. Then the plain Day-3 probe works.

## Verification (Checkpoint 3 — green)

```
$ kubectl exec cp3-probe -- uname -r
6.18.15-nvidia-gpu                 # host is 6.17.0-35-generic → it's a microVM ✓
$ kubectl exec cp3-probe -- nvidia-smi
NVIDIA H100 NVL … 95830 MiB … Driver 590.48.01   # GPU visible inside the microVM ✓
```

Pod reached `Running 1/1` in ~24 s. Day 3 is unblocked; Day 4 (vLLM) uses the same runtime.

## ⚠️ For Sprint 2 (confidential compute): flip it back ON

Sprint 2 needs CC mode back on. Set `defaultMode: on` (this is how the reference box runs):

```bash
kubectl patch clusterpolicy cluster-policy --type merge \
  -p '{"spec":{"ccManager":{"enabled":true,"defaultMode":"on"}}}'
```

Do the flip (either direction) only with **zero GPU pods running** — a careless flip under load
has caused a ~19-minute outage. Never mix CC and non-CC GPUs on one node.

## How the in-guest error was found (reusable technique)

The guest boots `quiet` and NVRC swallows the child's stderr, so nothing useful reaches the kata
log. Two ways to see the truth:

1. **Console socket:** connect to `/run/vc/vm/<sandbox>/console.sock` during boot (no `socat` on
   the box — a small Python `AF_UNIX` reader works). Add `nvrc.log=trace ignore_loglevel
   loglevel=8` to the runtime config's `kernel_params`
   (`/opt/kata/share/defaults/kata-containers/configuration-qemu-nvidia-gpu.toml`) to un-quiet it.
2. **Manual QEMU (best):** boot the guest under your own
   `/opt/kata/bin/qemu-system-x86_64` — dm-verity root from
   `kata-ubuntu-noble-nvidia-gpu-590.48.01.image`, one `vfio-pci` GPU on a `pcie-root-port`,
   `-serial file:/tmp/guest-serial.log`, `ulimit -l unlimited`. You control its lifetime (no
   kubelet churn) and capture the full driver dmesg + NVRC + `nvidia-ctk` error.

Remember to revert any `kernel_params`/`enable_debug` edits and free any loop-mounted image
afterwards.
