# Bruk Abbreviation & Acronym Glossary

Compiled from the compendium, spec PDF, ADRs, and CONTEXT.md.

## Hardware / Physical

| Abbrev. | Full meaning |
|---|---|
| CPU | Central Processing Unit |
| GPU | Graphics Processing Unit |
| EPYC | AMD's server CPU product line (brand name, not an acronym) |
| PCIe | Peripheral Component Interconnect Express |
| DDR5 | Double Data Rate (5th generation) — system RAM standard |
| NVMe | Non-Volatile Memory Express |
| SSD | Solid State Drive |
| BMC | Baseboard Management Controller |
| IPMI | Intelligent Platform Management Interface |
| VLAN | Virtual Local Area Network |
| HBM / HBM3e | High Bandwidth Memory (3e = 3rd-gen "extended") |
| VRAM | Video RAM |
| DPU | Data Processing Unit (e.g., BlueField-3) |
| NIC | Network Interface Card |
| ARM | Advanced RISC Machine (CPU architecture used in DPU cores) |
| BIOS | Basic Input/Output System |
| USB | Universal Serial Bus |
| JTAG | Joint Test Action Group (hardware debug/test interface) |
| IOMMU | Input–Output Memory Management Unit |

## GPU Compute / Inference

| Abbrev. | Full meaning |
|---|---|
| CUDA | Compute Unified Device Architecture (NVIDIA's GPU compute platform) |
| FP16 / FP8 | Floating Point, 16-bit / 8-bit precision |
| KV-cache | Key–Value cache (stores attention Key/Value vectors) |
| Q, K, V | Query, Key, Value (attention mechanism vectors) |
| FFN | Feed-Forward Network |
| ReLU | Rectified Linear Unit (activation function) |
| SiLU | Sigmoid Linear Unit (activation function) |
| GeLU | Gaussian Error Linear Unit (activation function) |
| TP | Tensor Parallel / Tensor Parallelism |
| MIG | Multi-Instance GPU (NVIDIA GPU slicing — explicitly *not* used in Bruk) |
| DCGM | Data Center GPU Manager (NVIDIA monitoring/metrics tool) |
| NIXL | NVIDIA Inference Xfer Library (Dynamo's KV-transfer library) |
| LLM | Large Language Model |

## Networking / Fabric

| Abbrev. | Full meaning |
|---|---|
| RDMA | Remote Direct Memory Access |
| RoCEv2 | RDMA over Converged Ethernet, version 2 |
| PFC | Priority Flow Control |
| ECN | Explicit Congestion Notification |
| IP | Internet Protocol |
| TCP | Transmission Control Protocol |
| DOCA | Data Center Infrastructure on a Chip Architecture (NVIDIA DPU SDK) |
| DNS | Domain Name System |
| SSH | Secure Shell |

## Boot / OS Security

| Abbrev. | Full meaning |
|---|---|
| UEFI | Unified Extensible Firmware Interface |
| GRUB | GRand Unified Bootloader |
| TPM | Trusted Platform Module |
| PCR | Platform Configuration Register (TPM measurement slot) |
| CA | Certificate Authority |
| FIPS | Federal Information Processing Standards |
| AES | Advanced Encryption Standard |
| SHA | Secure Hash Algorithm |
| RSA | Rivest–Shamir–Adleman (public-key cryptosystem) |
| CIS | Center for Internet Security (benchmark standard) |
| UKI | Unified Kernel Image |
| dm-verity | device-mapper verity (Linux kernel block-integrity feature) |
| OS | Operating System |

## Containers / Virtualization

| Abbrev. | Full meaning |
|---|---|
| VFIO | Virtual Function I/O |
| QEMU | Quick Emulator |
| KVM | Kernel-based Virtual Machine |
| VM | Virtual Machine |
| PID | Process ID (a Linux namespace type) |
| CoCo | Confidential Containers |

## Kubernetes / Orchestration

| Abbrev. | Full meaning |
|---|---|
| RBAC | Role-Based Access Control |
| API | Application Programming Interface |
| CNI | Container Network Interface |
| eBPF | extended Berkeley Packet Filter |
| CRD | Custom Resource Definition |
| ADR | Architecture Decision Record (the `000x-*.md` files) |

## Identity / Security / Policy

| Abbrev. | Full meaning |
|---|---|
| SPIFFE | Secure Production Identity Framework for Everyone |
| SPIRE | SPIFFE Runtime Environment (SPIFFE's reference implementation) |
| SVID | SPIFFE Verifiable Identity Document |
| JWT | JSON Web Token |
| mTLS | mutual Transport Layer Security |
| TLS | Transport Layer Security |
| OPA | Open Policy Agent |
| WASM | WebAssembly |
| PII | Personally Identifiable Information |
| xDS | (envoy) discovery service API family — "x" is a placeholder for CDS/LDS/RDS/EDS etc. (Cluster/Listener/Route/Endpoint Discovery Service) |
| OIDC | OpenID Connect |
| HSM | Hardware Security Module |
| KMS | Key Management Service |

## Confidential Compute (CC)

| Abbrev. | Full meaning |
|---|---|
| CC | Confidential Compute |
| TEE | Trusted Execution Environment |
| SEV-SNP | Secure Encrypted Virtualization – Secure Nested Paging (AMD CPU confidential-compute tech) |
| PSP | Platform Security Processor (AMD security co-processor that signs SEV-SNP measurements) |
| TDX | Trust Domain Extensions (Intel's confidential-compute tech) |
| SPDM | Security Protocol and Data Model (used for GPU/PCIe attestation) |
| SPT | Single-GPU PassThrough (per `CONTEXT.md` — CC-mode topology, one GPU per confidential guest; not in the ground-truth spec PDF) |

## Supply Chain

| Abbrev. | Full meaning |
|---|---|
| SBOM | Software Bill of Materials |
| SLSA | Supply-chain Levels for Software Artifacts |
| OCI | Open Container Initiative (artifact/image format & registry spec) |

## Licensing

| Abbrev. | Full meaning |
|---|---|
| OSI | Open Source Initiative |
| MIT | MIT License |
| BSD | Berkeley Software Distribution (license family) |
| MPL | Mozilla Public License |
| LGPL | Lesser General Public License |
| AGPL | Affero General Public License |

## Observability / Misc.

| Abbrev. | Full meaning |
|---|---|
| OTel | OpenTelemetry |
| S3 | Simple Storage Service (AWS object storage; used here via S3 Object Lock for audit logs) |
| SaaS | Software as a Service |
| gRPC | gRPC Remote Procedure Call (originally "gRPC RPC"; recursive-ish backronym — the "g" has been assigned different meanings across releases) |

---

### Note on terms you mentioned but that don't appear in the docs

**GPA / HPA** (Guest Physical Address / Host Physical Address) don't show up anywhere in the compendium, the spec PDF, the ADRs, or `CONTEXT.md`. They're standard virtualization/TEE vocabulary (relevant to how SEV-SNP or TDX remap guest memory), but since the ground-truth PDF is your authoritative source, I left them out rather than importing outside terminology. Flag it if you want me to add a "related terms not in the spec" appendix — useful context for the SEV-SNP work, but would need to be clearly marked as supplementary, same as `CONTEXT.md`'s SPT entry.
