# GitOps (Flux) — Level-2 delivery, Phase 3.1

The per-cluster app layer reconciles from this repo via Flux (see `docs/plan-2026-07.md` Phase 3
and `docs/deployment-model.md`). One object is applied by hand (`gotk-sync.yaml`); everything
else converges from Git and self-heals.

```
bootstrap.sh (step 7)                      Git (this repo, branch main)
  flux install (controllers, pinned)          │
  kubectl apply -f gitops/gotk-sync.yaml ──►  GitRepository "bruk"  (ssh + read-only deploy key)
                                              Kustomization "bruk-apps" → ./gitops/apps/
                                                ├─ bruk-registry → ./manifests/registry/
                                                │    (mirror + digest-pinned seed job)
                                                └─ bruk-cluster  → ./manifests/
                                                     (trusted-storage PVs/PVCs + both CC workloads,
                                                      ${INITDATA_B64} substituted post-build)
```

## Current scope

- **App layer (Stage A):** registry mirror, seed job, trusted-storage PVs/PVCs, `vllm-cc-smoke`,
  `vllm-cc` (incl. the `gai-ipv4-first` ConfigMap). Delete/drift any of these → Flux restores.
- **Helm layer (Stage B):** Cilium, GPU Operator, kata-deploy as HelmReleases (`gitops/helm/`),
  values sourced from the SAME `install/helm-values/*.yaml` files bootstrap uses (published as
  ConfigMaps by `bruk-helm-values`). Adopted from the CLI-installed releases 2026-07-02 after a
  rendered-manifest diff proved no-op (only hook artifacts + regenerated Hubble certs).
  `bootstrap.sh` still does the FIRST install imperatively (Cilium is the CNI — Flux can't run
  without it); Flux owns lifecycle from then on.
  ⚠️ `ccManager.defaultMode` in `gpu-operator.yaml` is live GPU state, not just config — "off"
  on a live CC node force-flips CC mode node-wide. Treat values edits as cluster changes.
- **Deliberately out of Git:** `hf-token` secret; host-side LVM (`host-setup.sh` / RUNBOOK §7);
  BIOS/firmware.

## Operational notes

- **Changing `initdata.toml`** → regenerate the blob (`bash manifests/registry/build-initdata.sh`)
  and paste it into `gitops/apps/cluster.yaml` (`postBuild.substitute.INITDATA_B64`). This rolls
  both CC pods; the 24B re-downloads ~90 GB weights (~30 min).
- **Suspend/resume** during manual experiments on the box:
  `flux suspend kustomization bruk-cluster` / `flux resume ...` (or annotate with kubectl if the
  flux CLI isn't installed).
- The deploy key is read-only and per-cluster; rotate by replacing the `bruk-deploy-key` secret
  and the GitHub deploy key (recipe in `gotk-sync.yaml` header).
