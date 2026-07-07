# RUNBOOK — Airon Operator go-live (M4 completion)

Turns the **already-built, gated** Airon Operator into a running, CR-driven serving controller on
the reference cluster (`ubuntu@<build-box>`, node `anton-bruk`), and adopts the first hand-written
workload (`vllm-cc-smoke`) as CRs. Do the steps **in order** — later steps assume earlier ones.

> **Box access:** use `sudo k3s kubectl` (k3s bundles kubectl + knows its root kubeconfig).
>
> **No `flux` CLI on the box** — only the in-cluster controllers are installed. Drive Flux with
> kubectl:
> - **force reconcile:** `sudo k3s kubectl -n flux-system annotate --overwrite <kind>/<name> reconcile.fluxcd.io/requestedAt="$(date +%s)"`
>   (`<kind>` = `gitrepository`, `kustomization`, or `helmrelease`)
> - **suspend / resume:** `sudo k3s kubectl -n flux-system patch <kind>/<name> --type=merge -p '{"spec":{"suspend":true}}'` (or `false`)
> - **status:** `sudo k3s kubectl get helmrelease,kustomization -n flux-system`
>
> **Inertness contract (holds through step 3):** an installed-but-CR-less operator renders no
> workloads. The hand-written CC workloads (`vllm-cc`, `vllm-cc-smoke`) are untouched until step 4.

Artifacts this runbook drives:
- Image: `ghcr.io/antongust/bruk-operator:v0.1.0` @ `sha256:2cf1b21a0175918ca41df2591c4c8784f2e0440efd2cfd68c00eaeb585301121`
- **PR #16** — unsuspend (pin digest + `suspend: false`), draft
- **PR #17** — adopt `vllm-cc-smoke` as CRs, draft

---

## Step 1 — Release the image ✅ DONE
Tag `operator/v0.1.0` pushed; `operator-release` workflow built and pushed the image and printed the
digest above. Nothing to do; recorded here for the chain of custody.

## Step 2 — Make the GHCR package public  (manual, GitHub UI)
ADR-0008: the operator image is public software (no secrets/config baked in), so no pull secret is
needed. On first push the package is **private** — flip it once:

1. Go to `https://github.com/users/AntonGust/packages/container/bruk-operator/settings`
2. **Danger Zone → Change visibility → Public**
3. (Optional) link it to the `Bruk` repo.

**Verify:** `curl -sI https://ghcr.io/v2/antongust/bruk-operator/manifests/v0.1.0` returns without a
401 (or an anonymous `docker manifest inspect ghcr.io/antongust/bruk-operator:v0.1.0` succeeds).

## Step 3 — Unsuspend the operator  (merge PR #16, then reconcile)
**Gate:** step 2 must be done first, or the manager pod ImagePullBackOffs.

1. Mark **PR #16** ready for review; merge it. It pins the digest in
   `install/helm-values/airon-operator.yaml` and flips `gitops/helm/operator.yaml` `suspend: false`.
2. On the box, force Flux to pull the change (git → Kustomization → HelmRelease):
   ```
   sudo k3s kubectl -n flux-system annotate --overwrite gitrepository/bruk reconcile.fluxcd.io/requestedAt="$(date +%s)"
   sleep 15   # let bruk-apps + bruk-helm re-apply the HelmRelease manifest
   sudo k3s kubectl -n flux-system annotate --overwrite helmrelease/airon-operator reconcile.fluxcd.io/requestedAt="$(date +%s)"
   ```
3. **Verify the operator runs, RBAC-scoped, and inert:**
   ```
   sudo k3s kubectl get helmrelease -n flux-system               # airon-operator: Ready=True
   sudo k3s kubectl -n airon-operator-system get deploy,pod       # manager Running; pulled by digest
   sudo k3s kubectl -n airon-operator-system get pod -o jsonpath='{.items[0].spec.containers[0].image}'; echo
   ```
   ⚠️ If a prior install left the release `failed`/`pending-install`, helm-controller normally
   remediates on the spec change; if it's stuck, suspend+resume the HelmRelease to force a retry:
   `... patch helmrelease/airon-operator --type=merge -p '{"spec":{"suspend":true}}'` then `false`.
4. **Confirm inertness** — no Bruk CRs applied yet, so nothing is rendered; the hand-written
   workloads are unchanged:
   ```
   sudo k3s kubectl get brukmodels,inferenceservices,bruktenants -A   # none yet
   sudo k3s kubectl get deploy vllm-cc vllm-cc-smoke -n default        # both still Running, untouched
   ```

## Step 4 — Adopt `vllm-cc-smoke` as CRs  (on-box cutover around PR #17)
**Gate:** step 3 done (CRDs installed, operator running). This is a sequenced cutover, **not**
merge-and-walk-away — the one-pod-per-PVC guard refuses to render while the legacy Deployment still
mounts the trusted-store claim (that's the LUKS double-format protection working).

1. Stop Flux recreating the static manifest:
   ```
   sudo k3s kubectl -n flux-system patch kustomization/bruk-cluster --type=merge -p '{"spec":{"suspend":true}}'
   ```
2. Delete the hand-written Deployment + Service — **keep** the `trusted-image-smoke` PVC and its data:
   ```
   sudo k3s kubectl delete deploy/vllm-cc-smoke svc/vllm-cc-smoke-svc -n default
   ```
3. Merge **PR #17** (adds `BrukTenant`/`BrukModel`/`InferenceService`, drops the smoke manifest from
   `manifests/kustomization.yaml`), then resume + force reconcile:
   ```
   sudo k3s kubectl -n flux-system patch kustomization/bruk-cluster --type=merge -p '{"spec":{"suspend":false}}'
   sudo k3s kubectl -n flux-system annotate --overwrite gitrepository/bruk kustomization/bruk-cluster reconcile.fluxcd.io/requestedAt="$(date +%s)"
   ```
4. **Verify** the operator renders the identical workload and status is honest:
   ```
   sudo k3s kubectl get bt cluster                                  # Ready; AppliedInitDataHash set
   sudo k3s kubectl get inferenceservice vllm-cc-smoke -n default   # Configured=True, then Ready=True
   sudo k3s kubectl get deploy vllm-cc-smoke -n default -o yaml      # zero spec diff vs the old manifest
   ```
   The pod re-runs guest-pull onto the existing trusted store (~15–25 min to Ready). The 24B
   (`vllm-cc`) is untouched.

**Done:** M4 complete — an `InferenceService` CR drives a running confidential workload.

---

## Rollback  (force-reconcile after each revert: annotate `gitrepository/bruk` + the Kustomization)
- **Step 3:** revert PR #16 (`suspend: true`) and reconcile `bruk-helm` — the operator is removed;
  no workload was touched.
- **Step 4:** revert PR #17 (re-adds `h100-vllm-cc-smoke.yaml` to the kustomization) and reconcile
  `bruk-cluster`; delete the CRs
  (`sudo k3s kubectl delete inferenceservice/vllm-cc-smoke brukmodel/qwen-0.5b -n default`). Flux
  re-applies the hand-written workload onto the same PVC. The PVC/data are never deleted, so the
  store survives either direction.

## Next (post-M4)
Adopt the 24B (`vllm-cc`) the same way — its own CR pair with `weightsCache: {}` and
`tokenSecretRef: hf-token` (`operator/README.md`). Then Phase 3.3 (fleet plane / bare-metal, M5).
