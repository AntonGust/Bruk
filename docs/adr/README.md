# Architecture Decision Records (ADRs)

Each ADR is an **immutable record** of a decision at a point in time. We don't delete or rewrite them —
when a decision changes, the old ADR is marked **superseded** and points to its replacement. So a
`superseded` ADR is history, **not current guidance** — check the status before acting on one.

| # | Decision | Status | Notes |
|---|---|---|---|
| [0001](0001-fleet-of-single-tenant-clusters.md) | Fleet of single-tenant clusters (one cluster per tenant) + a fleet plane | ✅ accepted | See also `docs/deployment-model.md` |
| [0002](0002-airon-operator-is-deterministic.md) | Airon Operator is a deterministic K8s operator, not agentic-AI | ✅ accepted | |
| [0003](0003-sm120-attention-backend.md) | `FLASH_ATTN` attention backend on **sm_120** (RTX PRO 6000) | ⚠️ **superseded by 0005** | RTX PRO 6000 / sm_120 era only. Its "FA3 impossible" ceiling is an sm_120 fact — **does NOT apply to the H100 pilot** (sm_90, where FA3 is native). Historical record. |
| [0004](0004-confidential-serving-snp-spt-is-next-phase.md) | Confidential serving (SEV-SNP + single-GPU CC) is the next phase | ✅ accepted | Decision stands; **hardware context amended by 0005**, weights/image delivery worked out in **0006** |
| [0005](0005-h100-pilot-hardware.md) | H100 pilot hardware (2× H100 on EPYC 9224 Genoa) | ✅ accepted | Supersedes 0003 for H100; amends 0004's hardware context |
| [0006](0006-confidential-weights-delivery-storage.md) | Confidential model delivery: attested storage on a block device; Bruk targets Pattern B; KBS is roadmap | ✅ accepted | Part 1 (registry mirror) done; Part 2 (block storage for 24B) pending |

**Current pilot hardware = H100 (sm_90) on AMD EPYC 9224 Genoa** (ADR-0005). If an ADR talks about
RTX PRO 6000 / sm_120 as if it were current, it's the pre-migration record — 0005 is the authority.
