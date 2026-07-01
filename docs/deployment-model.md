# Bruk deployment model — how it's shipped to customers

Consolidates how Bruk is deployed and isolated per customer. This was previously only *implicit*
across `docs/adr/0001` (fleet of single-tenant clusters), `docs/adr/0002` (deterministic operator),
`CONTEXT.md` (the glossary), and the compendium. Terminology here is the `CONTEXT.md` glossary — use it.

## The one-sentence answer

**Bruk is a *fleet of single-tenant clusters*: every customer gets their own cluster (their own Bruk
instance). The cluster *is* the tenant boundary — there is no multi-tenancy inside a cluster.** Kata +
SEV-SNP + CC-GPU is *how the workload runs inside* each customer's own cluster; it is not a shared
arrangement between customers.

## The confusion this resolves

"Is each customer getting their own Bruk instance, **or** are they getting Kata-containers on picked
GPUs?" — **Both, at two different layers.** They are not alternatives; they stack:

| Layer | Isolates | Mechanism | Granularity |
|---|---|---|---|
| **Tenant boundary** | Customer A from Customer B | one whole **cluster** per tenant (ADR-0001) | per customer |
| **Confidential compute** | the customer from **Bruk itself** (the host/operator) | Kata microVM + SEV-SNP guest + CC-mode GPU, *inside* that cluster | per workload |

The first says *customers never share a cluster*. The second says *even the operator running the cluster
can't read the customer's model or prompts*. "Kata on picked GPUs" is the second layer, living inside
each customer's dedicated cluster.

## The three tiers

```
┌─ FLEET PLANE ─ cross-cluster control (provisions/attests/orchestrates) ── DESIGNED, not built ─┐
│   tenant & key registry · token issuer · host-attestation verifier · bare-metal provisioning   │
└──────────────┬──────────────────────────────────┬─────────────────────────────────────────────┘
               │ one cluster per tenant            │
   ┌───────────▼────────────┐          ┌───────────▼────────────┐
   │  Customer A — CLUSTER   │          │  Customer B — CLUSTER   │   ← TENANT boundary (single-tenant)
   │  bare-metal Node(s),    │          │          ...            │     Node = 1 machine, ~8 GPUs
   │  k3s + Cilium + GPU-Op  │          │                         │
   │  + kata-deploy          │          │                         │
   │  Airon Operator (CRDs,  │          │                         │   ← deterministic K8s operator,
   │   GitOps/Flux)          │          │                         │     GitOps — DESIGNED, not built
   │  ┌───────────────────┐  │          │                         │
   │  │ Kata + SEV-SNP +  │  │          │                         │   ← CONFIDENTIAL workload
   │  │ CC-GPU vLLM worker│  │          │                         │     BUILT (by hand, 1 ref cluster)
   │  └───────────────────┘  │          │                         │
   └─────────────────────────┘          └─────────────────────────┘
```

- **Fleet plane** — the cross-cluster layer that provisions, attests, and orchestrates many
  single-tenant clusters. All *multi-customer* concerns (tenant registry, key/token issuer, the
  host-attestation verifier) live here, **never inside a cluster** (ADR-0001, CONTEXT.md).
- **Cluster** — one K8s control plane + its bare-metal **Node(s)**; belongs to exactly one **tenant**.
  Aggregate "10,000+ GPUs" is a *fleet* target, not a single-cluster target.
- **Airon Operator** — a *deterministic* K8s operator (Go + controller-runtime), GitOps via Flux, no
  direct `kubectl` on prod; reconciles CRDs (`BrukTenant`, `BrukModel`, `InferenceService`) into the
  Envoy/Dynamo/vLLM stack (ADR-0002). Any LLM help is read-only/advisory (a PR a human approves).

## How a customer gets stood up (target lifecycle)

1. **Fleet plane** provisions a bare-metal **Node** and a single-tenant **cluster** for the customer
   (this is where MAAS-style bare-metal provisioning fits — see "different site" below).
2. **Host attestation gate** — the Node receives no secrets (keys, weights, creds) until it passes
   host attestation (proves its boot chain; requires the TPM). Distinct from *guest* attestation.
3. **GitOps (Flux)** applies the per-cluster stack declaratively from Git (k3s config, Cilium,
   GPU Operator Helm values, kata-deploy, the Airon Operator).
4. **Airon Operator** reconciles the customer's CRDs → the serving stack converges.
5. **Model artifacts** (signed, Cosign-verified, digest-pinned OCI) are staged and served; for
   *confidential* serving the workload runs in a SEV-SNP + CC-GPU Kata guest (guest attestation via
   AMD-PSP + GPU device report).

## What's BUILT vs DESIGNED (honest status, 2026-07-01)

| Piece | Status |
|---|---|
| Per-cluster serving stack (k3s, Cilium, GPU Operator, kata-deploy) | ✅ Built (one reference cluster) |
| Confidential serving (SEV-SNP + CC-GPU + Kata) — small model | ✅ Built & proven (see `h100-bringup-status.md`) |
| Local registry mirror for confidential guest-pull | ✅ Built (`manifests/registry/`, ADR-0006 Part 1) |
| Confidential serving of a *large* (24B) model | 📋 Blocked on block-device storage (ADR-0006 Part 2) |
| GitOps/Flux repo + Helm umbrella for the whole per-cluster stack | 📋 Designed (compendium), not built |
| Airon Operator + CRDs | 📋 Designed (ADR-0002 / CONTEXT.md), not built |
| Fleet plane (provisioning, tenant/key registry, attestation verifier) | 📋 Designed (ADR-0001), not built |
| Host-attestation gate | 📋 Designed; blocked on the TPM (discrete module on order) |

The repo today is **docs + manifests** — the per-cluster stack is assembled largely by hand; the
automation and cross-cluster layers are decisions, not yet code.

## Reproducing on a different site

- **Today (manual):** follow `docs/RUNBOOK-confidential-serving.md` by hand on a new Node — firmware
  (BIOS SEV-SNP) → kernel/VFIO → k3s (+ flag) → Helm ×3 → kubectl (registry, workloads) → scripted
  initdata. It works and is committed, but it is a runbook, not a one-click deploy.
- **Target (easy/repeatable):** the golden recipe becomes **declarative + automated** — a GitOps repo
  (Flux) + a Helm umbrella chart for the per-cluster stack, instantiated by the **fleet plane**
  ("provision customer N at site X") and reconciled by the **Airon Operator**.
- **The hard layer is hardware/firmware.** BIOS SEV-SNP, kernel, and VFIO are per-physical-machine and
  **cannot** be done via K8s/GitOps. That is a **bare-metal provisioning** problem — the natural
  fleet-plane component. This box already carries **MAAS** (PXE/IPMI) leftovers; MAAS (or equivalent) is
  the likely mechanism to PXE-boot a new Node, apply the firmware/kernel/VFIO config, and hand a ready
  host to k3s. Capturing that host layer as automation is the biggest gap for multi-site.

## A known divergence the confidential path introduces

`CONTEXT.md`'s **Model artifact** definition delivers weights by **virtio-fs bind-mount** into the Kata
worker — that's the *non-confidential* path. Confidential guests are **`shared_fs = "none"`** (no
virtio-fs), so that delivery mechanism does **not** apply to confidential serving. The confidential path
therefore needs a different artifact delivery (block device / encrypted, attested key release) — this is
exactly what ADR-0006 works out. Keep both paths in mind: the compendium's virtio-fs staging is the
non-CC story; ADR-0006 is the CC story.

## Pointers
- ADR-0001 (fleet of single-tenant clusters), ADR-0002 (deterministic operator), ADR-0006 (confidential
  weights delivery/storage). `CONTEXT.md` (glossary — the source of truth for these terms).
- `docs/RUNBOOK-confidential-serving.md` (reproduce the per-cluster confidential stack).
- `docs/h100-bringup-status.md` (current reference-cluster state).
