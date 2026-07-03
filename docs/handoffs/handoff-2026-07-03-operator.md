# Bruk — Session Handoff (2026-07-03, Phase 3.2 / operator)

Follows `handoff-2026-07-03.md` (M1+M2+M3). This session built **Phase 3.2 — the Airon
Operator (M4 entry)** on branch `feat/operator-scaffold`, opened as **PR #2**. Nothing is on
`main` yet and nothing is deployed to the box — the delivery is gated (suspended HelmRelease).

---

## TL;DR — what got built

A deterministic Go + controller-runtime operator (`operator/`, ADR-0002/0007) reconciling
`bruk.airon.ai/v1alpha1` CRDs into the confidential vLLM stack:

- **`BrukModel`** (namespaced) — model identity + OpenRouter-style catalog metadata (the
  product decision this session: design the catalog surface now, build the `/v1/models` +
  routing service later).
- **`InferenceService`** (namespaced) — one CC vLLM endpoint per BrukModel; `internal/render`
  output is golden-proven identical to `manifests/h100-vllm-cc-smoke.yaml` /
  `h100-vllm-cc.yaml`. Status splits `Configured` / `WorkloadAvailable` / `Ready`.
- **`BrukTenant`** (cluster-scoped singleton `cluster`) — per-cluster config: node, storage VG,
  digest-pinned default image, `initDataB64` (same Flux `postBuild.substitute` pipe as today).

Reconcilers: InferenceService renders Deployment+Service(+gai ConfigMap) via server-side apply;
BrukModel/BrukTenant validate + set Ready. One-pod-per-PVC guard protects against LUKS
double-format (also detects unmanaged Deployments/Pods — adoption-safe).

**Verified locally:** `make test-coverage` 84.2 % (gate 80 %), `make lint` 0 issues, `make
test-e2e` 4/4 on kind (installs via the Helm chart; asserts `Configured=True /
WorkloadAvailable=False` — no GPU/kata in kind, by design), `helm lint` clean.

## Delivery — gated, nothing live

- Image: `ghcr.io/antongust/bruk-operator` (private). CI (`operator-ci.yml`) builds on PR,
  pushes `sha-`/`main` on merge to main; `operator-release.yml` pushes `vX.Y.Z` on tag
  `operator/v*` and prints the digest to pin.
- Helm chart owned in Git at `operator/dist/chart` (CRDs as templates, `resource-policy: keep`,
  `imagePullSecrets` added).
- `gitops/helm/operator.yaml` — HelmRelease `airon-operator` with **`suspend: true`**, chart
  from the existing `bruk` GitRepository. Committing it is inert (the gate lives in-file because
  `gitops/helm/` is auto-globbed). Values in `install/helm-values/airon-operator.yaml`.

## Box baseline (unchanged this session)

`ssh ubuntu@77.87.121.15`, **kubectl needs sudo** (k3s root kubeconfig). Confirmed still
Running: `registry`, `vllm-cc` (24B), `vllm-cc-smoke`, all Flux controllers. No
`airon-operator-system` namespace. The hand-written workloads were **not touched**.

## Next steps (all genuinely post-merge — need a human)

1. **Merge PR #2** once CI is green (branch ruleset couldn't be created — private repo needs
   GitHub Pro; the PR/review contract is discipline, documented in `operator/README.md`).
2. **Tag `operator/v0.1.0`** → release workflow pushes the image; capture the digest.
3. **GHCR checklist:** confirm the `bruk-operator` package is Private and repo-linked.
4. **Contract-C inertness check on the box:** after Flux reconciles main, `sudo flux get
   helmreleases -n flux-system` shows `airon-operator Suspended`; `sudo kubectl get ns
   airon-operator-system` NotFound; no new pods; CC workloads still Running.
5. **Unsuspend (a later phase, explicit go-ahead):** create out-of-band `ghcr-pull` secret, pin
   the values image to `v0.1.0@sha256:<digest>`, flip `suspend: false`.
6. **First adoption slice (later):** retire `vllm-cc-smoke` for the sample CR pair per the
   migration steps in `operator/README.md` (the PVC guard makes the cutover fail-safe).

## Toolchain pins (installed to `~/.local` this session)

Go 1.26.4, kubebuilder v4.15.0, kind v0.32.0 (+ `kindest/node:v1.35.5@ce977ae6`), helm v3.21.2,
flux v2.9.0, golangci-lint v2.12.2, envtest 1.35.0. controller-runtime v0.24.1 / k8s.io v0.36.0
(client +1 minor vs the k3s v1.35.5 cluster; envtest pins the tested API at 1.35.0).

## Commits (branch `feat/operator-scaffold`, 8)

scaffold → CI skeleton → CRD schemas → render (golden TDD) → reconcilers (envtest) → Helm+e2e
→ gated gitops → docs (ADR-0007).
