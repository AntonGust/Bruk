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
addressed in the PR. This is enforced by a GitHub **ruleset on `main`**
(`main-requires-pr-and-operator-ci`): pull request required, status checks `changes` /
`lint` / `test` / `e2e` / `image` must pass, with a repo-admin bypass reserved for the
established infra/docs/gitops direct-push workflow — **never** used for operator code.
The stakes are real: Flux consumes `main` directly (`gitops/gotk-sync.yaml`), so the
PR gate also protects the live cluster's config source.

## Adopting the hand-written workloads (future cutover, per workload)

The golden tests prove the operator renders exactly what `manifests/h100-vllm-cc-smoke.yaml` /
`h100-vllm-cc.yaml` contain, so the cutover is a rename-level change — but it must be sequenced,
because the one-pod-per-PVC guard refuses to render while the legacy Deployment still mounts the
trusted-store claim (that's the LUKS double-format protection working):

1. `flux suspend kustomization bruk-cluster` (stop Flux recreating the static manifest).
2. Remove the workload's manifest from `manifests/kustomization.yaml`; delete the Deployment+Service
   (`kubectl delete -f manifests/h100-vllm-cc-smoke.yaml` minus the PVC — the trusted-store PVC and
   its data stay).
3. Commit the BrukModel + InferenceService pair (see `config/samples/`) into the Flux-applied path;
   `flux resume kustomization bruk-cluster`.
4. The operator renders the identical Deployment/Service; the pod re-runs guest-pull onto the
   existing store. Verify `Configured=True` then `Ready=True`, zero spec diff vs the old manifest.

Mapping: manifest fields → CR fields are documented field-by-field in ADR-0007 and enforced by
`internal/render` golden tests. The 24B follows the same steps with its own CR pair
(`weightsCache: {}`, `tokenSecretRef: hf-token`).

## Delivery

CI builds `ghcr.io/antongust/bruk-operator` (**public** — ADR-0008: the image is public
software with no secrets or private config baked in; no pull secret needed). Release tags
`operator/vX.Y.Z` push versioned images; deployment pins `tag@digest`. The operator ships
as a Helm chart (`operator/dist/chart`) through a Flux `HelmRelease` sourced from this
repo's existing `GitRepository` — see `gitops/helm/operator.yaml` (suspended until cluster
rollout).

## Security posture

See `docs/adr/0008-operator-security-posture.md`. In brief: least-privilege RBAC (the
operator only reads its CR kinds + writes status; `secrets: get`-only, never reads the
value); no secret/initdata-blob leakage into status or logs (status carries only a
12-char hash); a single reviewed digest-pinned serving image (no per-workload override);
no `env`/`args`/`extraArgs`/`hostPath`/`nodeSelector` passthrough; `runtimeClassName`
fixed; supply-chain CI (govulncheck, Dependabot, SHA-pinned actions).
