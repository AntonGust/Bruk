# Bruk

Airon's open-source, sovereign AI/ML platform for bare-metal, single-tenant inference (and, later, fine-tuning). This glossary fixes the language used across the spec, compendium, and implementation so terms stay unambiguous as the system is built.

## Language

### Topology & scale

**Cluster**:
A single Kubernetes control plane and the bare-metal nodes it manages. The cluster *is* the tenant boundary — one cluster belongs to exactly one tenant.
_Avoid_: deployment, environment

**Fleet**:
The set of all Bruk clusters Airon operates, across customers and datacenters. "10,000+ GPUs" is an aggregate **fleet** capacity target, not a single-cluster target.
_Avoid_: estate, fleet of fleets

**Fleet plane**:
The cross-cluster control layer that provisions, attests, and orchestrates many single-tenant clusters. Distinct from the per-cluster control plane the spec PDF describes. (New concept — not in the original PDF.)
_Avoid_: meta-cluster, global control plane

**Tenant**:
The single customer (or Airon itself) that owns one cluster. There are no multi-tenant primitives *inside* a cluster; multi-customer concerns live only on the fleet plane.
_Avoid_: account, client, user

**Node**:
One bare-metal, single-tenant physical machine (typically 8 GPUs) that is a member of exactly one cluster.
_Avoid_: server, host, box

### Serving path

**Serving skeleton**:
Milestone 1 — the thinnest end-to-end inference path on a single node: client → Envoy → Dynamo → vLLM-worker (in Kata, whole-GPU VFIO passthrough) → streamed response. Runs on vanilla Kubernetes on the one node.
_Avoid_: MVP, prototype

**OpenAI-compatible contract**:
The client-facing API surface (`/v1/chat/completions` etc.). **Envoy owns this contract**, so the upstream (vLLM directly, or via Dynamo) is invisible to clients.
_Avoid_: the API, the endpoint (when precision is needed)

**Attestation gate**:
The rule and its enforcing artifact: a node receives no secrets (keys, weights, credentials) until it passes remote attestation. Lands in parallel with the serving skeleton.
_Avoid_: preflight (the preflight script is one input to the gate, not the gate itself)

### Model library

**Model library**:
The set of open-weight models Bruk offers through the OpenAI-compatible contract. Seeded with **Apache-2.0, European-origin** models (Mistral family; Qwen2.5 acceptable) to stay consistent with the OSI-only, sovereign ethos. Non-OSI weights (e.g. Llama) are dev-only, never shipped.
_Avoid_: model zoo, model catalog

**Model artifact**:
A model packaged as a signed, versioned, content-addressed **OCI artifact** in a registry — the canonical source of truth. Cosign-verified and digest-pinned on the host before being staged to node-local NVMe and bind-mounted (virtio-fs) into the Kata worker.
_Avoid_: weights blob, checkpoint (when referring to the distributable artifact)

### Roles

**Airon Operator**:
A **deterministic Kubernetes operator** (Go + controller-runtime, CRD-driven reconcilers) that manages Bruk's control-plane services. Explicitly **not** an LLM/agentic-AI actor — every change flows through Git/Flux and is auditable. Reconciles CRDs (e.g. `BrukModel`, `BrukTenant`, `InferenceService`) into the vLLM/Dynamo/Envoy stack.
_Avoid_: **agent** (collides with SPIFFE workload + LLM agent), AI operator, controller (generic). Any LLM assistance is read-only/advisory and may only propose a Git PR a human approves — never act on the cluster.
