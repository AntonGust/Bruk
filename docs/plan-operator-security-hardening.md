# Airon Operator — security hardening (post-implementation, pre-unsuspend)

## Context

The Phase 3.2 Airon Operator is built, merged to `main` (PR #2), and shipping through Flux as a **suspended** HelmRelease (not serving; no customers on the cluster). A pre-implementation security assessment was re-checked against the code that actually shipped. **Most of it was already satisfied by the implementation** — verified:

- Operator RBAC has `secrets: get` **only** (no `list`/`watch`); the reconciler reads `secret.Data` solely for a length check and never materializes the value.
- No secret values, no full `initDataB64` blob, no raw pod logs / stack traces reach any status field, condition message, log line, or event (status carries only a 12-char `appliedInitDataHash`; the blob appears only in the `cc_init_data` pod annotation it is designed to populate).
- Admission-enforced (CEL/marker): `gpus<=1`, digest-pin regex on images, `modelRef` immutability + no-namespace shape, trustedStore XOR, model-source-present, BrukTenant singleton name.
- No `env`/`args`/`extraArgs`/`volumeMount`/`hostPath`/`nodeSelector`/`tolerations` passthrough fields exist; `trust_remote_code` is unreachable; `runtimeClassName` and `replicas: 1` are hardcoded; endpoint is ClusterIP-internal only.
- `main` branch protection is active (ruleset `main-requires-pr-and-operator-ci`).

This plan implements the **genuine remaining gaps**, scoped to what matters before the operator is unsuspended. Customer-facing controls (admin/customer RBAC, model allowlist, admission webhook) are recorded as future work — they only bite once non-platform actors write CRs.

### Decisions locked with the user
- **Scope:** pre-unsuspend hardening only (cheap, clearly-good fixes; no big future scaffolding).
- **`engine.image` override:** **drop it in v1alpha1.** One reviewed, digest-pinned serving path via `BrukTenant.spec.engine.defaultImage`, kept lockstep with the seeded mirror. Reintroduce later as an admin-only field / `BrukEngineProfile` if per-workload images are ever needed. (No CEL mirror-host restriction — hardcoding mirror DNS into the CRD is brittle.)
- **HF `revision`:** keep optional (non-breaking), but surface a non-fatal warning condition when unpinned.
- **Operator image:** **make it public.** Verified no secrets/config baked in (distroless + binary only), RBAC-controlled at runtime, digest-pinned at release, GHA-built. Drops the `ghcr-pull` secret. Workload/model secrets (`hf-token`) stay private.

## Changes

1. **Drop `engine.image` override.** Remove the `Image` field from `EngineSpec`; `ResolveImage` collapses to tenant-default-only; remove the override golden test; regenerate CRDs.
2. **Tighten operator RBAC.** Edit `+kubebuilder:rbac` markers so the operator can no longer create/delete CRs nor mutate CR specs — only `get;list;watch` the CR kinds and write `/status`. Deployments/Services/ConfigMaps/Pods verbs stay; `secrets: get` stays.
3. **CI supply-chain hardening.** Add `govulncheck` job + `make vulncheck` (and to the ruleset required checks); `.github/dependabot.yml` (gomod `/operator` + github-actions, weekly); SHA-pin all workflow actions; add a "generated artifacts up-to-date" CI guard (`make manifests generate && git diff --exit-code config/ dist/chart/`).
4. **Public image.** Remove `imagePullSecrets: [ghcr-pull]` from values; rewrite the `gitops/helm/operator.yaml` unsuspend checklist (drop the secret step); document the one-time "set GHCR package public" step.
5. **Resource max-bounds.** CEL `quantity()` caps on `memory.limit`/`cpu.limit`/storage sizes + a `maxModelLen` schema maximum (belt-and-suspenders over the reconciler's contextLength check). Verify CEL compiles on envtest 1.35; reconciler `InvalidConfig` fallback if not.
6. **Leak-guard test + revision warning.** Table test asserting no token/blob bytes in any status message or log; `brukmodel` reconciler attaches a non-fatal `UnpinnedRevision` reason when `revision` is empty.
7. **Docs.** New ADR-0008 "Operator security posture (v1alpha1)" recording the audit outcome, the decisions, and explicit future non-goals (admin/customer RBAC, model allowlist/scan, validating webhook, seeded-digest verification, required revision). Update `operator/README.md`.

## Verification
1. `make manifests generate` clean (the new CI guard).
2. `make test-coverage` green incl. leak-guard + revision-warning; coverage ≥ 80%.
3. `make lint` and `make vulncheck` clean.
4. `make test-e2e` on kind — chart installs with tightened RBAC; operator still renders workload + reports status.
5. CR exceeding a resource cap rejected at admission (or `InvalidConfig` via reconciler fallback).
6. Built image public + pullable without a secret; values pin `tag@digest`.
7. Reviewed PR into `main` (ruleset-gated); operator stays **suspended**.

## Out of scope (future, recorded in ADR-0008)
Admin/customer RBAC + who-can-write-BrukTenant enforcement; model allowlist/catalog gating + metadata scanning; validating admission webhook; seeded-digest verification; making `revision` required; unsuspending the operator.
