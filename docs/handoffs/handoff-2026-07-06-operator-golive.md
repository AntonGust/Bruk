# Bruk — Session Handoff (2026-07-06, operator go-live / M4 complete)

Follows `handoff-2026-07-03-operator.md` (operator built + gated). This session **took the operator
live and completed M4**: security-hardened it, released `v0.1.0`, unsuspended it on the box, and
cut **both** confidential workloads over from hand-written manifests to CR-driven management — both
now `Ready` and serving. Also built a local `bruk-box` testing skill. All code is on `main`
(PRs #15–#22 merged).

---

## TL;DR — M4 is done

The Airon operator is **live on the reference box** (`anton-bruk`), RBAC-scoped, image pinned by
digest, and is the **sole manager of the confidential serving layer**:

- `vllm-cc-smoke` (Qwen2.5-0.5B) — rendered from CRs, `Ready`, serving (~391 tok/s).
- `vllm-cc` (Mistral-Small-3.1-24B, FP8) — rendered from CRs, `Ready`, serving (~97 tok/s single-stream,
  ~1450 tok/s aggregate at 16-way).

An `InferenceService` CR → running confidential workload, CI green. The hand-written
`manifests/h100-vllm-cc*.yaml` are retired from the Flux resource list (kept on disk **only** as
`internal/render` golden-test references).

## Go-live sequence (all merged)

1. **Security hardening (PR #15)** — from a `security-bounty-hunter` self-review (no CRITICAL/HIGH found).
   Three LOW/defense-in-depth fixes: (a) nil-deref guard in the BrukModel reconciler + fake-client test
   (CEL normally blocks it, but the reconciler shouldn't rely on that); (b) release-workflow shell-injection
   fix — pass the tag-derived version/digest via `env:`, not `${{ }}` into `run:`; (c) `bootstrap.sh` now
   installs a **pinned, sha256-verified** Helm (`v3.17.3`) instead of `curl … | bash` from `main`.
2. **Release `operator/v0.1.0`** → image `ghcr.io/antongust/bruk-operator:v0.1.0`
   @ `sha256:2cf1b21a0175918ca41df2591c4c8784f2e0440efd2cfd68c00eaeb585301121`.
3. **GHCR package made public** (ADR-0008: public image, no pull secret).
4. **Unsuspend (PR #16)** — pin the digest in `install/helm-values/airon-operator.yaml`, flip
   `gitops/helm/operator.yaml` `suspend: false`.
5. **Install-bug fix (PR #18)** — see gotcha below.
6. **Chart hardening (PR #19)** — sanitize the chart's `helm.sh/chart` label (`replace "+" "_"`).
7. **Adopt `vllm-cc-smoke` (PR #17)** and **`vllm-cc`/24B (PR #20)** as `BrukTenant`+`BrukModel`+
   `InferenceService` CRs; retire the hand-written manifests from `manifests/kustomization.yaml`.
8. **Gated-secret fix (PR #21)** — merged, **deploy parked** (see below).
9. **gitignore `.claude/` (PR #22)** — keep local Claude tooling out of the public repo.

## ⚠️ Gotchas discovered this session

- **`reconcileStrategy: Revision` broke the Helm install.** Flux's Revision strategy rewrites the chart
  version to `0.1.0+<git-sha>`; the kubebuilder chart's `helm.sh/chart` label stamped the **raw**
  `.Chart.Version`, and `+` is illegal in a k8s label value → server-side apply rejected every object →
  install failed. Fix: dropped `reconcileStrategy: Revision` (default `ChartVersion` keeps `0.1.0`, PR #18)
  **and** hardened the chart label to `replace "+" "_"` (PR #19). e2e missed it because it uses a plain
  `helm install` (static version). Trade-off of `ChartVersion`: chart *template* edits need a `Chart.yaml`
  bump; values changes still upgrade via `valuesFrom`.
- **Stale `TrustedStoreConflict` on cutover.** When the old hand-written Deployment is deleted, the new
  `InferenceService` may record a `TrustedStoreConflict` and **not auto-retry** (no watch on the deleted
  Deployment). Once the old Deployment is gone, nudge a re-reconcile:
  `sudo k3s kubectl annotate inferenceservice <name> -n default relaunch="$(date +%s)" --overwrite`.
  The old CC pod also stays `Terminating` a while (Kata teardown) and holds the RWO PVC, so the new pod
  waits on Multi-Attach until it's gone — self-resolves. The one-pod-per-PVC guard keeps it fail-safe.
- **The box has no `flux` CLI** (only in-cluster controllers). Drive Flux via `sudo k3s kubectl`:
  `annotate … reconcile.fluxcd.io/requestedAt="$(date +%s)"` to reconcile, `patch … --type=merge -p
  '{"spec":{"suspend":true|false}}'` to suspend/resume. kubectl is `sudo k3s kubectl`.
- **Gated models exposed a real RBAC/caching bug** (the 24B, PR #21). The BrukModel reconciler does a
  point `Get` on the `hf-token` Secret; controller-runtime's **cached client serves `Get` via a
  list+watch informer**, but least-privilege RBAC grants `secrets: get` only (ADR-0008) → `cannot list
  secrets` → gated BrukModel reconcile errors → its `Ready` stays **blank**. Fix (PR #21, Codex-reviewed):
  `Client.Cache.DisableFor: [corev1.Secret]` in `cmd/main.go` so `Get(secret)` goes direct (get-only
  preserved). **Does not block serving** (the kubelet injects `HF_TOKEN` at pod runtime). The ungated
  smoke model never hit it.

## Parked / open

- **`v0.1.1` deploy PARKED.** PR #21 is merged to `main` but **not released/deployed** — it's cosmetic
  (only the `BrukModel mistral-small-3.1-24b` `Ready` blank; serving is fine). To deploy later: tag
  `operator/v0.1.1` → release workflow prints the new digest → re-pin `install/helm-values/airon-operator.yaml`
  → Flux rolls **only the manager pod** (SSA no-op re-reconcile; workloads untouched) → the gated BrukModel
  flips to `Ready`.
- `docs/RUNBOOK-operator-go-live.md` was created this session (working-tree, uncommitted like the other
  docs) — the ordered release→unsuspend→adopt sequence with on-box kubectl commands.

## Tooling: `bruk-box` skill (local, gitignored)

`.claude/skills/bruk-box/` (kept out of the public repo — `.claude/` gitignored, PR #22). SSHes to the
box (key auth + passwordless sudo, both confirmed) and wraps testing:
`status | chat <model> <app> | bench <model> <app> | concurrency <model> <app> [maxtok] [levels] |
profile <model> <app> | kubectl … | ssh "…"`. Read-only. The box IP lives only in this gitignored
script (and `~/.ssh`), never in a tracked file.

**Perf baselines (2026-07-06, 24B FP8):** single-stream ~97 tok/s; batched ~1450 tok/s aggregate @ 16-way
(~15×, wall-clock ~flat — continuous batching); TTFT ~8 ms warm; decode ~10.3 ms/token (decode-bound,
prefill negligible). Smoke (0.5B) ~391 tok/s.

## Box state (verified this session)

`bruktenant/cluster` Ready; `inferenceservice/vllm-cc` + `/vllm-cc-smoke` Ready with endpoints; both
Deployments `1/1 Running`; all HelmReleases Ready (`airon-operator@0.1.0`, cilium, gpu-operator,
kata-deploy). `brukmodel/mistral-small-3.1-24b` `Ready` blank (the parked v0.1.1 bug — expected).

## Next steps

1. **Deploy `v0.1.1`** when convenient (tag → release → re-pin digest) to clear the gated-BrukModel `Ready`
   blank. Non-urgent.
2. **Phase 3.3 / M5** — fleet plane + bare-metal (MAAS/PXE, tenant/key registry, host-attestation verifier,
   KBS/Trustee for the Pattern-A upgrade). Hard gate: host-attestation verifier needs the discrete TPM (on
   order); sequence it after arrival, don't block the rest of 3.3 on it.

## Merged this session (all via PR, CI green)

#15 security hardening · #16 unsuspend · #18 drop reconcileStrategy Revision · #19 sanitize chart label ·
#17 adopt vllm-cc-smoke · #20 adopt 24B · #21 secret-cache fix (v0.1.1, parked) · #22 gitignore .claude/.
