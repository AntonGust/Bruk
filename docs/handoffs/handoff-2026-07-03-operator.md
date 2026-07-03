# Bruk — Session Handoff (2026-07-03, Phase 3.2 / operator + hardening)

Follows `handoff-2026-07-03.md` (M1+M2+M3). This session built **Phase 3.2 — the Airon
Operator (M4 entry)**, then merged it, scrubbed the box IP, took the repo public with an
enforced branch ruleset, and hardened the operator's security posture. **All of it is on
`main`** (PRs #2, #3, #4 merged). The operator ships **gated** (suspended HelmRelease) — it
is not serving and the hand-written CC workloads are untouched, verified on the box after
each merge.

---

## TL;DR — what's on main

A deterministic Go + controller-runtime operator (`operator/`, ADR-0002/0007/0008) reconciling
`bruk.airon.ai/v1alpha1` CRDs into the confidential vLLM stack:

- **`BrukModel`** (namespaced) — model identity + OpenRouter-style catalog metadata (design the
  catalog surface now, build the `/v1/models` + routing service later).
- **`InferenceService`** (namespaced) — one CC vLLM endpoint per BrukModel; `internal/render`
  output is golden-proven identical to `manifests/h100-vllm-cc-smoke.yaml` / `h100-vllm-cc.yaml`.
  Status splits `Configured` / `WorkloadAvailable` / `Ready`.
- **`BrukTenant`** (cluster-scoped singleton `cluster`) — per-cluster config: node, storage VG,
  digest-pinned default image, `initDataB64` (same Flux `postBuild.substitute` pipe as today).

Reconcilers: InferenceService renders Deployment+Service(+gai ConfigMap) via server-side apply;
BrukModel/BrukTenant validate + set Ready. One-pod-per-PVC guard protects against LUKS
double-format (also detects unmanaged Deployments/Pods — adoption-safe).

## Security posture (ADR-0008 — hardened PR #4)

- **Least-privilege RBAC:** operator only `get;list;watch` its CR kinds + writes `/status` —
  cannot create/delete/mutate CRs. `secrets: get`-only (existence check; never reads the value).
- **No leakage:** no HF token, no raw `initDataB64` blob, no logs/stack-traces in status/events;
  status carries only a 12-char `appliedInitDataHash`. Locked by leak-guard regression tests.
- **Single image path:** the per-workload `engine.image` override was **dropped**; serving image
  is always `BrukTenant.spec.engine.defaultImage` (digest-pinned, lockstep with the mirror seed).
- **Resource ceilings** (CEL `quantity()`, admission-enforced): `memory.limit<=512Gi`,
  `cpu.limit<=128`, `maxModelLen` max.
- **Supply-chain CI:** `govulncheck` (`make vulncheck`), Dependabot (gomod + actions), all
  workflow actions SHA-pinned; a `generated` CI job fails if RBAC/CRDs drift from source markers.
- **Public image** (no secrets/config baked in) → no `ghcr-pull` secret needed.
- **Non-goals (deferred, in ADR-0008):** admin/customer RBAC on who writes `BrukTenant`, model
  allowlist/metadata scan (gated on BYO-HF), validating webhook, seeded-digest verification,
  required `revision` (currently optional + non-fatal `UnpinnedRevision` warning).

## Delivery — gated, nothing live

- Image: `ghcr.io/antongust/bruk-operator` (**public** — ADR-0008). `operator-ci.yml` builds on
  PR, pushes `sha-`/`main` on merge; `operator-release.yml` pushes `vX.Y.Z` on tag `operator/v*`
  and prints the digest to pin.
- Helm chart owned in Git at `operator/dist/chart` (CRDs as templates, `resource-policy: keep`).
- `gitops/helm/operator.yaml` — HelmRelease `airon-operator` with **`suspend: true`**, chart from
  the existing `bruk` GitRepository. The gate lives in-file (`gitops/helm/` is auto-globbed).
  Values in `install/helm-values/airon-operator.yaml` (no imagePullSecrets).

## Repo / CI state (changed this session)

- **Repo is PUBLIC.** Branch ruleset `main-requires-pr-and-operator-ci` (id 18480008) is ACTIVE:
  PR required + checks `changes/lint/generated/test/vulncheck/e2e/image`, repo-admin bypass
  reserved for the infra/docs/gitops direct-push workflow.
- **Box IP scrubbed** from all committed docs (PR #3) → `<build-box>` placeholder; it remains in
  git *history* (not rewritten — non-secret IP behind the box's own auth).
- ⚠️ Box `kubectl`/`flux` need **sudo** (k3s root kubeconfig): `ssh ubuntu@<build-box>` (real IP
  in your ssh config / private notes).
- ⚠️ `.dockerignore` is a denylist (not the scaffolded `**`+`!**/*.go` allowlist — the legacy
  builder silently drops `cmd/main.go` with nested re-includes → image build breaks).

## Box state (verified inert after each merge)

`airon-operator` HelmRelease = `suspend: true`; no `airon-operator-system` namespace; no operator
pods; `registry`, `vllm-cc` (24B), `vllm-cc-smoke`, and all Flux controllers still Running. The
hand-written workloads were never touched.

## Next steps (need a human / explicit go-ahead)

1. **Release:** tag `operator/v0.1.0` → release workflow pushes the image + prints the digest.
2. **Set the GHCR package PUBLIC** on first push (one-time; repo is public, image should be too).
3. **Unsuspend (later phase):** pin `install/helm-values/airon-operator.yaml` to
   `v0.1.0@sha256:<digest>`, flip `suspend: false` in `gitops/helm/operator.yaml` (a reviewed
   one-line PR). No pull secret needed. Re-verify inertness flips to a running, RBAC-scoped
   manager that still touches nothing until a CR is applied.
4. **First adoption slice:** retire `vllm-cc-smoke` for the sample CR pair per the migration
   steps in `operator/README.md` (the PVC guard makes the cutover fail-safe against double-LUKS).
5. **CI polish (optional):** vulncheck flagged that CI built on unpatched Go 1.26.0 — fixed by
   requiring `go 1.26.4` in `go.mod`; Dependabot will keep deps + actions current going forward.

## Toolchain pins (installed to `~/.local` this session)

Go 1.26.4 (go.mod requires 1.26.4 — patched stdlib), kubebuilder v4.15.0, kind v0.32.0
(+ `kindest/node:v1.35.5@ce977ae6`), helm v3.21.2, flux v2.9.0, golangci-lint v2.12.2, envtest
1.35.0. controller-runtime v0.24.1 / k8s.io v0.36.0 (client +1 minor vs the k3s v1.35.5 cluster;
envtest pins the tested API at 1.35.0). `golang.org/x/net` bumped to v0.55.0 (vuln fix).

## Merged this session

- **PR #2** — operator build (scaffold → CI → CRD schemas → render golden TDD → reconcilers
  envtest → Helm+e2e → gated gitops → ADR-0007). Verified inert on box.
- **PR #3** — scrub build-box IP from committed docs.
- **PR #4** — security hardening (ADR-0008): drop image override, least-privilege RBAC,
  govulncheck/Dependabot/SHA-pinned actions, public image, resource caps, revision warning,
  leak-guard tests. Verified inert on box.
