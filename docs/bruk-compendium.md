# Bruk Platform — Learning Compendium

A reference guide covering everything learned so far in the Airon Bruk sovereign AI infrastructure curriculum.

---

# Phase 1: The Physical and Hardware Foundation

## Topic 1: Bare-Metal Server Architecture

### The key mental model

A regular server is built around the CPU — everything flows through it. A GPU server flips this: the GPUs are the real workers and the entire physical design exists to feed them as fast as possible. The CPU is just a manager — loading data, launching GPU kernels, orchestrating.

### What's inside an AI server

A 4U–8U rackmount chassis containing:

- **1–2 CPU sockets** — typically AMD EPYC. EPYC wins over Intel Xeon for GPU servers because it provides more PCIe lanes (128 per socket), meaning more bandwidth highways to GPUs.
- **8 GPUs** — the standard NVIDIA reference configuration (DGX/HGX platforms), each with its own heatsink or liquid cooling.
- **1–2 TB DDR5 system RAM** — mostly for staging data before it's pushed to GPUs.
- **NVMe SSDs** — for the OS and model weight storage.

### PCIe topology

PCIe (Peripheral Component Interconnect Express) is the physical bus connecting components inside the server. Each GPU needs 16 PCIe lanes. With 128 lanes per CPU socket and 8 GPUs, the physical layout of which GPU connects to which socket matters for latency.

Key insight: GPU-to-GPU communication should avoid PCIe when possible. PCIe bandwidth is ~64 GB/s per x16 slot. NVLink provides ~900 GB/s — a 14x difference.

### BMC (Baseboard Management Controller)

A tiny, always-on computer embedded on the motherboard, accessed via IPMI. It provides:

- Remote power on/off
- Console access even when the OS is dead
- Temperature, fan speed, and voltage monitoring
- Firmware updates

The BMC sits on a **separate, isolated management VLAN** because it has god-level access to the server. If an attacker compromises the BMC, they own the machine entirely. This network isolation is a security fundamental in Bruk's design.

---

## Topic 2: GPU Hardware — H200, B200, B300

### HBM (High Bandwidth Memory)

GPUs have their own memory (HBM) sitting directly on the chip package via a silicon interposer — physically millimeters from the compute cores, not inches away across a circuit board like system RAM.

| | DDR5 System RAM | HBM3e (H200) | HBM3e (B200/B300) |
|---|---|---|---|
| Bandwidth | ~90 GB/s per channel | ~4.8 TB/s | ~8 TB/s |
| Capacity | 1–2 TB total | 141 GB per GPU | 192–288 GB per GPU |

### Memory-bandwidth bound vs. memory-capacity bound

These are two different problems:

- **Memory-bandwidth bound** — the GPU is waiting for data to be *read from memory* faster than HBM can deliver it. During token generation, the GPU reads the entire model's weights for every single token but does little math per weight. The compute cores sit idle waiting for data. The bottleneck is the *speed of the pipe*, not the *size of the tank*.
- **Memory-capacity bound** — the model simply doesn't fit in the available VRAM. This is a *size* problem solved by adding more GPUs.

LLM token generation is memory-bandwidth bound. HBM bandwidth directly determines token generation speed.

### Tensor cores vs. CUDA cores

- **Tensor cores** — specialized circuits that do matrix multiply-accumulate on small matrices (4×4 or 8×8) in a single clock cycle. Every transformer layer is a chain of matrix multiplications, so tensor cores handle the heavy lifting.
- **CUDA cores** — general-purpose cores that handle everything else: activation functions (ReLU, SiLU), softmax, layer normalization, sampling, data reshaping. Still necessary for all the non-matrix math.

### The three GPUs

| GPU | VRAM | Key significance |
|---|---|---|
| H200 | 141 GB HBM3e | Current generation. Llama 70B in FP16 (~140 GB) barely fits on one card. 405B needs multiple cards. |
| B200 | 192 GB HBM3e | Next gen (Blackwell). ~2x inference performance of H200. |
| B300 | 288 GB HBM3e | Further evolution. A 405B model in FP8 (~405 GB) fits on just 2 cards instead of 3–4 H200s. |

### Why VRAM sizing matters architecturally

VRAM per GPU determines how many GPUs you need → which determines how much inter-GPU communication is required → which determines how important NVLink and RDMA are. Everything connects.

---

## Topic 3: NVLink and Intra-Node GPU Communication

### What NVLink is

A direct physical interconnect between GPUs — dedicated wires on the baseboard that bypass PCIe entirely. NVLink 4th gen (Hopper/H200) provides 900 GB/s bidirectional bandwidth across all 8 GPUs.

### NVSwitch

A crossbar switch that sits between all 8 GPUs. Without it, you'd need 28 separate point-to-point connections for 8 GPUs. With NVSwitch, any GPU can talk to any other GPU at full bandwidth simultaneously. All 8 GPUs effectively share a single flat memory pool.

Important scope precision: NVSwitch is **intra-node** (inside one server), NOT intra-rack.

### Why it matters for inference

When a model is sharded across multiple GPUs (tensor parallelism), each GPU holds a slice of each layer. After every layer computation, GPUs must exchange partial results before the next layer can begin. This synchronization happens for every token generated.

- Over PCIe at 64 GB/s → GPUs sit idle waiting → high latency per token
- Over NVLink at 900 GB/s → exchange is nearly instant → GPUs stay busy

### The critical boundary

- **Intra-node (inside one server)** = NVLink — fast
- **Inter-node (between servers)** = Network RDMA — slower but still fast

If a model needs more than 8 GPUs, NVLink can't help across servers. That's where RDMA and Spectrum-X come in (Phase 2).

---

## Topic 4: BlueField-3 DPU / SuperNIC

### The problem

In a GPU server, the CPU is already busy orchestrating GPU work. Piling networking tasks (packet processing, encryption, firewall rules, RDMA setup) onto it steals cycles from the real job.

### What a DPU is

A small computer on a network card. The BlueField-3 has its own ARM cores, its own memory, and its own OS. It sits between the server and the network and handles:

- **RDMA** — direct memory transfers without CPU involvement
- **Encryption** — line-rate hardware encryption, no CPU overhead
- **Network isolation** — tenant separation and firewall rules in hardware
- **Storage offload** — NVMe-over-Fabrics if needed

### The analogy

The CPU is a manager. The GPUs are specialists. The DPU is the mailroom and security desk — handles all communication, checks credentials, encrypts envelopes, routes packages, so the manager and specialists are never interrupted.

### Security significance for Bruk

The DPU enforces network policy in hardware. Even if the host OS is compromised, the DPU still enforces isolation. It's a separate trust boundary — critical for a sovereign platform.

---

## Phase 1 — The big picture

Inside a server, the data flow for an inference request:

1. Request hits the **network switch**
2. Arrives at the **DPU** → decrypts, checks policies, passes through
3. **CPU** receives it → determines which GPUs handle it, loads prompt into GPU memory
4. **GPUs** run the model layer by layer, exchanging partial results over **NVLink** after each layer
5. Output tokens flow back out the reverse path

Each layer protects and feeds the next one.

---

# Phase 2: Networking and Cluster Fabric

## Topic 5: Ethernet vs InfiniBand for AI Clusters

### The two options

| | InfiniBand | Ethernet |
|---|---|---|
| Latency | ~1 μs | ~2–5 μs (historically) |
| RDMA | Native, built-in | Requires RoCEv2 (add-on) |
| Ecosystem | Specialized, NVIDIA-dominated | Universal, multi-vendor |
| Cost | Expensive, fewer vendors | Competitive, many vendors |
| Talent pool | Small, niche expertise | Huge, everyone knows Ethernet |

### Why Bruk chose Ethernet

This is a **sovereignty decision**. "Sovereign" doesn't just mean data stays in the country — it means you control every layer of the stack. InfiniBand means NVIDIA switches, firmware, management tools, and NVIDIA-trained engineers. If NVIDIA changes licensing, raises prices, or deprioritizes support, you're stuck.

Ethernet gives you: multi-vendor switch options, massive talent pool, standard tooling, no lock-in. InfiniBand's performance edge has also shrunk — Spectrum-X with RoCEv2 gets close enough that sovereignty and flexibility outweigh the last few percent of latency.

### RoCEv2 (RDMA over Converged Ethernet)

Pronounced "rocky v2." Layers RDMA protocol on top of standard Ethernet, giving InfiniBand-like direct memory access semantics over Ethernet switches.

The catch: RDMA doesn't handle packet loss gracefully (it bypasses the kernel and TCP entirely). So Ethernet must be configured as **lossless** — packets are never dropped.

### Lossless Ethernet mechanisms

- **PFC (Priority Flow Control)** — the emergency brake. Tells an upstream switch "stop sending, my buffer is almost full." Effective but can cause head-of-line blocking.
- **ECN (Explicit Congestion Notification)** — the early warning system. A 2-bit field in the IP packet header. When a switch buffer starts filling, it flips the ECN bits to "congestion experienced" on passing packets. The receiver notifies the sender, who slows down. The packet still gets delivered — it just carries a warning flag. Proactive, not reactive.

ECN is preferred because it's gradual. PFC is the emergency fallback.

---

## Topic 6: RDMA Fundamentals

### Normal networking vs. RDMA

**Normal networking** — the path of a data transfer:
1. Application asks kernel to send data
2. Kernel copies data from app memory → kernel buffer
3. Kernel passes to NIC
4. NIC sends across the wire
5. Remote NIC receives, hands to kernel
6. Kernel copies from kernel buffer → app memory
7. Application is notified

Multiple copies, CPU involved at every step on both sides.

**RDMA:**
1. Application tells the NIC "write this data directly into memory address X on Server B"
2. The NIC does it. Done.

This is **zero-copy, kernel-bypass** networking. No kernel involvement, no CPU involvement, no extra memory copies.

### Why RDMA matters for Bruk

In disaggregated inference, work is split between prefill workers (process the input prompt, compute-heavy) and decode workers (generate output tokens, memory-bandwidth-heavy). After prefill finishes, the KV-cache (the model's "memory" of the prompt) must transfer from the prefill server to the decode server. This cache can be gigabytes.

RDMA lets the prefill server write the KV-cache directly into the decode server's GPU memory over the network, almost as if it were local memory. This is what makes disaggregated inference practical.

### The three RDMA operations

| Operation | Description | Use case |
|---|---|---|
| RDMA Write | "I push data into your memory." Remote CPU never knows. | KV-cache transfer — prefill pushes cache to decode worker |
| RDMA Read | "I pull data from your memory." Remote CPU uninvolved. | Less common in critical paths |
| RDMA Send/Receive | Both sides coordinate, more like traditional messaging. | Control messages |

---

## Topic 7: NVIDIA Spectrum-X and DOCA

### What Spectrum-X is

NVIDIA's complete Ethernet networking platform optimized for AI. Not a single product — it combines three components:

1. **Spectrum-4 switches** — physical network switches in the rack
2. **BlueField-3 DPUs/SuperNICs** — on each server
3. **Software stack** — adaptive routing, congestion control, telemetry

NVIDIA's answer to "you want Ethernet instead of InfiniBand? Fine, we'll make Ethernet behave like InfiniBand."

### Adaptive routing

Normal Ethernet: packets take a fixed path. If congested, packets queue and latency spikes.

Spectrum-X: monitors congestion across all paths in real time and redirects packets along less-congested routes, **per-packet**. This matters because AI traffic is **bursty** — after every layer computation, all GPUs blast data at the same microsecond. Adaptive routing spreads that burst across multiple paths.

### Congestion control

Spectrum-X implements a hardware-speed congestion control loop:

1. ECN — switches mark packets when buffers start filling (early warning)
2. Receiver sees ECN marks, notifies the sender
3. Sender slows down before buffer overflows

This keeps the network lossless without relying on PFC's heavy-handed link pausing, avoiding head-of-line blocking.

### DOCA (Data Center Infrastructure on a Chip Architecture)

NVIDIA's SDK for programming BlueField DPUs. Lets Bruk engineers write custom networking and security logic that runs on the DPU, not the host CPU:

- Custom packet filtering and firewall rules
- Telemetry and flow monitoring
- Encryption policies
- Storage virtualization

For Bruk: security and network policies enforced in DPU hardware — the host OS never gets a vote. Even if the host is compromised, the DPU still enforces isolation.

---

## Phase 2 — The big picture

- Inside a server, GPUs talk over **NVLink** (fast, ~900 GB/s)
- Between servers, they talk over **RDMA on Spectrum-X Ethernet** (fast enough, sovereign)
- The **DPU** handles network traffic without burdening the CPU
- **Lossless Ethernet** (PFC + ECN) prevents packet drops that would break RDMA
- **Adaptive routing** handles the bursty traffic patterns unique to AI workloads
- Every layer is designed to keep data flowing to the GPUs as fast as possible

---

# Phase 3: Operating System and Boot Security

The shift: Phases 1–2 were about hardware and networking. Phase 3 asks — how do you know the software running on all that hardware hasn't been tampered with?

## Topic 8: UEFI, Secure Boot, and Measured Boot

### The boot chain

When a Bruk server powers on:

1. **UEFI firmware** — first code to run, burned into motherboard flash. Initializes hardware.
2. **Shim** — small first-stage bootloader signed by Microsoft's UEFI certificate authority. Bridge to custom-signed Linux distributions.
3. **GRUB** — the actual bootloader. Loads kernel and initial ramdisk.
4. **Linux kernel** — the OS core.
5. **initrd** — initial ramdisk with early drivers and scripts to mount the real root filesystem.

### Secure Boot — prevention

Uses **digital signatures** (not hashes) to verify each boot stage. UEFI firmware has a database of trusted signing keys baked in. Before executing each stage, it checks: "Is this signed by a key I trust?" If not, boot is refused.

An attacker who replaces the kernel can't sign it with the correct private key, so the machine refuses to execute it.

Key distinction: signatures prove the code was authorized by a **specific trusted signer**. Anyone can compute a hash, but only the holder of the private key can produce a valid signature.

### Measured Boot — detection

At each boot stage, a **cryptographic hash** of the code about to execute is calculated and stored in the **TPM** (Topic 9) in specific **PCR registers**:

- PCR 0 → UEFI firmware hash
- PCR 4 → bootloader hash
- PCR 8–9 → GRUB configuration and kernel hash

PCR registers can only be **extended** (new measurements added), never overwritten or reset without rebooting. By the time the OS is running, the TPM contains an unforgeable record of every piece of code that executed during boot.

### Secure Boot vs. Measured Boot

They're complementary:

- **Secure Boot** = prevention. Blocks unauthorized code from running.
- **Measured Boot** = detection. Records what ran so it can be verified later by a remote party (remote attestation).

Bruk uses both.

---

## Topic 9: TPM 2.0 and Attestation

### What a TPM is

A **Trusted Platform Module** — a dedicated security chip soldered to the motherboard. Separate hardware from the CPU, with its own processor, secure storage, and firmware. A tamper-proof safe bolted to the server.

It can: store cryptographic keys that never leave the chip, hold PCR measurements, and sign statements ("these are my measurements") in a way that can't be forged.

### PCR extend operation

```
new_PCR_value = hash(old_PCR_value + new_measurement)
```

A one-way chain. Can't work backward to fake intermediate steps. Can't reset without rebooting.

### Remote attestation

How Bruk verifies 200 servers haven't been tampered with:

1. Central **attestation service** sends a challenge to a node: "Prove what you booted."
2. Node's TPM signs its PCR values with a key only that TPM holds.
3. Attestation service compares signed PCR values against **known-good expected values**.
4. Match → node is trusted. Mismatch → node is quarantined.

The attacker's dilemma: can't forge the TPM signature (key never leaves hardware), can't manipulate PCR values (extend-only). The only way to pass attestation is to actually boot the approved software.

### Critical rule for Bruk

Before a node gets any secrets — encryption keys, model weights, API credentials — it must pass remote attestation. Untrusted nodes are locked out.

---

## Topic 10: Immutable OS Images

### The problem with traditional Linux

Read-write filesystems allow drift: admins edit configs, install packages, apply one-off fixes. Each server becomes slightly different. Security risk (root can install backdoors that persist) and reproducibility nightmare.

### Bruk's approach: OS as read-only appliance

You don't patch a router by SSH-ing in and running `apt upgrade`. You flash a new image. Same idea.

### dm-verity — runtime tamper detection

A Linux kernel feature that builds a **hash tree (Merkle tree)** of every block on disk. When the kernel reads any block, dm-verity recalculates its hash and checks against the tree. A single modified byte on the filesystem causes a read failure.

Key distinction from Secure Boot: Secure Boot verifies at **boot time** only. dm-verity protects at **runtime** — every disk read is hash-checked in real time. Catches tampering that happens after boot.

### A/B image updates

Two OS partitions. Running system boots from A. New image written to B. Next reboot boots from B. If B fails, automatic fallback to A.

Updates are **atomic** — fully applied or not applied at all. Never halfway through a patch.

### UKI (Unified Kernel Images)

Bundles kernel, initrd, and kernel command line into a **single signed file**. One signature check instead of multiple. Eliminates attacks where someone swaps just one component (e.g., replacing initrd while leaving kernel legitimate).

---

## Topic 11: Hardened Ubuntu and CIS Benchmarks

### CIS Benchmarks

Center for Internet Security configuration guides. CIS Level 2 (Bruk's target) is the stricter tier — may reduce usability but provides stronger security. Covers: disabling unused filesystems/protocols, strict file permissions, authentication policies, network parameter lockdown, audit logging, removing unnecessary packages.

### FIPS-validated kernels

FIPS 140-3 is the US federal standard for cryptographic modules. The kernel's cryptographic implementations (AES, SHA, RSA) are formally tested and certified. Sovereign customers often require FIPS validation as a procurement condition.

### AppArmor in enforce mode

Confines each program to a limited set of resources (files, network access, system calls). Two modes: complain (logs violations but allows) and enforce (blocks violations). Bruk runs enforce mode — compromised processes are genuinely confined.

### Minimal package set

No compiler, no package manager in production, no debugging tools. If an attacker gets shell access, there's almost nothing useful on the system.

### How security layers compound

- **Secure Boot** → only signed code executes at boot
- **Measured Boot + TPM** → unforgeable boot record, verified by remote attestation
- **dm-verity** → filesystem can't be modified at runtime
- **CIS hardening** → minimal attack surface, strict permissions
- **FIPS kernel** → provably correct cryptography
- **AppArmor enforce** → processes confined even if compromised

No single layer is unbreakable. An attacker must defeat all of them.

---

## Phase 3 — The big picture

Boot integrity (Secure Boot + Measured Boot) → hardware-anchored proof (TPM + attestation) → tamper-proof filesystem (dm-verity + A/B updates) → minimal, locked-down OS (CIS hardening + FIPS + AppArmor).

A Bruk node proves what it booted, can't be modified while running, and confines every process. Secrets are only released to nodes that pass full verification.

---

# Phase 4: Containers, Kubernetes, and Kata (in progress)

## Topic 12: Container Fundamentals

### What a container actually is

Not a virtual machine. A regular Linux process with two isolation mechanisms:

- **Namespaces** — the process sees its own version of system resources (PID namespace: own process tree; network namespace: own IP address; mount namespace: own filesystem layout; user namespace: thinks it's root while being unprivileged on host).
- **Cgroups (control groups)** — limit resource consumption: CPU time, memory, disk I/O, network bandwidth.

### The key insight

Containers share the **host kernel**. One Linux kernel running, every container makes system calls directly to it.

- **Strength** — no overhead of a separate OS. Start in milliseconds, minimal resources, near-native performance.
- **Weakness** — the kernel is a shared attack surface. A kernel vulnerability can be exploited by any container to escape onto the host. No hardware boundary — just software rules.

Important clarification: a "Ubuntu container" doesn't run Ubuntu's OS. It packages Ubuntu's **userspace** (tools, libraries, binaries) but uses the host kernel. The Docker image is a filesystem snapshot, not a bootable OS.

---

## Topic 13: Kata Containers Deep Dive

### The problem Kata solves

Regular containers: fast but weak isolation (shared kernel). VMs: strong isolation (separate kernel, hardware boundary) but heavy. Kata gives you both.

### Container vs. VM — the fundamental difference

A container is isolated by **software rules the kernel enforces on itself**. A VM is isolated by **CPU hardware** (Intel VT-x, AMD-V) that creates a separate execution environment. A kernel vulnerability breaks container isolation but doesn't break VM isolation — the CPU hardware enforcer isn't compromised by software bugs.

### Kata architecture

When Kubernetes schedules a pod with `runtimeClassName: kata-qemu`:

1. **QEMU/KVM** creates a lightweight microVM with a tiny guest kernel
2. **kata-agent** runs inside the microVM, receives instructions from the host
3. Container workload runs inside the microVM
4. Host-to-VM communication uses **vsock** (direct channel, not network stack)

Kubernetes sees a container. The workload thinks it's a container. But underneath there's a hardware-enforced VM boundary.

### GPU passthrough with VFIO

**VFIO (Virtual Function I/O)** maps a physical GPU's hardware registers and memory directly into the microVM's address space. The GPU talks to the microVM as if there's no hypervisor. Near-native performance with VM-level security.

### Why Kata's overhead is acceptable for Bruk

Inference workloads are long-lived (model loaded once, serves for hours/days). A few extra seconds at startup is negligible. The security boundary is non-negotiable for a sovereign platform.

### Practical example: GPU passthrough setup

In a lab environment with 2 GPUs:

- **GPU 41:00.0** → bound to NVIDIA driver → host/Docker access (testing and validation)
- **GPU 81:00.0** → bound to VFIO-PCI driver → Kata passthrough via Kubernetes

The VFIO binding is configured in `/etc/modprobe.d/vfio.conf` and persisted via initramfs. The Kata config drop-in at `80-gpu-passthrough.toml` tells QEMU to pass the VFIO device into the microVM.

A test pod:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: kata-gpu-test
spec:
  runtimeClassName: kata-qemu-nvidia-gpu
  restartPolicy: Never
  containers:
  - name: cuda
    image: nvidia/cuda:13.0.2-base-ubuntu24.04
    command: ["bash", "-lc", "echo 'Guest kernel:'; uname -a; echo 'GPU:'; nvidia-smi; sleep 5"]
    resources:
      limits:
        nvidia.com/pgpu: 1
```

Key details: `runtimeClassName: kata-qemu-nvidia-gpu` triggers Kata + VFIO instead of a regular container. `nvidia.com/pgpu` (physical GPU) requests exclusive VFIO passthrough, distinct from `nvidia.com/gpu` (shared/non-passthrough). `uname -a` proves isolation — the guest kernel differs from the host kernel.

In production Bruk, all GPUs go through the Kata + VFIO path. The lab split (one GPU Docker, one GPU Kata) is for validation convenience.

---

## Topic 14: Kubernetes Core Concepts

### What Kubernetes does

Takes a declaration of desired state ("I need 3 replicas of this inference service, each with 2 GPUs") and continuously ensures that's what's actually running across the cluster.

### Core objects

- **Pod** — smallest deployable unit. One or more containers sharing networking and storage. In Bruk, typically a single inference worker inside a Kata microVM with passthrough GPUs.
- **Node** — a physical machine in the cluster (a bare-metal GPU server). Runs **kubelet**, an agent that receives instructions from the control plane.
- **Control plane** — the cluster brain on separate machines (not GPU nodes). Includes:
  - **API server** — front door, every command goes through it
  - **Scheduler** — decides which node runs a new pod
  - **etcd** — distributed key-value store holding all cluster state

### Scheduling in Bruk's model

Unlike shared cloud clusters (bin-packing many tenants per node), Bruk uses a **bare-metal, single-tenant model**:

- Match pods to nodes with the right GPU type (H200 vs B200)
- Don't schedule more work than a node's GPUs can handle
- Honor affinity rules — keep related inference workers on the same node for NVLink
- Ensure tenant workloads land only on their dedicated nodes (taints/tolerations)

### RBAC (Role-Based Access Control)

Controls what **humans and service accounts** can do through the Kubernetes API. Locks tenant workloads to their own namespace — Tenant A can't see Tenant B's pods.

### Pod Security Admission

Controls what **pods themselves** are allowed to do once running. Three levels:

- **Privileged** — unrestricted (system components only)
- **Baseline** — prevents known privilege escalations
- **Restricted** — heavily locked down (tenant workloads). Can't run as root, can't mount host filesystem, can't use host networking.

RBAC might let you create a pod, but Pod Security Admission can still reject it if the pod spec requests privileged access.

### NetworkPolicies

Controls what **pods can talk to what**. Default Kubernetes: every pod can reach every other pod (flat, open network). NetworkPolicies lock this down to default-deny — every connection must be explicitly permitted. Enforced by Cilium via eBPF (Topic 15).

### Two APIs — don't confuse them

**Kubernetes API** — built into the control plane (written in Go). Handles cluster operations: creating pods, checking node status, applying policies. Only cluster admins and internal components interact with it. Accessed via `kubectl` or direct HTTP to the API server.

**Inference API** — the user-facing endpoint for sending prompts and receiving generated text. vLLM exposes an OpenAI-compatible API (`/v1/chat/completions`). Envoy proxy sits in front handling TLS, authentication, rate limiting, and routing. Completely separate layer, separate audience.

---

## Phase 4 — Tenant isolation is defense in depth

No single layer isolates tenants alone:

- **Kubernetes scheduling** (taints/tolerations) → tenant pods land only on dedicated nodes
- **Kata + VFIO** → GPU hardware locked exclusively to one microVM
- **RBAC** → tenants can't see each other's resources through the API
- **NetworkPolicies** → cross-tenant network traffic blocked
- **Pod Security Admission** → workloads can't escalate privileges

If any one layer fails, the others still hold.

---

# Deep Dive: How Transformer Inference Works

This cuts across multiple topics (GPUs, KV-cache, RDMA, disaggregation) and is central to understanding why Bruk is designed the way it is.

## The attention mechanism

Every transformer layer has an attention block. For each token, it computes three vectors from learned weight matrices:

- **Query (Q)** = token_embedding × W_q — "What am I looking for?"
- **Key (K)** = token_embedding × W_k — "What do I contain?"
- **Value (V)** = token_embedding × W_v — "What information do I carry?"

Attention score:
```
Attention(Q, K, V) = softmax(Q × K^T / √d_k) × V
```

1. Q × K^T — each token's query dot-products with every other token's key → relevance scores
2. / √d_k — scaling to prevent softmax saturation
3. softmax — converts scores to probability distribution (each row sums to 1)
4. × V — weighted combination of value vectors. Output is influenced most by tokens with highest attention scores.

## Multi-head attention

Done h times in parallel (32–128 heads). Each head has its own W_q, W_k, W_v, so each can learn different patterns (syntactic, semantic, positional). Outputs concatenated and projected:
```
MultiHead = Concat(head_1, ..., head_h) × W_o
```

## Full transformer layer

```
input → multi-head attention → add & normalize → FFN → add & normalize → output
```

**FFN (Feed-Forward Network)**: two large matrix multiplications with activation function between them. Often holds the majority of model parameters. Activation functions (SiLU, GeLU) run on CUDA cores; matrix multiplications run on tensor cores.

Stack 80–126 layers = a full LLM.

## Prefill phase — processing the input

All input tokens processed through all layers simultaneously. Compute-heavy, GPU parallelism fully utilized. At each layer, K and V vectors for every token are computed and stored as the **KV-cache**.

## KV-cache — what it stores

After prefill, the cache contains every layer's K and V vectors for every input token:

```
Layer 0:   K=[k_1, k_2, ..., k_n], V=[v_1, v_2, ..., v_n]
Layer 1:   K=[k_1, k_2, ..., k_n], V=[v_1, v_2, ..., v_n]
...
Layer 125: K=[k_1, k_2, ..., k_n], V=[v_1, v_2, ..., v_n]
```

Without caching, generating each new token would require recomputing all previous tokens' K and V from scratch — quadratic cost.

## Decode phase — generating tokens

For each new token:

1. Compute Q, K, V for that one token only
2. Attention: new query attends to **all** cached keys and values
3. Append new K, V to the cache
4. Predict next token
5. Repeat

## Why decode is memory-bandwidth bound

For each new token at each layer:

- **Read from HBM**: W_q, W_k, W_v weight matrices (huge) + entire KV-cache for that layer (grows with sequence length)
- **Compute**: one small vector × matrix multiplication
- **Ratio**: gigabytes read, tiny math done. GPU cores finish instantly, then wait for next data chunk from HBM.

## KV-cache sizing

For Llama 405B (126 layers, 128 heads, 128 head dimension, FP16):
```
Per token per layer: 2 × 128 × 128 × 2 bytes = 64 KB
Per token all layers: 64 KB × 126 = ~8 MB
4096-token sequence: ~32 GB of KV-cache
```

This is why PagedAttention (Topic 21) manages cache like virtual memory pages, and why RDMA transfer of the cache between prefill and decode servers is a critical performance path.

## The full inference flow

```
"Explain quantum computing"
        ↓
   [Prefill GPU] — process all tokens, create KV-cache
        ↓
   [RDMA Write over Spectrum-X] — move KV-cache to decode GPU
        ↓
   [Decode GPU] — generate tokens one at a time, each streamed back
```

| | Prefill | Decode |
|---|---|---|
| Processes | All input tokens at once | One new token at a time |
| Bottleneck | Compute (lots of math) | Memory bandwidth (reading weights + cache) |
| GPU utilization | High (parallel work) | Low (small work, big reads) |

This asymmetry is why Bruk separates them onto different GPU pools (disaggregated inference).

---

## Topic 15: Cilium and eBPF Networking

### The problem Cilium solves

Kubernetes needs a CNI (Container Network Interface) plugin to enforce NetworkPolicies. The traditional approach uses iptables — a linear chain of rules every packet walks through. With hundreds of pods and thousands of rules this gets slow. iptables was designed for static firewalls, not dynamic clusters.

### What eBPF is

eBPF lets you run small, sandboxed programs directly inside the Linux kernel without modifying it. The kernel verifies safety then attaches the program to a hook point. Every time that event fires, the eBPF program runs at kernel speed.

For networking: instead of a packet walking a long iptables chain, an eBPF program makes a direct hash map lookup and decides allow/deny in constant time. O(1) instead of O(n). A cluster with 10 policies and one with 10,000 policies have the same lookup speed.

### How Cilium uses eBPF

Replaces kube-proxy entirely. Compiles NetworkPolicies into eBPF programs. Packet leaves a pod → eBPF fires → hash map lookup on source/destination identity → allow/deny in microseconds. No userspace involvement, no iptables chain walking.

### Identity-based policy

Traditional policies use IP addresses — but pods are ephemeral and IPs get recycled. Cilium assigns each pod a cryptographic identity based on Kubernetes labels. Policies reference identities, not IPs. Pod restarts with new IP but same labels → still allowed. Different pod inherits old IP with different labels → denied.

### Default-deny east-west

East-west = all pod-to-pod traffic across the entire cluster (same node or different nodes). Default-deny means no pod can talk to any other pod unless a policy explicitly permits it. Enforced efficiently because eBPF lookups add negligible latency.

---

## Topic 16: Helm, Flux, and GitOps

### GitOps — the principle

The entire desired state of the cluster is declared in a Git repository. Every Kubernetes manifest, every Helm chart value, every network policy — all version-controlled files. Git is the single source of truth. No one runs `kubectl apply` against production directly.

Why Git matters for a sovereign platform:
- **Audit trail** — every change has a commit with timestamp, author, and diff. Verifiable answer to "what ran on June 3rd at 14:00?"
- **Review process** — pull requests mean no change goes live without peer review
- **Rollback** — revert a commit, Flux applies it, cluster returns to previous state within minutes
- **Reproducibility** — entire cluster can be recreated from the repo

### Helm — packaging Kubernetes manifests

Package manager for Kubernetes. Charts are templated YAML with configurable values. Change `replicas: 3` to `replicas: 5` in values.yaml → Helm generates two additional pod specs automatically.

### Flux — the reconciliation engine

Runs inside the cluster, continuously watches Git:
1. Poll Git for changes
2. Compare desired state in Git to actual cluster state
3. Apply differences to make cluster match Git

If someone manually edits something in the cluster (bypassing Git), Flux detects drift and reverts it. Continuous reconciliation — not a one-time deploy, but a constant enforcement loop.

---

## Topic 17: NVIDIA GPU Operator

### The problem it solves

Each GPU node needs: NVIDIA kernel drivers, CUDA runtime, container toolkit, device plugin, MIG configuration, DCGM exporter. Doing this manually is error-prone. Critically for Bruk — drivers can't be baked into the immutable OS image because driver updates would require a full OS rebuild.

### What the GPU Operator does

A Kubernetes operator (controller) that manages GPU software lifecycle automatically. When a new GPU node joins: detects GPUs → deploys driver as container (not on host OS) → installs container toolkit → deploys device plugin → configures DCGM exporter → validates setup. No manual intervention.

### Drivers as containers — the key insight

Instead of installing the NVIDIA driver on the host OS (which would write to the protected filesystem and break dm-verity), the GPU Operator runs the driver inside a privileged container that loads the kernel module dynamically. OS image stays clean. Driver updates are container image updates — no OS rebuild. Loading a kernel module dynamically is an allowed operation, not a filesystem write to the protected root.

### DaemonSets

A DaemonSet runs exactly one pod of a type on every matching node. When a node joins, the pod is automatically placed. When a node leaves, it's cleaned up.

| | Deployment | DaemonSet |
|---|---|---|
| Says | "Run N replicas somewhere" | "Run one on every matching node" |
| Use case | Application workloads | Infrastructure agents |

The GPU Operator creates one DaemonSet per component (driver, device plugin, metrics exporter, etc.), each matching GPU nodes.

### Full node onboarding flow

1. Node boots → passes attestation (Phase 3)
2. Flux reconciles it into the cluster (Topic 16)
3. GPU Operator detects new node
4. Automatically deploys all GPU software components
5. Node ready to accept inference workloads

No human intervention between step 1 and step 5. Adding GPU capacity is a hardware operation, not a software operation.

### Connection to GitOps

The GPU Operator is deployed via Helm chart that lives in Git. Flux applies it. Driver version, component configuration, MIG settings — all declared as Helm values in Git. GitOps all the way down.

---

## Phase 4 Complete — Updated big picture

- **Containers** (namespaces/cgroups) → lightweight isolation
- **Kata microVMs** (QEMU/KVM + VFIO) → hardware-enforced GPU isolation
- **Kubernetes** (scheduling, RBAC, Pod Security) → workload placement and access control
- **Cilium eBPF** (identity-based, default-deny) → O(1) network policy enforcement
- **GitOps** (Helm + Flux + Git) → auditable, reproducible cluster state
- **GPU Operator** (DaemonSets + driver containers) → automated GPU software, immutable OS preserved

---

# Phase 5: Identity, Secrets, and Zero Trust

The shift: Phase 4 was about what runs and where. Phase 5 asks how each service proves who it is, and how authorization decisions are made without trusting the network. Bruk's principle: every service must cryptographically prove its identity on every request, regardless of where it's running.

## Topic 18: SPIFFE/SPIRE and Workload Identity

### The problem with static credentials

Static API keys and passwords get leaked, don't expire, and say nothing about *what* is making the request. In a cluster with hundreds of services, managing them becomes a security nightmare.

### SPIFFE — the standard

SPIFFE (Secure Production Identity Framework for Everyone) defines how workloads get cryptographic identities. The core concept is the **SVID (SPIFFE Verifiable Identity Document)** — a cryptographically signed document stating a workload's identity as a URI:

`spiffe://bruk.example.com/tenant-a/inference-worker`

SVIDs come in two forms:
- **X.509 SVID** — a TLS certificate with the SPIFFE ID in the Subject Alternative Name field
- **JWT SVID** — a signed JSON Web Token containing the SPIFFE ID

### SPIRE — the implementation

**SPIRE Server** — runs centrally. The certificate authority. Knows the trust policy: "a pod in namespace tenant-a with service account inference-worker should get this identity."

**SPIRE Agent** — runs on every node (as a DaemonSet). When a workload starts, it contacts the local agent via Unix socket. The agent uses kernel-level attestation to verify which pod/service account/namespace this process belongs to, then requests the SVID from the SPIRE Server on the workload's behalf.

### The attestation flow

Workload starts → contacts SPIRE Agent via Unix socket → agent uses kernel-level attestation to verify pod identity → agent requests SVID from SPIRE Server → server validates against policy → issues X.509 certificate → agent delivers SVID to workload → workload uses SVID for mTLS.

### Short-lived certificates

SVIDs expire in hours, not years. If a certificate is compromised, it's useless within hours automatically — no manual revocation required. This also changes the attacker's calculus: a stolen credential has a very short window of usefulness.

### mTLS (mutual TLS)

Normal TLS: client verifies server. mTLS: both sides verify each other's certificates. When the inference worker talks to the model store, both present their SVIDs. A compromised service without a valid SVID can't impersonate a legitimate one.

Each tenant's workloads get SPIFFE IDs scoped to their tenant — cryptographically distinct identities that downstream policy (OPA) uses for authorization decisions.

---

## Topic 19: OPA (Open Policy Agent) and WASM

### Identity vs. authorization

SPIFFE/SPIRE answers "who are you?" OPA answers "what are you allowed to do?" A valid SVID proves identity — but without OPA, every service would implement its own authorization checks inconsistently. OPA centralizes that logic: one policy engine, one place to audit, one place to change rules.

### What OPA is

A general-purpose policy engine. Policies are written in **Rego** — a declarative language that evaluates input data and produces allow/deny decisions:

```rego
allow {
    input.caller.tenant == input.resource.tenant
    input.resource.model in data.tenants[input.caller.tenant].allowed_models
    input.caller.request_count < data.tenants[input.caller.tenant].rate_limit
}
```

Policies live in Git, reviewed via pull requests, deployed centrally.

### OPA compiled to WASM

OPA normally runs as a sidecar — every authorization check is a network call, adding latency. OPA can compile Rego policies to a **WASM binary** that gets embedded directly inside Envoy and runs **in-process** — no network hop, no sidecar. This is called in-process policy evaluation. Policy evaluation happens in microseconds inside the same process handling the request.

### Policy as code

Authorization logic is version-controlled in Git with full history. Changes go through pull request review. Every decision is logged with input, matching policy, and outcome. A customer can ask "what policy governed access to my models on June 3rd?" — you can show the exact Git commit, who approved it, and the diff.

### What OPA evaluates in Bruk

- Request authorization — does this tenant have permission to use this model?
- Rate limiting — has this tenant exceeded their quota?
- PII validation — does this request contain data that shouldn't be logged?
- Kubernetes admission control — should this pod be allowed to run?

---

## Topic 20: Envoy Proxy Deep Dive

### What Envoy is

A high-performance proxy written in C++ that sits in front of every service in Bruk. Every request passes through Envoy before reaching its destination. It is the **enforcement layer** where identity and policy are applied to live traffic.

### What Envoy handles

**TLS termination and mTLS** — terminates inbound TLS, initiates mTLS to upstream services, validates X.509 SVIDs. Applications never handle TLS logic. If a TLS bug exists, fix it in Envoy once — not in 50 services written in different languages.

**JWT validation** — external requests carry a JWT. Envoy validates the signature, checks expiry, extracts claims (tenant ID, allowed models, rate limit tier). Invalid JWTs rejected immediately.

**Rate limiting** — enforces per-tenant request rate limits. Requests exceeding the limit get a 429 before touching the inference stack.

**PII redaction** — inspects request bodies before logging and scrubs sensitive fields. The inference worker processes the full request; the logged version is clean.

**Routing via xDS** — Envoy doesn't use static routing config. xDS (discovery service) is a dynamic API that pushes routing decisions to Envoy in real time from a control plane (Dynamo). When Dynamo wants to route a request to a specific worker with the right KV-cache, it updates Envoy's routing via xDS — no restart required.

### External vs. internal identity

- **External clients** use **JWT** — signed tokens in the Authorization header. Stateless, easy to generate from any language.
- **Internal services** use **SVID/mTLS** — both sides present X.509 certificates issued by SPIRE. Mutual verification, automatic rotation. Faster and more secure.

Different trust boundaries, different mechanisms.

### The enforcement chain on every request

```
Request arrives
      ↓
TLS termination + SVID/JWT validation  ← identity check
      ↓
OPA WASM policy evaluation             ← authorization check
      ↓
Rate limit check                        ← resource protection
      ↓
PII inspection/redaction                ← data protection
      ↓
xDS routing to correct backend          ← intelligent routing
      ↓
Request reaches inference worker
```

If any step fails, the request is rejected before reaching the GPU.

### xDS vs. traditional load balancers

A traditional load balancer has a static config file — edit and reload to change routes. Envoy with xDS has no static routing. The control plane (Dynamo) pushes routing decisions over a live API connection. Envoy picks them up instantly with no restart. This enables per-request routing decisions based on real-time state — "send this request to worker 7 because it already has the KV-cache for this conversation."

---

## Phase 5 — The big picture

SPIFFE/SPIRE (every service gets a cryptographic identity) → OPA in WASM (every request evaluated against policy in microseconds, in-process) → Envoy (the enforcement point where identity is verified, policy is applied, rate limits enforced, and traffic routed intelligently).

Every request is authenticated, authorized, rate-limited, and routed before it ever touches a GPU. This is what "replace trust with verify" looks like in practice.
