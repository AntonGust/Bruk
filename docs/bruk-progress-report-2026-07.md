# Bruk — Progress Report

**Period:** June – early July 2026 · **Status: pilot objectives achieved ahead of plan**

---

## The one-paragraph summary

Bruk is our platform for **confidential AI inference**: customers run large language models on
our infrastructure while their models, prompts, and outputs stay cryptographically protected —
**even from us, the operator**. As of this week, that claim is no longer a design goal. A real
production-size model (Mistral 24B) is serving on our H100 hardware inside a fully confidential
environment, at **~2 % performance cost**, with the confidentiality **mathematically proven**
against AMD's and NVIDIA's hardware root-of-trust — and the entire stack rebuilds itself
automatically from our Git repository.

## Why this matters

Enterprises with sensitive data (health, legal, finance, defence) want LLMs but cannot hand
their data or fine-tuned models to a cloud operator on trust. The emerging answer is
*confidential computing*: the CPU and GPU encrypt everything in memory and can produce signed
hardware evidence of exactly what is running. A small set of players (Edgeless Systems, Tinfoil,
Azure's confidential GPU offering) ship this today — their existence validates the market, and
**Bruk now demonstrably matches the same technical pattern on our own hardware**, as the
foundation for a sovereign, self-hosted alternative we control end to end.

## What is proven and working today

| Milestone | Result |
|---|---|
| **Confidential serving works** | vLLM serving inside an encrypted VM (AMD SEV-SNP) with an NVIDIA H100 in confidential mode |
| **It's provable, not just claimed** | Attestation verified end-to-end: CPU report validates against AMD's key servers; GPU firmware/driver measurements validate against NVIDIA's — re-runnable test suite committed to the repo |
| **Real model, real speed** | Mistral-Small 24B (FP8): **97.6 tokens/s vs ~100 non-confidential — ~2 % overhead** (industry references run 12–15 %) |
| **The hard storage problem is solved** | 90 GB of model weights + a 35 GB serving image live on encrypted NVMe volumes with keys that exist only inside the secure VM — the host only ever sees ciphertext. There is **no turnkey reference for this anywhere**; we assembled it from upstream building blocks and found (and worked around) a genuine upstream bug on the way |
| **It runs itself** | The whole per-cluster stack is now managed by GitOps (Flux): the cluster continuously syncs from Git, heals itself if anything is deleted or drifts, and every change is a reviewed, auditable commit |
| **It's reproducible** | Standing up a new machine = BIOS settings + one bootstrap script + point it at the repo. Everything else converges automatically. Full runbooks, architecture decision records, and a teaching curriculum are committed |

**In plain terms:** three of the five pilot milestones (prove it, scale it to the real model,
make it self-managing) were completed — the last three in a single focused sprint this week.

## What it took (highlights)

- Brought up bleeding-edge hardware security features (AMD SEV-SNP on the new dual-EPYC H100
  box) including BIOS-level pitfalls that are documented nowhere public — now captured in our
  checklists.
- Worked at the frontier of the Confidential Containers open-source stack — in several cases
  reading pinned upstream source to find undocumented configuration mechanisms, and isolating
  one reproducible upstream bug (a fix candidate we can contribute back).
- Every result is captured as re-runnable scripts/manifests, not one-off terminal work — the
  verification suite doubles as our regression gate and student material.

## What is *not* done yet (honest view)

- **Customer-facing layer** — today, "deploying a model" means our engineer edits the Git repo.
  The self-service surface (customer portal → declarative model requests → automatic rollout)
  is the next phase: the *Airon Operator* (design finished, implementation starting) turns a
  short customer intent file into everything we currently do by hand.
- **Fleet automation** — provisioning many customer machines automatically (bare-metal imaging,
  per-tenant isolation of configuration) is designed but not built; partially gated on a TPM
  module (on order) for host-level attestation.
- **Very large models** — one H100 caps out around 70B-parameter models. Multi-GPU confidential
  mode requires the HGX B300 platform arriving **October 2026**; the storage and attestation
  work done now carries over directly.
- Minor: model weights currently re-download (~30 min) if a serving instance is fully recreated
  — an accepted pilot trade-off with a designed upgrade path.

## Next steps

1. **Airon Operator (M4):** the Kubernetes operator that converts customer requests
   (`"serve model X, confidential, this context size"`) into running confidential workloads —
   this is where the platform becomes a *product*. Conventional software engineering phase:
   tests, CI, code review.
2. **Fleet plane (M5):** automated bare-metal provisioning + per-tenant Git delivery, so
   "customer rents a server" → "Bruk cluster appears" without hands on keyboards.
3. **October 2026, HGX B300:** multi-GPU confidential serving → frontier-scale models.

## Bottom line

The pilot set out to answer: *can we serve real LLMs confidentially, prove it, and operate it
repeatably on our own hardware?* The answer, demonstrated and documented, is **yes — at a ~2 %
performance cost**, which is materially better than published industry figures. The risk has
moved from "is the technology viable?" to ordinary product-engineering execution: building the
customer-facing automation on top of a foundation that already works.
