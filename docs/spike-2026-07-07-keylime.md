# Spike: Keylime vs the real TPM (Phase 3.3, work-order step 2)

**Date:** 2026-07-07 · **Timebox:** 1 day (used ~½) · **Verdict: GO** (one data point pending, see below)

Validates the M5b attestation-gate design from `docs/plan-3.3-fleet-plane.md`: Keylime
(registrar + verifier + rust agent) against the box's discrete Nuvoton NPCT75x TPM 2.0,
everything co-located on the box and bound to `127.0.0.1` (spike only — production splits
verifier/registrar onto the trust machine).

## The four spike questions

| # | Question | Verdict |
|---|---|---|
| 1 | EK enrollment works with our TPM? | **YES** — after adding Nuvoton's ECC384 chain to the cert store (see finding 1). Registration **and** credential activation (MakeCredential/ActivateCredential) succeed against the real EK. |
| 2 | PCR values stable across reboots? | **PENDING** — reference values captured (`~/keylime-spike/tpm_policy.json` on the box); `~/keylime-spike/rerun-after-reboot.sh` re-tests at the next natural reboot. Do not force a reboot for this (25–30 min POST). |
| 3 | Which PCRs can we pin? | **sha256 PCRs 0–7** (mask 0xff) enrolled and passing. Secure Boot is disabled; if it's ever enabled, PCR 7 changes → re-take reference values. Kernel/bootloader PCRs (8+) deliberately not pinned (churn on updates). |
| 4 | Payload mechanism fits the gate? | **YES** — payload released to the agent's root-only tmpfs (`/var/lib/keylime/secure/unzipped/decrypted_payload`) only after attestation passed. Continuous attestation at 2 s period, `attestation_status: PASS`. |

**Fail-closed proven:** extending PCR 4 with garbage (`tpm2_pcrextend`) flipped the agent to
`Invalid Quote` / `attestation_status: FAIL` (severity 6) within one attestation period. A
tampered host gets nothing.

## Findings (each will bite the production deployment if forgotten)

1. **Keylime's bundled TPM cert store does not cover modern Nuvoton chips.** Its `NTC1/NTC2`
   roots are the older RSA-era CAs. Our EK is issued by `NPCTxxx ECC384 LeafCA 022111` under
   `NPCTxxx ECC521 RootCA`; neither is bundled, and the chain is not in TPM NVRAM (the agent
   logs a benign NV-read warning). Without them: `No Root CA matched EK Certificate` → quote
   rejected. **Fix:** follow the EK cert's AIA URL (nuvoton.com) → fetch leaf + root → PEM →
   drop into `tpm_cert_store`. `openssl verify` confirms the chain. The registry's Node
   enrollment procedure (ADR-0009) must include vendor-chain provisioning.
2. **EK cert NV read needs a DER trim.** `tpm2_nvread 0x1c00002` returns the cert padded to the
   NV area size; openssl chokes. Trim to the ASN.1 TLV length first.
3. **The `keylime` PyPI wheel is incomplete.** It declares no dependencies (install
   `requirements.txt` from the source repo), ships no config templates and no
   `tpm_cert_store` (both live in the git repo), and needs the distro `python3-gpg` (gpgme
   bindings, not pip-installable) → venv must be `--system-site-packages`.
4. **rust-keylime (0.2.9) needs rustc ≥ 1.88 + clang.** Noble's default 1.75 fails (lockfile
   v4, dep MSRVs); `rustc-1.89`/`cargo-1.89` from noble-updates work. bindgen needs `clang`
   for the TSS2 headers. Build itself: seconds on the box.
5. **Startup order matters:** the registrar's TLS dir is the verifier-generated
   `/var/lib/keylime/cv_ca` → start the verifier once (or first) before the registrar, or the
   registrar exits.
6. **Defaults are sane:** verifier/registrar/tenant configs bind 127.0.0.1 out of the box;
   `require_ek_cert = True` by default. Spike-only deviations: `run_as = ""` (root; production
   creates a `keylime` user in `tss`), revocation notifications off (no ZMQ broker).

## What this proves for M5b

The gate mechanics are real on our hardware: **enroll → attest (PCR 0–7, 2 s cadence) →
payload release only on PASS → fail-closed on tamper**. The cluster secrets bundle from the
registry can ride exactly this payload path. Remaining before the design is fully de-risked:

- **PCR stability across a reboot** (finding above; script staged on the box).
- **BMC next-boot-PXE override** (boot-policy dependency; waiting on DC tech).
- Production shape: verifier/registrar on the trust machine, real mTLS across the network,
  per-Node UUIDs, policies fed from the registry, `keylime` service user, ADR for the Keylime
  choice once the reboot data point lands.

## Spike artifacts (on the box, `~/keylime-spike/`)

`venv/` (keylime 7.14.3 server), `rust-keylime/target/release/keylime_agent` (0.2.9),
`tpm_policy.json` (reference PCRs 0–7, 2026-07-07), `payload.txt`, `rerun-after-reboot.sh`,
`agent-build.log`. Configs in `/etc/keylime/`, CA + cert store + agent state in
`/var/lib/keylime/`. All transient systemd units stopped; nothing auto-starts on boot.
