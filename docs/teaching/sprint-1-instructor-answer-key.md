# Sprint 1 — Instructor Answer Key

> **Instructor-only.** Concrete commands + manifests for every checkpoint, so you can unblock a
> stuck team in seconds. Pairs with `sprint-1-single-node-serving-skeleton.md`.
>
> **Confidence labels** — be honest with yourself about what's proven:
> - ✅ **VERIFIED** — run end-to-end on our reference box (`secure-puppy`, 4× RTX PRO 6000, AMD, Ubuntu 24.04). Copy with confidence.
> - 🟡 **EXPECTED** — the pre-agreed correct approach, **not yet executed on our box** (we are paused before it). Validate on one box before turning a class loose; record fixes back here.
>
> Hardware will vary (AMD vs Intel, server vs consumer board, GPU size). Treat addresses/ids/sizes as placeholders.

---

## Day 1 — Firmware + host prep ✅ VERIFIED

### Inventory (read-only)
```bash
lspci -nnk | grep -iA3 nvidia          # GPU PCI addresses + device id (ours: 10de:2bb5)
lscpu | grep -i virt                   # AMD-V (svm) / VT-x
ls -l /dev/kvm && lsmod | grep kvm     # KVM present + module loaded
```

### Diagnosing a dead IOMMU (the #1 support ticket)
```bash
ls /sys/kernel/iommu_groups | wc -l    # 0  => IOMMU not active
sudo dmesg | grep -i "AMD-Vi"          # no "Found IOMMU" => firmware, not kernel
```
**Decision rule for the instructor:**
- 0 groups **and** no `Found IOMMU` line → **BIOS**. IOMMU is disabled in firmware. No kernel flag fixes this. Send them to BMC/BIOS.
- `AMD-Vi: Unknown option - 'on'` in dmesg → they added `amd_iommu=on`. Remove it; keep only `iommu=pt`.

### BIOS (BMC/console — cannot be done over SSH)
- **IOMMU** — AMD: `Advanced → AMD CBS → NBIO Common Options → IOMMU = Enabled`. Intel: enable **VT-d**.
- **SEV-SNP** — AMD: `AMD CBS → CPU Common Options →`
  - **SEV-ES ASID Space Limit Control = Manual**, **SEV-ES ASID Space Limit = N** (>0, e.g. 100).
    This is the item people miss: if the ASID pool isn't split, `dmesg` shows
    `kvm_amd: SEV-ES disabled (ASIDs 0-0)` / `SEV-SNP disabled (ASIDs 0-0)` and SNP never turns on.
  - **SEV-SNP Support = Enabled**; confirm **SMEE = Enabled**. (For the later CC milestone.)
- **fTPM / PTT = Enabled** (for the later attestation gate).
- > Notes from our box (verified 2026-06-22, kernel 6.17): enabling base IOMMU brought up base SEV
  > (`kvm_amd: SEV enabled`), and firmware already reserved the **RMP table** + the IOMMU advertises
  > SNP — but **SEV-ES/SNP stayed `ASIDs 0-0`** (ASID split not set) and **fTPM never appeared**.
  > So the second CBS pass is really: the ASID split, SNP Support, and fTPM. **Also** SNP requires the
  > IOMMU **not** be in passthrough mode — see the cmdline note below.

### Kernel cmdline
```bash
sudo cp /etc/default/grub /etc/default/grub.bak
# Edit GRUB_CMDLINE_LINUX (or _DEFAULT): add  iommu=pt   (AMD)
#                                             intel_iommu=on iommu=pt   (Intel)
# Do NOT add amd_iommu=on — invalid on modern kernels.
sudo update-grub
sudo reboot
```
> ⚠️ **`iommu=pt` vs SEV-SNP.** `iommu=pt` is **not required for VFIO** (VFIO sets up its own IOMMU
> domains; we removed it on our box and GPU passthrough still worked, 0 segfaults, GPU in-guest). But
> it **blocks SNP**: with it set, the kernel prints `AMD-Vi: SNP: IOMMU ... passthrough mode, SNP
> cannot be supported`. Removing it cleared that error over SSH (no BIOS trip). **Recommendation for
> a CC-targeted class: skip `iommu=pt` from the start.** It lives in `GRUB_CMDLINE_LINUX` on our box,
> not `_DEFAULT` — check both.

### Bind GPUs to vfio-pci (exact files we used)
`/etc/modprobe.d/vfio.conf`:
```
options vfio-pci ids=10de:2bb5
softdep nouveau pre: vfio-pci
softdep nvidiafb pre: vfio-pci
softdep nvidia pre: vfio-pci
```
`/etc/modprobe.d/blacklist-nvidia-nouveau.conf`:
```
blacklist nouveau
blacklist nvidiafb
```
Append to `/etc/initramfs-tools/modules`:
```
vfio
vfio_iommu_type1
vfio_pci
```
Then:
```bash
sudo update-initramfs -u
sudo reboot
```

### Checkpoint 1 verifier (paste this to grade)
```bash
echo "groups:"; ls /sys/kernel/iommu_groups | wc -l                 # expect > 0  (ours: 51)
sudo dmesg | grep -i "Found IOMMU"                                  # expect a line
for d in 01:00.0 41:00.0 81:00.0 c2:00.0; do                        # use their GPU addresses
  echo -n "$d: "; lspci -nnks $d | grep "driver in use" || echo NONE # expect vfio-pci
done
lsmod | grep nouveau || echo "nouveau not loaded (good)"
# IOMMU group isolation — each GPU should be alone in its group:
for d in 0000:01:00.0 0000:41:00.0 0000:81:00.0 0000:c2:00.0; do
  g=$(basename $(readlink -f /sys/bus/pci/devices/$d/iommu_group))
  echo "=== $d -> group $g ==="; ls /sys/kernel/iommu_groups/$g/devices/
done
```
On our box: 51 groups, all 4 GPUs `vfio-pci`, nouveau absent, each GPU alone in its group (36/48/24/12). **Server boards isolate cleanly; consumer boards often don't** — if a GPU shares a group with unrelated devices, that's a teaching moment about PCIe ACS, not a quick fix.

---

## Day 2 — Stripped k3s + Cilium ✅ VERIFIED

> Run end-to-end on our reference box on 2026-06-18. Resolved versions (pin these for the class):
> **k3s `v1.35.5+k3s1`**, **Helm `v3.21.1`**, **Cilium chart/app `1.19.5`**. Node went `Ready`
> ~20s after Cilium pods came up; all six kube-system pods reached `1/1 Running`; the busybox
> net test returned `NET OK`.

```bash
# k3s with bundled networking/ingress stripped so Cilium + Kata don't fight it
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--flannel-backend=none \
  --disable-network-policy --disable=traefik --disable=servicelb --cluster-init" sh -

# kubeconfig for the student user
mkdir -p ~/.kube && sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config && sudo chown $(id -u):$(id -g) ~/.kube/config
export KUBECONFIG=~/.kube/config

# Helm
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# Cilium (replaces flannel). Pin a chart version in class to keep teams in sync.
helm repo add cilium https://helm.cilium.io/ && helm repo update
helm install cilium cilium/cilium -n kube-system \
  --set operator.replicas=1            # single node
# (Optional Cilium CLI for status: github.com/cilium/cilium-cli)
```

### Checkpoint 2 verifier
```bash
kubectl get nodes                       # Ready
kubectl get pods -A                     # all Running (cilium, cilium-operator, coredns, metrics-server)
kubectl run net-test --image=busybox --restart=Never --rm -it -- wget -qO- https://example.com >/dev/null && echo "net OK"
```
**Common stumbles:** node stuck `NotReady` until Cilium is up (expected — no CNI before it). CoreDNS pending until a CNI exists. If they left flannel on, two CNIs fight → delete the cluster (`/usr/local/bin/k3s-uninstall.sh`) and reinstall with the strip flags.

---

## Day 3 — GPU Operator in VFIO/Kata mode ✅ VERIFIED end-to-end (read the kernel requirement)

> Run on our reference box with **GPU Operator v26.3.2** + **kata-deploy 3.29.0**. As of 2026-06-22
> the GPU **boots inside a Kata microVM via the iommufd VFIO path** (GPU `10de:2bb5` visible in-guest,
> guest kernel `6.18.15-nvidia-gpu`, 0 QEMU segfaults) — **but only after upgrading the host kernel
> to ≥ 6.13** (we run HWE **6.17.0-35**). On the original 6.8 kernel this step crashed; the fix and
> the full diagnosis are in the box below. (Caveat on the final `nvidia-smi`: we confirmed the GPU is
> in-guest via sysfs from a busybox probe; run the CUDA-image probe to tick `nvidia-smi` itself.)

> ⚠️ **v26.x CHANGED THE KATA FLAGS — older guides are wrong.** The widely-copied
> `--set kataManager.enabled=true` path is **deprecated and broken in v26.x**: its image is empty
> in the chart, so ClusterPolicy hangs `notReady` with `empty image path ... KATA_MANAGER_IMAGE`.
> The current path is `--set sandboxWorkloads.mode=kata`, and **Kata itself is installed
> separately via the kata-deploy chart** (the operator no longer ships it).

**Step A — GPU Operator in sandbox/kata mode** (note the k3s containerd-v2 template overrides):
```bash
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia && helm repo update
helm install gpu-operator nvidia/gpu-operator -n gpu-operator --create-namespace --version v26.3.2 \
  --set sandboxWorkloads.enabled=true \
  --set sandboxWorkloads.defaultWorkload=vm-passthrough \
  --set sandboxWorkloads.mode=kata \
  --set ccManager.defaultMode=off \
  --set toolkit.env[0].name=CONTAINERD_CONFIG \
  --set toolkit.env[0].value=/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl \
  --set toolkit.env[1].name=CONTAINERD_SOCKET \
  --set toolkit.env[1].value=/run/k3s/containerd/containerd.sock
```
This deploys `vfio-manager`, `nvidia-kata-sandbox-device-plugin`, `cc-manager`, NFD. It does **not**
create the `kata-qemu-nvidia-gpu` runtimeClass — that comes from kata-deploy. `ccManager.defaultMode=off`
because our box has base SEV but not SEV-SNP; leave it `off` for any non-confidential spike.
On our box ClusterPolicy reached `ready` and the GPUs were advertised as **both**
`nvidia.com/pgpu: 4` and `nvidia.com/GB202GL_RTX_PRO_6000_BLACKWELL_SERVER_EDITION: 4`.

**Step B — install Kata via kata-deploy** (creates `/opt/kata`, registers a containerd-v2 drop-in,
and creates all kata runtimeClasses including `kata-qemu-nvidia-gpu`):
```bash
helm install kata-deploy oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy \
  --namespace kata-system --create-namespace \
  --set k8sDistribution=k3s \
  --set nfd.enabled=false \
  --version 3.29.0 --wait --timeout 10m
```
`k8sDistribution=k3s` is essential — it makes kata-deploy write to k3s's
`config-v3.toml.d/kata-deploy.toml` drop-in and restart k3s, instead of the standard
`/etc/containerd` path. The NVIDIA-optimized guest ships here too:
`kata-containers-nvidia-gpu.img → kata-ubuntu-noble-nvidia-gpu-590.48.01.image` (driver baked in)
and `vmlinuz-6.18.15-189-nvidia-gpu`. So the in-guest driver question is **handled** by kata-deploy.

Confirm:
```bash
kubectl get runtimeclass | grep kata-qemu-nvidia-gpu   # expect it to exist
sudo ls /opt/kata/bin/                                 # qemu-system-x86_64, kata-runtime, shim
kubectl get node -o jsonpath='{.items[0].status.capacity}' | tr ',' '\n' | grep pgpu  # nvidia.com/pgpu
```

### Probe pod — `day3-probe.yaml`
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-probe
  annotations:
    cdi.k8s.io/gpu: "nvidia.com/pgpu=0"     # cold-plug VFIO needs the CDI annotation
spec:
  runtimeClassName: kata-qemu-nvidia-gpu
  restartPolicy: Never
  containers:
    - name: probe
      image: nvidia/cuda:12.4.1-base-ubuntu22.04
      command: ["bash","-lc","uname -r; echo '---'; nvidia-smi; sleep 3600"]
      resources:
        limits:
          nvidia.com/pgpu: 1
```

### Checkpoint 3 verifier (the real milestone)
```bash
kubectl apply -f day3-probe.yaml
kubectl wait --for=condition=Ready pod/gpu-probe --timeout=300s
echo "HOST kernel:"; uname -r
echo "GUEST kernel:"; kubectl exec gpu-probe -- uname -r      # MUST differ from host => microVM
kubectl exec gpu-probe -- nvidia-smi                          # MUST list the GPU
```
**Diagnosis ladder (we used exactly this):**
- Pod stuck `ContainerCreating` with `FailedCreatePodSandBox ... create container timeout` → the
  Kata VM isn't booting. Get the real cause from the journal:
  `sudo journalctl --since "5 min ago" | grep -iE "qemu|kata" | grep -iE "error|segfault|exited"`.
- **First isolate Kata from the GPU:** run a plain `kata-qemu` pod (busybox, *no* `nvidia.com/pgpu`).
  If it boots (guest kernel e.g. `6.18.15`) → Kata is fine and the problem is the **GPU passthrough**,
  not Kata. (This is how we localized our blocker.)
- **`qemu-system-x86_64: segfault` on attach → check `uname -r` FIRST.** On kernel **< 6.13** the
  iommufd GPU attach (`IOMMU_VDEVICE_ALLOC`) returns `ENOTTY`, QEMU mishandles it and crashes. Upgrade
  to a ≥6.13 / HWE kernel and reboot. This was our actual root cause — *not* the GPU's BAR size.
- `uname -r` matches the host → not actually Kata; check `runtimeClassName` + the RuntimeClass exists.

**Getting the real error behind a bare QEMU `segfault` (advanced — this is how we found it):**
The journal only shows `segfault at 0`. To recover the masked cause without source access:
```bash
CFG=/opt/kata/share/defaults/kata-containers/runtimes/qemu-nvidia-gpu/configuration-qemu-nvidia-gpu.toml
# 1) Find the qemu binary + machine type kata uses:
sudo grep -nE '^path = |^machine_type' "$CFG"
# 2) Reproduce standalone under gdb (strip fd-based devices; keep pcie-root-port + iommufd + vfio-pci):
sudo apt-get install -y gdb strace
sudo gdb -batch -ex run -ex bt --args /opt/kata/bin/qemu-system-x86_64 \
  -machine q35,accel=kvm -cpu host,pmu=off -m 8192M,maxmem=257440M \
  -device pcie-root-port,id=rp0,bus=pcie.0,chassis=0,slot=0 \
  -object iommufd,id=iommufd0 \
  -device vfio-pci,host=<GPU_BDF>,bus=rp0,iommufd=iommufd0 \
  -display none -nographic -no-user-config -nodefaults -S
# bt showed: error_prepend(util/error.c) <- vfio_pci_realize <- iommufd_cdev_attach returning false w/ NULL errp.
# 3) strace the same run to see the failing syscall + errno:
sudo strace -f -e trace=ioctl -o /tmp/q.log /opt/kata/bin/qemu-system-x86_64 ...same args...
sudo grep '= -1' /tmp/q.log | grep -i iommu
# -> ioctl(/dev/iommu, ...0x92...) = -1 ENOTTY  (0x92 = IOMMUFD_CMD_VDEVICE_ALLOC, kernel <6.13)
```
The lesson for students: a "segfault" with no message is not a dead end — isolate the failing
component, reproduce it minimally under gdb for a symbolic backtrace, and strace for the failing
syscall/errno. That turned "QEMU crashes" into "kernel too old" in two commands.

> ### ⚠️ The real Day-3 blocker: host kernel must be ≥ 6.13 (corrected 2026-06-22)
> On our **RTX PRO 6000 Blackwell**, the plain `kata-qemu` pod booted fine but the
> `kata-qemu-nvidia-gpu` pod made **QEMU segfault on startup**. We *initially* blamed the GPU's
> **128 GB 64-bit BAR** overflowing the guest MMIO aperture — **that theory was WRONG.** We injected
> `-global q35-pcihost.pci-hole64-size=256G`, confirmed it reached QEMU, and it still crashed at the
> identical offset. The real root cause (found via gdb + strace, above):
> - QEMU 10.2's iommufd VFIO realize path calls **`IOMMU_VDEVICE_ALLOC`** (a vIOMMU/vdevice ioctl).
> - That ioctl **merged in Linux 6.13**. Our host ran **6.8** → `ENOTTY` → `iommufd_cdev_attach()`
>   returns false **without setting an error** → `vfio_pci_realize` calls `error_prepend` on a NULL
>   error → **SIGSEGV**. The bare "segfault" masked the real failure.
> - **This is GPU-size-independent** — any GPU on this Kata/QEMU stack + a <6.13 kernel hits it.
>
> **Fix (verified):** upgrade the host kernel and reboot, then re-run the probe.
> ```bash
> sudo apt update && sudo apt install --install-recommends linux-generic-hwe-24.04   # -> 6.17 on 24.04
> sudo reboot
> uname -r                                  # >= 6.13 (ours: 6.17.0-35-generic)
> # vfio binding + GPU Operator + kata survive the reboot (initramfs carries /etc/modprobe.d/vfio.conf).
> ```
> After 6.17 the `kata-qemu-nvidia-gpu` probe reached **Running, 0 segfaults**, GPU visible in-guest.
> Teaching point: don't over-fit to a plausible story (the BAR size *looked* guilty) — verify it.
> The cheap experiment (inject the hole-64 flag) falsified it in minutes and redirected us to the kernel.

---

## Day 4 — vLLM-in-Kata spike ✅ VERIFIED end-to-end (2026-06-23, reference box)

Model: **Mistral-Small-3.1 (FP8)**, Apache-2.0. Smaller boxes: substitute a smaller Apache-2.0
model and have them record the swap. Simplest weight staging for Sprint 1 = a host path /
hostPath PVC; the signed-OCI path is a later milestone, not this sprint.

> **Verified result (RTX PRO 6000 Blackwell, ~96 GB, kernel 6.17, vLLM 0.11.1):** a coherent
> `/v1/chat/completions` response served from inside the Kata+VFIO microVM at **~33.8 tokens/sec**
> single-stream (256-token generation, FP8). sm_120 worked first try — the failures we hit were
> two config gotchas, not GPU/Blackwell issues (see below).

### `day4-vllm.yaml` (the validated manifest lives in the repo at `manifests/day4-vllm.yaml`)
Key points that differ from a naive first draft — **all three bit us, none were GPU-related**:
```yaml
apiVersion: v1
kind: Pod
metadata: { name: vllm, labels: { app: vllm } }
spec:
  runtimeClassName: kata-qemu-nvidia-gpu
  restartPolicy: Never
  containers:
    - name: vllm
      image: vllm/vllm-openai:v0.11.1        # PIN it. matches NVIDIA's CUDA-13.1 vLLM (25.12). never :latest
      args:
        - "--model=mistralai/Mistral-Small-3.1-24B-Instruct-2503"
        - "--served-model-name=mistral-small-3.1"
        - "--quantization=fp8"
        - "--max-model-len=32768"            # ~96 GB VRAM has headroom; raise from 8192
        - "--gpu-memory-utilization=0.90"
        - "--host=0.0.0.0"
        - "--port=8000"
      env:
        - name: HF_TOKEN
          valueFrom: { secretKeyRef: { name: hf-token, key: token } }
        - name: VLLM_ATTENTION_BACKEND       # GOTCHA 1: this is an ENV VAR, not a serve CLI flag.
          value: "FLASH_ATTN"                #  FlashInfer JIT is broken on sm_120 (CUDA-13 wheels).
      resources:
        limits: { nvidia.com/pgpu: 1, memory: 32Gi, cpu: "8" }   # Kata sizes the VM from these; 8Gi default is too small
        requests: { memory: 32Gi, cpu: "4" }
      volumeMounts: [{ name: dshm, mountPath: /dev/shm }]
  volumes:
    - name: dshm
      emptyDir: { medium: Memory, sizeLimit: 8Gi }
---
apiVersion: v1
kind: Service
metadata: { name: vllm-svc }                 # GOTCHA 2: NOT "vllm". A svc named "vllm" makes k8s
spec:                                         #  inject VLLM_PORT=tcp://... which vLLM mis-reads as
  selector: { app: vllm }                     #  its own config (wants an int) -> engine core crashes.
  ports: [{ port: 8000, targetPort: 8000 }]
```
```bash
kubectl create secret generic hf-token --from-literal=token=$HF_TOKEN   # model is gated
kubectl apply -f manifests/day4-vllm.yaml
kubectl wait --for=condition=Ready pod/vllm --timeout=1800s   # ~14 GB image + ~90 GB weights pull = slow
```

> **GOTCHA 3 — patience on the load.** `kubectl logs` looks frozen at `Starting to load model` for
> 10–20 min: no TTY → no HF progress bars. It's downloading, not hung — `kubectl exec vllm -- du -sh
> /root/.cache/huggingface` shows it climbing. The Mistral-Small-3.1 repo ships **both** consolidated
> and sharded weights, so the pull is ~90 GB (≈2× the bf16 size). It's ephemeral pod storage, so a
> restart re-downloads — back it with a PVC for anything past a spike.

### Checkpoint 4 verifier (the win)
```bash
# from inside the pod (no port-forward needed):
kubectl exec vllm -- curl -s http://localhost:8000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"mistral-small-3.1",
       "messages":[{"role":"user","content":"In one sentence, what is confidential computing?"}]}' | jq .
```
Measure **tokens/sec** with a timed 256-token request (`completion_tokens / wall_time`), or read the
periodic `Avg generation throughput` line from `kubectl logs vllm`. Our reference run: **~33.8 tok/s**
single-stream — note this is FLASH_ATTN (FlashInfer unavailable on sm_120) and unbatched, so it's a
floor, not the ceiling.

**Common stumbles:** OOM on small GPUs (lower `--max-model-len`, smaller model, confirm FP8 is
actually engaged); slow first request = weights still loading (watch the cache size, not the logs);
gated model = missing/invalid `HF_TOKEN` or unaccepted license; `--attention-backend` rejected =
it's an env var; engine-core crash on `VLLM_PORT ... appears to be a URI` = service named `vllm`.

---

## Fast triage table

| Symptom | Most likely cause | Fix |
|---|---|---|
| 0 IOMMU groups, no `Found IOMMU` | IOMMU off in BIOS | BMC/BIOS, enable IOMMU/VT-d |
| `AMD-Vi: Unknown option - 'on'` | `amd_iommu=on` on cmdline | remove it — AMD needs no `iommu=` flag once firmware IOMMU is on (Intel: `intel_iommu=on`) |
| Kata GPU pod: `create container timeout`, `qemu-system-x86_64: segfault` | host kernel < 6.13 (no `IOMMU_VDEVICE_ALLOC` for iommufd) | upgrade kernel (`linux-generic-hwe-24.04` → 6.17), reboot |
| `SNP: IOMMU ... passthrough mode, SNP cannot be supported` | `iommu=pt` set | remove `iommu=pt` from cmdline (not needed for VFIO) |
| `kvm_amd: SEV-SNP disabled (ASIDs 0-0)` | ASID pool not split in CBS | BMC: SEV-ES ASID Space Limit = Manual, N>0; SEV-SNP Support = Enabled |
| GPU `driver in use: nouveau` | nouveau won the boot race | blacklist nouveau + vfio in initramfs, reboot |
| node `NotReady`, CoreDNS pending | no CNI yet | install Cilium; wait for it |
| host `nvidia-smi` works | installed default (host-driver) operator mode | reinstall with sandboxWorkloads/vfio/kata, `driver.enabled=false` |
| pod `uname` == host kernel | not actually Kata | set `runtimeClassName: kata-qemu-nvidia-gpu` |
| `nvidia-smi` fails in pod | GPU not passed in | use `nvidia.com/pgpu`, confirm vfio-pci + kata-manager ready |
| vLLM OOM / won't load | GPU too small / FP8 not engaged | smaller model, lower `--max-model-len`, verify quantization |
| vLLM `unrecognized arguments: --attention-backend` | it's not a serve CLI flag | set env `VLLM_ATTENTION_BACKEND=FLASH_ATTN` (FlashInfer is broken on sm_120) |
| vLLM engine-core crash: `VLLM_PORT 'tcp://...' appears to be a URI` | Service named `vllm` → k8s injects `VLLM_PORT` | rename the Service (e.g. `vllm-svc`) so it doesn't shadow vLLM's own env var |
| `kubectl logs` stuck at `Starting to load model` for 10–20 min | silent HF weight download (no TTY) | not hung — watch `du -sh ~/.cache/huggingface` grow; ~90 GB pull for this repo |

---

## Instructor prep checklist (do this before Day 1)

- [x] Run **Day 4 (vLLM) yourself** — done on the reference box 2026-06-23 (Checkpoint 4 ✅, ~33.8 tok/s, vLLM 0.11.1). Re-validate on a student-class box if its GPU/VRAM differs.
- [ ] **Verify every class box is on kernel ≥ 6.13** before Day 3 (`uname -r`; on 24.04 install `linux-generic-hwe-24.04`). A <6.13 kernel segfaults the Kata GPU attach — the #1 Day-3 wall, hardware-independent.
- [ ] **Pin versions** (k3s channel, Cilium chart, GPU Operator chart, vLLM image tag) so all teams hit the same behavior — float versions = divergent bugs.
- [ ] Confirm the class GPUs **isolate cleanly in their own IOMMU groups** (server boards yes, consumer boards often no). If not, prepare the ACS-override discussion or pick different hardware.
- [ ] Pre-stage / mirror model weights if bandwidth is tight (24B FP8 is a large pull × N teams).
- [ ] Decide whether teams self-serve BIOS/BMC or you do the firmware pass for them.
