# Airon Operator

A **deterministic** Kubernetes operator (Go + controller-runtime, ADR-0002) that reconciles
Bruk's customer-facing CRDs into the confidential vLLM serving stack:

| Kind (`bruk.airon.ai/v1alpha1`) | Scope | Role |
|---|---|---|
| `BrukTenant` | cluster (singleton `cluster`) | Per-cluster platform config: node, storage VG, default engine image, initdata blob |
| `BrukModel` | namespaced | Model identity + OpenRouter-style catalog metadata |
| `InferenceService` | namespaced | One confidential vLLM endpoint serving a BrukModel |

The operator renders exactly what the hand-written manifests do today
(`manifests/h100-vllm-cc-smoke.yaml`, `manifests/h100-vllm-cc.yaml`) — golden tests in
`internal/render` enforce that contract. No LLM in the loop; every change flows through
Git/Flux (see `docs/adr/0002-airon-operator-is-deterministic.md`).

## Development

Toolchain (see `docs/plan-2026-07.md` Phase 3.2): Go 1.26.x, kubebuilder v4.15,
kind v0.32 (+ `kindest/node:v1.35.5` — matches the k3s v1.35.5 reference cluster),
envtest pinned to 1.35.0 in the Makefile.

```sh
make test            # envtest unit/integration suite
make test-coverage   # same + coverage gate (>= 80% over api/ + internal/, generated files excluded)
make lint            # golangci-lint
make test-e2e        # kind-based e2e (creates/deletes a kind cluster)
```

## Review contract (code review before merge)

Everything under `operator/**` and `.github/workflows/operator-*` lands through a
**pull request** with the `operator-ci` checks green (lint, test + coverage gate, e2e,
image). Self-review of the full diff plus an AI-assisted review pass; findings are
addressed in the PR. Direct pushes to `main` remain for the established
infra/docs/gitops workflow and are **never** used for operator code. Note: GitHub-side
enforcement (branch ruleset) requires GitHub Pro on private repos — until the plan is
upgraded or the repo goes public, this contract is discipline, not machinery. The
stakes are real either way: Flux consumes `main` directly (`gitops/gotk-sync.yaml`),
so the PR gate also protects the live cluster's config source.

## Delivery

CI builds `ghcr.io/antongust/bruk-operator` (private). Release tags `operator/vX.Y.Z`
push versioned images; deployment pins `tag@digest`. The operator ships as a Helm chart
(`operator/dist/chart`) through a Flux `HelmRelease` sourced from this repo's existing
`GitRepository` — see `gitops/helm/operator.yaml` (suspended until cluster rollout).
