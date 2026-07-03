---
status: accepted
---

# The Airon Operator's v1alpha1 API surface: three CRDs under `bruk.airon.ai`, catalog-ready, CC-invisible

Phase 3.2 (ADR-0002) required drafting the actual CRD schemas. Decisions made 2026-07-03:

**API group `bruk.airon.ai/v1alpha1`** (kubebuilder domain `airon.ai`, group `bruk`) — product-scoped
under the company domain, leaving room for future non-Bruk groups. Three Kinds:

- **`BrukModel`** (namespaced) — model identity (`source.huggingFace{repo,revision,tokenSecretRef}`,
  `servedName`) plus **OpenRouter-style catalog metadata** (`catalog{displayName, description,
  contextLength, modalities, features, license, pricing}`). We decided to *design for* an
  OpenRouter-compatible API (rich `GET /v1/models` + OpenAI-compatible completions routing) now and
  *build it later*: the CRs carry all catalog data, so the future catalog service is a read-only
  join of BrukModel specs with Ready InferenceService statuses — no side-channel state.
- **`InferenceService`** (namespaced) — one confidential vLLM endpoint for a same-namespace
  BrukModel (`modelRef` immutable). Customer-facing knobs are minimal (`modelRef`,
  `engine.maxModelLen`, `engine.quantization`); sizing/storage/image are platform-set. Status splits
  **`Configured`** (rendered+applied) from **`WorkloadAvailable`** (Deployment Available), with
  `Ready` = both — on clusters without CC/GPU capacity (kind, CI) `Configured=True +
  WorkloadAvailable=False` is a legitimate terminal state. Status also carries `endpoint.url`,
  `servedModelName`, `resolvedImage`, `appliedInitDataHash`: CC pods have empty logs, status must
  debug without them.
- **`BrukTenant`** (cluster-scoped singleton, name CEL-enforced `cluster`) — **the single-tenant
  cluster contract**, not an end-user tenant abstraction (ADR-0001: the cluster IS the tenant
  boundary; the Kind name comes from ADR-0002/CONTEXT.md and is kept for continuity). Carries the
  per-cluster platform config the reconcilers need: `infrastructure{nodeHostname,
  storageVolumeGroup}`, `engine.defaultImage` (digest-pinned, test-enforced lockstep with the
  mirror seed job), `confidential.initDataB64` (the blob keeps today's exact Flux
  `postBuild.substitute` delivery pipe — only the destination changed from static manifests to a CR).

**Validation split** (reviewed decision): OpenAPI/CEL enforces *schema-local* invariants only
(singleton name, trustedStore exactly-one union, modelRef immutability, digest/base64 patterns,
ranges); everything needing *live state* is reconciler status logic with typed reasons
(`ModelNotFound`, `TenantConfigMissing`, `TrustedStoreConflict`, `InvalidConfig`,
`NotImplemented`); admission webhooks are deferred.

**One-pod-per-PVC rule** (LUKS double-format protection): a CR loses the trusted-store claim if an
older sibling CR references it (creationTimestamp, name tie-break) or if any unmanaged
Deployment/standalone Pod mounts it — the latter protects the hand-written workloads during
adoption.

**Render contract**: golden tests decode the real committed `manifests/h100-vllm-cc-smoke.yaml` /
`h100-vllm-cc.yaml` and require semantic identity of the operator's output (template, selector,
strategy, service ports; quantities semantic; env/volumes order-insensitive) — cutover to
CRs is therefore a provable no-op.

## Explicit v1alpha1 non-goals (the schema must not promise them)

No CC on/off field (node-level property; the runtimeClass is a hardcoded constant — a per-CR
toggle is the 19-minute-outage trap). No BYO customer images. No KBS/Trustee fields (ADR-0006
roadmap). No multi-GPU/replicas/autoscaling (SPT is the only validated CC topology until HGX
B300). No extraArgs/podSpec passthrough (ADR-0002's audited deterministic surface). No public
endpoint/auth fields (Envoy phase).

## Consequences

- The operator ships as `ghcr.io/antongust/bruk-operator` (private) + in-repo Helm chart
  (`operator/dist/chart`) via a **suspended** HelmRelease (`gitops/helm/operator.yaml`) sourced
  from the existing `bruk` GitRepository; unsuspending is a reviewed one-line commit gated on the
  out-of-band `ghcr-pull` secret and a digest-pinned values file.
- Migration of the hand-written workloads (see `operator/README.md` adoption notes): per workload —
  `flux suspend kustomization bruk-cluster`, remove the static manifest from
  `manifests/kustomization.yaml`, apply the equivalent CR pair, resume; the one-pod-per-PVC rule
  refuses to render while the legacy Deployment still mounts the claim, making the cutover
  fail-safe against double-LUKS-format.
- `spec.engine.defaultImage` and the seed job must be updated together (single commit); a Go test
  (`operator/internal/lockstep`) fails CI when they diverge.
