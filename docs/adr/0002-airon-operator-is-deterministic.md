---
status: accepted
---

# The Airon Operator is a deterministic Kubernetes operator, not an agentic-AI operator

The spec describes "an Agent operator managing the services," which reads ambiguously as either an LLM-driven agentic operator or a Kubernetes operator. We decided the Airon Operator is a **deterministic Kubernetes operator** (Go + controller-runtime, CRD-driven reconcilers). We rejected an agentic-AI operator because a non-deterministic LLM in the most privileged control loop directly contradicts the platform's own goals — "contain the rogue LLM," zero-trust at every layer, and GitOps with no direct `kubectl` on prod.

## Consequences

- Every control-plane change flows through Git/Flux and is auditable; the Operator reconciles CRDs (`BrukModel`, `BrukTenant`, `InferenceService`, …).
- Any future LLM assistance is read-only/advisory and may only propose a Git PR a human approves — it never acts on the cluster directly.
