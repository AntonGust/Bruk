---
status: accepted
---

# Airon Operator security posture (v1alpha1)

A pre-implementation security assessment of the operator was re-checked against the code that shipped in Phase 3.2 (PR #2). Most recommended controls were **already satisfied by the implementation**; this ADR records the audit outcome, the hardening applied on top, and the explicit future non-goals. The operator remains a privileged **platform control plane** (not a customer-facing API): today only Flux writes its CRs, and it ships gated (suspended HelmRelease).

## Already satisfied by the implementation (verified, no change needed)

- **Least-privilege secret access:** the operator has `secrets: get` only — no `list`, no `watch`. The BrukModel reconciler reads `secret.Data[key]` solely for a length check and never materializes, logs, or copies the value.
- **No sensitive data on observable surfaces:** no HF token, no raw `initDataB64` blob, no pod logs / stack traces reach any status field, condition message, log line, or event. Status carries only a 12-char `appliedInitDataHash`; the blob appears only in the `cc_init_data` pod annotation it is designed to populate. The operator emits no Kubernetes Events.
- **Admission-enforced guardrails** (CEL / OpenAPI markers, rejected at write time): `gpus <= 1`; digest-pin regex on the image; `modelRef` immutability and same-namespace-only shape (`LocalRef` has no namespace field); `trustedStore` exactly-one-of; model source present; `BrukTenant` singleton name `cluster`.
- **No pod-spec passthrough:** there are no `env` / `args` / `extraArgs` / `volumeMount` / `hostPath` / `nodeSelector` / `tolerations` fields anywhere; `trust_remote_code` is unreachable; `runtimeClassName` and `replicas: 1` are hardcoded in the renderer; the endpoint is ClusterIP-internal only.
- **Reconciler-enforced safety** (async, via status conditions): one-workload-per-trusted-store-PVC (LUKS double-format guard, also detects unmanaged Deployments/Pods), `localVolume` rejected as `NotImplemented`, `maxModelLen <= catalog.contextLength`.
- **Delivery:** `main` is PR-protected (ruleset `main-requires-pr-and-operator-ci`); the operator ships gated (`suspend: true`); deployment pins the image by digest.

## Hardening applied in this change

1. **Dropped the per-workload `engine.image` override.** The serving image is always `BrukTenant.spec.engine.defaultImage` — one reviewed, digest-pinned path kept lockstep with the seeded mirror (a lockstep test enforces it). The override was optional + digest-pinned but host-unrestricted, and became public API surface on ship. If per-workload images are ever needed, they return as an **admin-only field / `BrukEngineProfile`**, not a general `InferenceService` knob.
2. **Tightened RBAC to least-privilege.** The operator can no longer create, delete, or mutate CR specs — only `get;list;watch` the three CR kinds and write their `/status`. Unused `create/update/patch/delete` verbs and finalizers rules from the scaffold were removed. `secrets: get` stays; child Deployments/Services/ConfigMaps keep their verbs. A CI job (`generated`) fails the build if a widened `+kubebuilder:rbac` marker isn't regenerated and committed — the RBAC-review gate.
3. **Resource ceilings** (defense-in-depth against resource exhaustion): CEL `quantity()` caps — `memory.limit <= 512Gi`, `cpu.limit <= 128` — plus a `maxModelLen` schema maximum. Admission-enforced on k8s 1.35 (verified via envtest). Well above real workloads (the 24B uses 64Gi / 8 CPU).
4. **Supply-chain CI:** `govulncheck` job + `make vulncheck` (which immediately caught and fixed two called vulnerabilities in `golang.org/x/net`); Dependabot for Go modules and GitHub Actions; all workflow actions pinned by commit SHA.
5. **Public operator image.** `ghcr.io/antongust/bruk-operator` is public — the image is public software (no secrets or private config baked in; distroless + the compiled binary only; permissions come from RBAC at runtime; digest-pinned; GHA-built). This removes the `ghcr-pull` secret and simplifies unsuspend. Workload/model secrets (`hf-token`) remain private.
6. **Unpinned-revision warning:** a BrukModel with no `huggingFace.revision` is still `Ready`, but with a non-fatal `UnpinnedRevision` reason so the "weights may change after review" risk is visible in `kubectl get bm`.
7. **Leak-guard regression tests:** assert the HF token value and the raw initdata blob never appear in any CR status condition, and that validation errors never echo the blob.

## Explicit non-goals (deferred; recorded so the schema doesn't imply them)

- **Admin/customer-scoped RBAC and who-may-write-`BrukTenant` enforcement.** Today only Flux writes CRs and there are no non-platform actors, so this is convention (`BrukTenant` doc comment) not enforcement. It becomes real work when a portal / tenant RBAC model exists — customers must never write Kubernetes CRs directly; a website/API validates input and generates CRs.
- **Model allowlist / catalog gating + metadata scanning.** Gated on "bring-your-own Hugging Face model" becoming a real customer path. Until then the model library is platform-curated.
- **Validating admission webhook.** Would make the reconciler-only checks (PVC conflict, `localVolume`, `maxModelLen <= contextLength`) synchronous at write time. Deferred — status conditions are acceptable while the operator is platform-only.
- **Seeded-digest verification.** Nothing currently verifies that a digest-pinned image was actually seeded into the mirror; this is enforced at runtime by the mirror + image-rs, not by the operator.
- **Required `revision`.** Kept optional + warned (a breaking change to require it belongs to a later API revision).

## Consequences

- The operator's blast radius if compromised is bounded: it cannot read secret values, cannot fabricate or destroy CRs, cannot escalate via pod-spec fields, and runs non-root with a restricted securityContext.
- `BrukTenant` and its `initDataB64` remain sensitive control-plane config; protecting *who* writes them is future RBAC work, tracked above.
