# Phase 3.3 — Fleet plane + bare-metal: design and work plan

Output of the 2026-07-07 grilling session. Refines the 3.3 paragraph of `docs/plan-2026-07.md`
into a full design: every decision below was made against ADR-0001 (fleet of single-tenant
clusters), ADR-0006 (Pattern A is roadmap), ADR-0009 (registry is a Git repo), and the
`CONTEXT.md` glossary. Terminology note: the machines that run fleet-plane services are
**fleet-plane hosts**, never "fleet-plane nodes" — a **Node** is always a cluster-member GPU
machine.

## Scope: M5 is two milestones

- **M5a — Fleet provisioning.** BIOS by hand, then unattended: MAAS images the Node, the host
  layer lands, the cluster bootstraps. Exit: "only BIOS is manual per machine."
- **M5b — Attestation-gated onboarding.** A provisioned Node receives its secrets (Flux deploy
  key, `hf-token`) **only** through the attestation gate — TPM-backed host attestation against
  the registry. Exit: a node that fails attestation converges to nothing; a node that passes
  converges to a serving cluster, with no human carrying secrets.
- **Out of scope: KBS/Trustee (Pattern A).** Different animal — its consumer is the CC *guest*
  (in-guest CDH requesting `kbs://` keys), its evidence is guest attestation, and ADR-0006
  phrases the fleet-plane verifier as Pattern A's *precondition*, not its content. 3.3 only
  builds the landing pad (the trust machine is its future home; the registry already has a place
  for per-cluster keys). Pattern A becomes **Phase 3.4**, starting once M5b is real.

## Topology (decided 2026-07-07)

The datacenter is Airon-owned. The fleet plane spans two fleet-plane hosts:

- **The MAAS machine** — already exists, already imaged the box once (the PXE-leftover note in
  `host-setup.sh` is the fossil). **Nothing gets installed on it**; we use MAAS strictly through
  its API/UI: deploy with our user-data, BMC power control, VM-host registration.
- **The trust machine** — new, to be procured. Carries everything that decides or holds trust:
  - registry checkout + the **age private key** (decrypts every cluster's secrets bundle),
  - Keylime verifier + registrar (pending spike),
  - later the Trustee/KBS stack (3.4),
  - the **lab VMs** (libvirt/LXD + swtpm), registered in MAAS as a VM host over the network.

  Spec: 8 cores with AMD-V, 32–64 GB RAM, 500 GB SSD (mirrored if cheap), 1 GbE, **TPM 2.0
  required** — not because 3.3 needs it, but for TPM-bound LUKS full-disk encryption: this
  machine is where the chain of trust bottoms out, and by 3.4 it holds weights-decryption keys.
  No GPU. Needs TCP reach to Nodes (Keylime agent port); does **not** need provisioning-L2
  adjacency or BMC access — those are MAAS-machine properties.

Deliberate separation: the imager (MAAS) holds no secrets; the trust machine holds keys but
never touches an OS install. A compromised imager can't read cluster secrets. Keep this
separation when adding sites.

## Provisioning mechanism (M5a)

**Stock Ubuntu 24.04 + cloud-init calling the committed scripts. No golden image.**

1. MAAS deploys 24.04 with the **HWE kernel selected in MAAS** (step 1 of `host-setup.sh`
   evaporates).
2. Cloud-init user-data clones the repo at a **pinned ref** and runs `install/host-setup.sh` —
   cloud-init *calls* the script, never re-implements it, or the host layer forks into two
   diverging copies and the repo-is-the-delivery-artifact invariant breaks.
3. Reboot; a systemd oneshot runs `install/bootstrap.sh` on the next boot (its SEV-SNP preflight
   already gates it). Under M5b, its Flux step first blocks on the attestation-gated secrets
   delivery.

Golden images (packer-maas) are deferred until provisioning speed or drift actually hurts —
a multi-node problem; 3.3 stays single-site per `docs/plan-2026-07.md`.

**Boot policy: disk-first + BMC-forced PXE.** Nodes keep disk-first boot order (applied at
deploy time via cloud-init — promotes `host-setup.sh` step 5 from box-specific note to fleet
policy); MAAS sets next-boot=PXE via the BMC only when it actually wants the machine. Deliberate
deviation from MAAS's PXE-first default: this firmware *hangs* on a dead PXE entry, and Node
reboots must never depend on a fleet-plane host being up. The spike must verify the BMC
next-boot override works on this board — it is the one hard external unknown.

## Registry (M5b input) — ADR-0009

Private Git repo; one file per Tenant/Cluster/Node; secrets sops/age-encrypted; enrollment is a
PR (a Node's EK certificate + reference values get committed). The age private key lives only on
the trust machine. The client-facing token issuer reads the same Tenant records but is out of M5
scope entirely.

## Host-attestation verifier (M5b) — Keylime, pending spike

**Keylime over Trustee-AS or hand-rolling**: it is exactly this shape (registrar + verifier +
per-Node agent, EK/AK enrollment, continuous re-attestation, revocation), and its
**attestation-gated encrypted-payload delivery is the gate mechanism itself** — the cluster
secrets bundle from the registry *is* the payload. Trustee's verifiers are TEE-centric (guest
evidence, not host boot chains); hand-rolling means reimplementing enrollment/nonce/quote
protocols. Decision confirmed by a **1–2 day timeboxed spike** (Phase-2 pattern) against the
box's discrete TPM once installed; fall back to a minimal Go verifier only if Keylime fights us.
If the spike passes, record the choice as an ADR.

Gate mechanics: provisioned Node boots → agent enrolls/attests → verifier checks quote against
the registry's reference values → releases the sops-decrypted bundle → `bootstrap.sh`'s Flux
step proceeds. No attestation, no deploy key, no cluster. This makes the `CONTEXT.md`
**attestation gate** mechanically true rather than procedurally hoped.

Spike design notes: start reference values with the stable PCRs (firmware/Secure Boot state,
0–7); treat kernel/bootloader PCRs carefully — they churn on every kernel update, and a
reference-value story that breaks on `apt upgrade` trains you to ignore attestation failures.
Check whether Secure Boot is even enabled on the box (do it during the TPM BIOS visit).

## Test strategy

**Develop in a VM lab; accept with one sacrificial wipe.**

- **VM lab**: 2–3 throwaway KVM guests on the trust machine, driven by the *real* MAAS (same
  version, templates, network) — deleted when done. Rationale: provisioning development is
  dozens of failed attempts; a failed attempt in a VM costs ~4 minutes, on the box it costs the
  live cluster plus ~an hour (25–30 min cold boots). `bootstrap.sh` has never run end-to-end as
  a script — those first failures burn VM time, not downtime windows. swtpm vTPMs make the full
  M5b gate loop (enroll → attest → release → converge, plus the fail-closed paths) rehearsable
  ~20×/day. The lab cannot test BIOS/CBS, real EK/PCRs, VFIO/GPU, SEV-SNP, or BMC power control
  — exactly the short list the wipe covers.
- **The wipe**: one scheduled, low-stakes window; **full-chain M5a+M5b acceptance** — BIOS by
  hand → MAAS images → host layer → attest → secrets released → Flux converges → the 24B serves
  confidentially. Only disks are touched: the CBS/SEV-SNP settings and the TPM (EK is burned in;
  AKs regenerate at enrollment) survive re-imaging — that is what makes "only BIOS is manual"
  coherent. Costs ~an afternoon + the 90 GB weights re-download when it passes. Do not schedule
  it until the VM rehearsal converges unattended. It is also the first real test of the standing
  invariant ("a fresh clone + the runbook reproduces the cluster") — currently a claim, not a
  fact.

## Work order

1. **TPM install + box re-verify** (maintenance window; cold power-cycle). While in the BIOS:
   enable the TPM, note Secure Boot state, confirm CBS SEV-SNP settings intact.
2. **Keylime spike** (needs 1; non-destructive — the agent attests the *live* box without gating
   anything yet). Exit: go/no-go + PCR-stability check across a reboot + BMC next-boot-PXE
   verified.
3. **Trust machine procurement + setup** (parallel): TPM-bound LUKS, libvirt/LXD + swtpm,
   registered in MAAS as a VM host.
4. **VM lab / M5a development** (needs 3): cloud-init templates, disk-first policy, first-ever
   end-to-end `bootstrap.sh` (everything converges except the CC workload — no GPU in a VM).
5. **Registry repo** (anytime, small): ADR-0009 schema, sops/age, enroll the box + lab VMs.
6. **M5b integration** (needs 2+4+5): bundle as Keylime payload; Flux step blocks on it; exercise
   the fail-closed path.
7. **The wipe** (needs all; scheduled window): full-chain acceptance.
8. **Docs pass**: `RUNBOOK-fleet-provisioning.md`, Keylime ADR, status update, handoff.

## Open items (external)

- **BMC reachability from MAAS to the H100 box** — hard dependency of the boot policy (ask DC
  tech; also which MAAS power driver is configured).
- **MAAS version** — shapes user-data templates and VM-host registration.
- **Trust machine hardware** — procure per spec above.

## Revisit triggers

- Node enrollment-as-PR becomes a chore → service in front of the registry (ADR-0009).
- Provisioning speed/drift hurts → golden image (packer-maas).
- Multi-site → verifier reference-value provenance independent of any one MAAS; fleet-plane
  hosts attesting each other; keep imager/key-store separation.
- Next machines arrive (fabric, summer 2026) → the box stops being precious; lab-vs-iron
  calculus flips; M5b done → **start Phase 3.4 (Pattern A / KBS/Trustee)** per ADR-0006.
