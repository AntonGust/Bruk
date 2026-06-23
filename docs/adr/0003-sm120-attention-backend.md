# FLASH_ATTN is the chosen vLLM attention backend on sm_120, not a stopgap

Bruk's GPUs are **RTX PRO 6000 Blackwell = compute capability sm_120** (workstation Blackwell),
confirmed by Checkpoint 3's in-guest `nvidia-smi`. This is distinct from — and harder to support than
— the **sm_100** datacenter Blackwell (B200/B300). An earlier analysis assumed the box was sm_100 and
concluded the kernel situation was "better"; that was wrong, and the FlashInfer question has to be
answered for sm_120 specifically.

We decided to keep **`VLLM_ATTENTION_BACKEND=FLASH_ATTN`** (see `manifests/day4-vllm.yaml`) as the
**deliberate** attention backend for dense FP8 serving on sm_120 — not a temporary fallback. On
sm_120, FLASH_ATTN runs FlashAttention-2 (via Triton) and **retains full CUDA-graph capture and paged
attention**. We verified this on the box (2026-06-23): the Checkpoint-4 pod logs show
`enforce_eager=False`, `cudagraph_mode=FULL_AND_PIECEWISE`, and "Capturing CUDA graphs … 100%" for
both PIECEWISE (mixed prefill-decode) and FULL (decode) — "Graph capturing finished in 42 secs". The
widely-repeated claim that "without FlashInfer you lose CUDA graphs and paged attention" applies only
to the **pure-SDPA eager fallback**, which we are not in. The measured ~33.8 tok/s single-stream for
Mistral-Small-3.1-24B FP8 is in the reasonable band for a 24B dense FP8 model on this hardware.

## Rejected alternatives

- **FlashInfer (forced via `FLASHINFER_*` env vars + custom image).** FlashInfer's sm_120 value is in
  NVFP4 / MoE-GEMM kernels, **not dense attention**. For attention on sm_120 it is *more* limited
  (decode-only graph support, `head_size` failures on some models), released wheels ship **zero sm_120
  cubins** (FlashInfer #3294), and JIT needs the full system CUDA-13 toolkit baked into the image plus
  nightly vLLM/FlashInfer/CUTLASS version-locking that field reports say rots in ~30–60 days. No win
  for our dense FP8 model; likely a regression. (vLLM #40677, #2555.)
- **TensorRT-LLM.** Not viable on sm_120: no trtllm-gen FMHA cubins for sm_120/sm_121 and no JIT
  fallback — it fails rather than degrades. (TensorRT-LLM #11799.)
- **NGC image `nvcr.io/nvidia/vllm:25.12`.** Ships the *same* vLLM 0.11.1 + an old FlashInfer
  (flash_attn only on sm_120) and requires NGC auth (`imagePullSecret`) — friction against Bruk's
  "signed, mirrored, no runtime public-registry pulls" model, with no benefit here. (vLLM #31424.)
- **SGLang.** Works on sm_120 but is newer / less hardened, sometimes needs `--disable-cuda-graph`,
  and its real edge (shared-prefix/RAG, native NVFP4 MoE) does not apply to a single dense model
  today. Kept as a future watch item, not adopted.

## Hard ceiling (accept it)

FlashAttention-3 / FA4 are **architecturally impossible on sm_120** — they depend on the TMEM
subsystem that workstation Blackwell lacks (Dao-AILab/flash-attention #1987). No image build unlocks
datacenter-class attention on this hardware; FA2-equivalent is the ceiling.

## Revisit triggers

Re-evaluate this decision (FlashInfer / SGLang may become worthwhile) when **any** of:
- Bruk adds an **NVFP4-quantized** model to the model library;
- Bruk adds an **MoE** model (CUTLASS MoE has no sm_120 path; backend choice starts to matter);
- the cluster moves to **sm_100 datacenter GPUs** (the summer-2026 Spectrum-4 / BlueField-3 testbed may
  change hardware — see the multi-node testbed timeline).

## Consequences

- `FLASH_ATTN` stays pinned in the serving manifest with a comment pointing here; it is documented as
  the chosen backend, not a TODO.
- No custom vLLM image, no FlashInfer build, no TensorRT-LLM, no SGLang migration is undertaken now.
- A `FLASH_ATTN` vs `TRITON_ATTN` throughput A/B was **deferred, not run**: the model weights sit on
  ephemeral pod storage, so swapping the backend forces a ~90 GB re-download + reload for a result we
  expect to confirm the choice. It becomes a cheap experiment once weights are on a PVC (already a
  deferred item) and should be recorded here when run.
- Version-specific upstream claims were deliberately avoided; this ADR cites issue numbers and
  observed behaviour, not exact vLLM/FlashInfer point releases (sources disagreed on those).
