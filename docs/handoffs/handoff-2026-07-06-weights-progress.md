# Handoff 2026-07-06 — Weights-download progress signal

> Spec for making weights-download progress a first-class `InferenceService` status
> field, so the Airon console (see `console/docs/react-remake-design.md` §6) can show
> a real progress bar instead of an indeterminate "starting…" state.
>
> Status: NOT STARTED — this document is the work order.

## Problem

During a cold start the pod spends ~30 min (24B FP8) downloading weights from
Hugging Face, but the operator can only report `Progressing=True/Deploying` /
`WorkloadPending` the whole time, because:

1. **The container is stock vLLM.** `render.go engineArgs()` passes args straight to
   vLLM; vLLM downloads weights itself at startup into the weights cache
   (`/root/.cache/huggingface`, block-encrypted emptyDir). Nothing reports download
   progress externally.
2. **CC pod stdout is sealed** — progress cannot be scraped from logs, by design.
3. **The reconciler is watch-driven only** (`inferenceservice_controller.go` —
   `Owns(Deployment)`, no `RequeueAfter`). Status changes only when the Deployment
   flips, i.e. exactly twice: not-Available → Available.
4. The only in-cluster signal is the readiness probe (`/health:8000`,
   60s initial delay + 240 × 10s failure budget ≈ 41 min with weightsCache) —
   binary, no progress.

The weights cache is ephemeral (re-downloads on **every pod recreate**), so this UX
gap recurs on every roll, not just first deploy.

## Constraints to respect

- **ADR-0008:** single reviewed engine image (`BrukTenant.spec.engine.defaultImage`,
  digest-pinned; no per-workload image). Any image change ships through that one
  Git-reviewed path. Operator RBAC stays least-privilege (it already has
  `pods: get;list;watch`).
- **v1alpha1 is frozen by ADR-0007** — additive, optional status fields are
  backward-compatible, but record the amendment in the ADR (or a successor ADR).
- **Don't widen the serving surface:** the progress endpoint must NOT be added to
  the ClusterIP Service. The operator polls the pod IP directly.
- Progress metadata (byte counts of open-weight models) is not confidential — safe
  to expose cluster-internally without auth.

## Deliverables

### 1. API: `InferenceServiceStatus.weightsDownload` (+ new Progressing reason)

`operator/api/v1alpha1/inferenceservice_types.go`:

```go
// WeightsDownloadStatus reports engine weights-download progress during startup.
// Present only while the workload is not yet Available; nil once serving.
type WeightsDownloadStatus struct {
    // Phase: Pending (guest booting / reporter unreachable), Downloading,
    // Complete (download done, engine warming: quantize + CUDA graphs).
    // +kubebuilder:validation:Enum=Pending;Downloading;Complete
    Phase string `json:"phase"`
    // +optional
    BytesTotal int64 `json:"bytesTotal,omitempty"`
    // +optional
    BytesDone int64 `json:"bytesDone,omitempty"`
    // +optional
    Percent *int32 `json:"percent,omitempty"` // 0–100; nil when total unknown
    // +optional
    StartedAt *metav1.Time `json:"startedAt,omitempty"`
    // +optional
    UpdatedAt *metav1.Time `json:"updatedAt,omitempty"`
}
```

- Add `WeightsDownload *WeightsDownloadStatus` to `InferenceServiceStatus`.
- New reason constant `ReasonDownloadingWeights = "DownloadingWeights"` — used on
  `Progressing=True` while phase==Downloading (replaces the blanket `Deploying`
  during that window; `Deploying` stays for guest boot/guest-pull, and after
  `Complete` while the engine warms).
- `make manifests generate`, refresh `config/crd/bases/` and `dist/chart` CRDs.
- Optional nicety: a `Progress` printcolumn.

### 2. Engine image: wrapper entrypoint with a progress reporter

The image referenced by `BrukTenant.spec.engine.defaultImage` gains a small launcher
(Python, ships inside the image) that:

1. Starts a tiny HTTP server on **`:8001`** (`GET /progress` → the JSON shape above)
   *before* any download starts, so the operator can distinguish "booting" from
   "downloading".
2. Pre-downloads the model with `huggingface_hub.snapshot_download` into the
   standard cache (`/root/.cache/huggingface`) so vLLM finds it and skips its own
   download. Total size from `HfApi.model_info(repo, files_metadata=True)`; done
   bytes from a tqdm subclass or by polling the cache dir size. Honors the existing
   `HF_TOKEN` env (gated models) and the gai.conf IPv4-first override.
3. Marks phase `Complete`, then `exec`s vLLM with the original args (signal handling
   preserved; vLLM stays PID 1's child via exec).

Inputs: the wrapper needs repo/revision. Cleanest is two env vars rendered by the
operator (`BRUK_MODEL_REPO`, `BRUK_MODEL_REVISION`) rather than parsing argv — see
deliverable 3. Note in passing: `engineArgs()` today does not pass
`--revision` to vLLM even when `BrukModel.spec.source.huggingFace.revision` is
pinned — the wrapper should download the revision vLLM will actually load; fixing
the vLLM arg too is a small, separate correctness win.

Ship: build + push the new image, digest-pin, update
`BrukTenant.spec.engine.defaultImage` via Git (the ADR-0008 path).

### 3. Render layer (`operator/internal/render/render.go`)

- New const `progressPort = 8001`; add it to the container's `Ports`.
- Add `BRUK_MODEL_REPO` / `BRUK_MODEL_REVISION` env in `engineEnv()`.
- Do **not** touch the Service (serving surface unchanged).
- Update golden tests. ⚠️ The golden fixtures assert semantic identity with the
  hand-written manifests (`manifests/h100-vllm-cc*.yaml`) — either update those
  manifests in lockstep or consciously relax the invariant; also note the pod-spec
  change will **roll the CC pods on adoption** (initdata-hash-style churn — status
  already explains rolls via `appliedInitDataHash`; a status message is enough).

### 4. Reconciler polling loop (`inferenceservice_controller.go`)

In `updateReadyStatus`, in the not-Available branch:

- List the Deployment's pods (RBAC already grants `pods: get;list;watch`), pick the
  running pod, `GET http://<podIP>:8001/progress` with a ~2s timeout.
  - unreachable / pod ContainerCreating → `weightsDownload.phase=Pending`, keep
    `Progressing=Deploying` ("CC guest boot + guest-pull…").
  - reachable, downloading → write progress, `Progressing=True` with reason
    `DownloadingWeights`, message includes percent.
  - phase Complete but not Available → keep progress (100%), reason `Deploying`
    with an "engine warming (quantize + CUDA graphs)" message.
- Return `ctrl.Result{RequeueAfter: 15 * time.Second}` while not Available — this is
  the behavioral change that makes live progress possible at all.
- When Available: clear `WeightsDownload` (nil) — it's a startup-only signal.
- Rate-limit etcd writes: only `Status().Update` when phase changed or percent moved
  ≥1 point.
- HTTP client injected into the reconciler struct so envtest/unit tests can fake it.

### 5. Tests + CI

- envtest: condition/reason transitions incl. the new `DownloadingWeights` path,
  requeue behavior, write rate-limiting (fake progress server).
- render golden tests: new port + env.
- kind e2e: fake engine image (or stub server) serving `/progress`; assert
  `status.weightsDownload` progresses and clears on Available.
- Keep coverage ≥ the current gate (80%+).

### 6. Console side (tracked in `console/docs/react-remake-design.md` §6)

Once 1–5 land: `FleetAdapter` maps `weightsDownload` → the `/api/v1` endpoint type
(add `downloading_weights` + `progress` to `EndpointStatus`), push over the
notifications SSE stream, and the Phase B stepper gets its real progress bar. Update
design-doc §6.4/§6.6 (the §6.6 "no weights-download progress signal" risk closes).

## Alternative considered — host-side OCI model staging

The glossary's "Model artifact" end-state (signed OCI artifact, cosign-verified,
staged to node NVMe, virtio-fs into the guest) solves this *better*: download happens
once per node outside the TEE (progress trivially host-observable) and **kills the
cold start entirely** (cache survives pod recreates). It is also a much bigger lift
(artifact pipeline, staging controller, Kata virtio-fs plumbing).

Recommendation: land deliverable 1 (the status contract) regardless — it is durable
under either mechanism. Choose wrapper-reporter (2–4, days of work) vs. staging
(weeks) based on roadmap priority; if staging is imminent, skip the wrapper and feed
the same status field from the host-side staging job instead.

## Done means

- `kubectl get bsvc` shows a service progressing `Pending → Downloading (n%) →
  Complete → Ready`, with `status.weightsDownload` populated live and cleared once
  Available, on the kind e2e and on the reference cluster after operator adoption.
- No new ports on the ClusterIP Service; RBAC diff is empty; ADR amendment merged.
