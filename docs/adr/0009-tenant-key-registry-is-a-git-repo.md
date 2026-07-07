---
status: accepted
---

# Tenant/key registry is a Git repository, not a service

The fleet plane needs a registry that answers "is this node expected, and what is it entitled
to?" (ADR-0001). We chose a **private Git repo** — one file per Tenant/Cluster/Node, secrets
sops/age-encrypted — over a database-backed service or an overlay on MAAS's machine inventory.
Rationale: it matches the project's everything-through-Git-auditable ethos (see the Airon
Operator entry in CONTEXT.md), adds zero stateful infrastructure to the fleet-plane host, and
has a clean upgrade path — a future fleet-plane API *fronts* the repo rather than replacing it.
The MAAS-inventory overlay was rejected outright: MAAS knows machines, not tenants or keys, and
it would couple the tenant model to MAAS's database schema.

Minimal schema (three levels, matching the CONTEXT.md topology terms):

- **Tenant** — the customer; owns exactly one Cluster.
- **Cluster** — its Flux repo/ref and its secrets bundle (deploy key, `hf-token`, …) —
  the things a human carries onto the box today.
- **Node** — hardware identity (TPM EK certificate) plus expected boot-chain reference values.

## Consequences

- **Enrollment is a PR**: adding a Node = committing its EK certificate and reference values.
  Auditable and reviewable; becomes a chore at fleet scale — that is the revisit trigger for
  putting a service in front.
- The host-attestation verifier and (later) the KBS/Trustee stack read from a checkout of this
  repo. The client-facing token issuer reads the same Tenant records but is out of M5 scope.
- Per-cluster secret bundles live in the repo encrypted at rest; their release to a node is
  gated by host attestation (M5b).
