---
status: accepted
---

# Fleet of single-tenant clusters, not one giant cluster

The spec's "scale to 10,000+ GPUs" combined with "the cluster *is* the tenant boundary / no multi-tenant primitives at any level" forces a choice. We read "10,000+ GPUs" as **aggregate fleet capacity**, not a single-cluster target: Bruk is a **fleet of single-tenant clusters** (one cluster per tenant), and the hyperscale problem lives in a new **fleet plane** that provisions, attests, and orchestrates many clusters. We rejected "one enormous single-tenant cluster" because it only makes sense with a single anchor tenant buying the whole footprint, which we don't have.

## Consequences

- Introduces a **fleet plane** (cross-cluster control layer) the original PDF does not describe.
- Per-cluster architecture stays small and matches the PDF; multi-customer concerns (tenant/key registry, the real token issuer) belong to the fleet plane, never inside a cluster.
