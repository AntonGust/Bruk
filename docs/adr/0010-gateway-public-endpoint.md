---
status: accepted
---

# The Gateway: a public OpenAI-compatible endpoint, as operator-rendered plain Envoy

Output of the 2026-07-12 grilling session (design only; implementation follows). ADR-0007
deferred all public endpoint/auth fields to "the Envoy phase" — this is that phase's design. It
deliberately reverses one recorded ADR-0008 property: the serving endpoint is no longer
ClusterIP-internal only; it becomes reachable through exactly one audited component, the
**Gateway** (see CONTEXT.md).

## Decisions

**Full contract shape from day one.** One public base URL per cluster; the upstream model is
picked from the request body (`"model"` field), as OpenAI clients send it; `GET /v1/models`
serves the catalog. The URL surface is the hard-to-change artifact — its shape ships correct even
while the implementation routes between exactly two upstreams. (Rejected: per-model path
prefixes — cheaper now, a public breaking change later.)

**Plain Envoy, rendered by the Airon operator.** A Deployment + ConfigMap + Service produced by
`operator/internal/render`, golden-tested like the workloads (ADR-0007 render contract).
Rejected: Cilium's built-in ingress/Gateway API — body-based routing is not expressible in
`HTTPRoute`, so raw Envoy config would tunnel through `CiliumEnvoyConfig` anyway, and enabling it
is a live-CNI Helm change on the single node; Envoy Gateway — a second controller whose
abstractions the body-routing requirement immediately punctures. Revisit the implementation at
multi-node/Dynamo; the public contract stays fixed regardless.

**API surface: `BrukTenant.spec.gateway` (optional, additive to v1alpha1).** Supersedes-in-part
ADR-0007's "no public endpoint/auth fields" non-goal. Carries the public hostname (delivered via
`${GATEWAY_HOSTNAME}` postBuild.substitute, the `${INITDATA_B64}` pipe — the public repo carries
no reference-box coordinates), `tls.secretRef`, and `apiKeysSecretRef`. **Every Ready
InferenceService is automatically routable by `servedModelName`** — no opt-in flag: the routing
table and the catalog are the same join (ADR-0007's catalog design), so they cannot skew. The
smoke model is therefore publicly listed and invocable behind auth — accepted as a feature: a
cheap end-to-end health check of the whole public path.

**Auth: Bearer API keys, validated by an ext_authz sidecar that *is* the operator binary.**
`Authorization: Bearer <key>` because every OpenAI SDK sends it unprompted. Keys are named
entries in one Secret (`key-primary`, `key-secondary`, … — overlap-then-retire rotation),
volume-mounted into the Gateway pod and compared constant-time (`crypto/subtle`) by a second
entrypoint of the same `bruk-operator` image (`gateway-authz`) — one image, one digest pin, the
existing CI/lockstep discipline. The ADR-0008 posture survives intact: the reconciler never
materializes key values; nothing secret appears in rendered config. Auth precedes routing — 401
before any model-existence disclosure; unknown models get an OpenAI-style `model_not_found`.
Rejected: Lua-filter compare (hand-rolled security-critical code in an untestable sandbox);
Envoy's native `api_key_auth` filter (credentials inline in config — exactly what the operator
must not render; a simplification candidate iff file-sourced credentials verify upstream).

**Catalog and routes delivered via filesystem xDS.** The operator renders RDS/CDS and the
`/v1/models` response into the ConfigMap; Envoy watches the mounted files and hot-reloads. A new
Ready InferenceService reaches the public endpoint with zero Gateway pod restarts (propagation
latency = kubelet ConfigMap sync, ~1 min). Everything requires auth except a bare `/healthz`.

**TLS: a standard `kubernetes.io/tls` Secret, produced by cert-manager, consumed via filesystem
SDS.** Producer and consumer deliberately decoupled — the Gateway mounts and hot-rotates whatever
the Secret holds; how it is produced may differ per cluster with no schema change. Production
path: cert-manager (a new Flux HelmRelease, the M3 adoption pattern) with Let's Encrypt **DNS-01
via Cloudflare** (airon.ai's NS is Cloudflare), using a zone-scoped API token in a Secret that
only cert-manager can read. Port 80 never opens; issuance is independent of Envoy's bind.

**Exposure: `hostNetwork: true`, port 443 — a named single-node-era exception.** On this box
there is nothing to load-balance: one node, one public IP, bound to the host NIC. An LB layer
(MetalLB / Cilium LB-IPAM) cannot announce the node's own IP — it needs its own routable VIP pool
(DC IP allocation) plus L2/BGP announcement enabled in Cilium (a live-CNI change), all to route
to the same single machine. k3s runs `--disable=servicelb`; NodePort forces `:30443`-style URLs;
hostPort needs the same live-CNI change. Hardening required: `runAsNonRoot`, drop ALL
capabilities except `NET_BIND_SERVICE`, read-only root FS, restricted seccomp, no privileged
mode, pinned/distroless Envoy image, NetworkPolicy where still meaningful. Any future policy
forbidding hostNetwork carries exactly one named exception: this pod.

**Named successor (multi-node era; fabric-dependent, not speculated now):** the Gateway becomes a
normal pod (1–2 replicas) behind a `LoadBalancer` Service with a per-cluster VIP announced to the
Spectrum-4 ToR — Cilium BGP control plane or BlueField-3-hosted LB, decided when the fabric's
routing design exists. The migration is a render change plus a DNS update; hostname, schema, TLS
Secret, API keys, and the public contract are all unchanged — that cheapness is deliberate.

**Fleet-scale framing (ADR-0001):** 500–1000 customers = 500–1000 single-tenant clusters, each
stamping this same pattern; no Gateway ever serves two customers. Per-cluster Gateway traffic is
bounded by what its GPUs can generate — orders of magnitude inside one Envoy's capacity. The real
fleet-scale pressure points are DNS automation and Let's Encrypt's ~50 new certs/week per
registered domain (renewals exempt): onboarding pace, not steady state — request an LE rate-limit
increase before mass onboarding.

**Abuse posture: Envoy local rate limiting now; per-key quotas deferred.** A conservative global
token bucket on `/v1/*` sized to protect the engine queue (one GPU saturates at low double-digit
concurrency — the bucket is engine protection, not QoS), a stricter bucket on unauthenticated
requests (cheap-reject credential stuffing), and connection limits. Per-key quotas/QoS arrive
with the metering increment, together with named-key audit logging in the sidecar. Two defaults
that would silently break the product are pinned here: route timeouts must accommodate
multi-minute streaming completions (Envoy's 15 s default kills them), and only **request** bodies
may be buffered (for model routing) — SSE responses stream unbuffered or time-to-first-token
dies.

## Consequences

- ADR-0008's "endpoint is ClusterIP-internal only" becomes "internal by default; public exactly
  through the Gateway."
- cert-manager joins the cluster as a Flux-managed HelmRelease; a Cloudflare API token Secret
  joins the out-of-band secrets (`hf-token`, deploy key) — and later the M5b attestation-gated
  delivery list.
- The operator image gains a second entrypoint; the authz sidecar inherits the operator's
  TDD/CI/review discipline (it is security-critical code).
- ADR-0007's render contract extends: Gateway golden tests sit beside the workload golden tests.
