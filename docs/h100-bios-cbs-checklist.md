# BIOS / CBS pass — new H100 box (`anton-bruk`)

Firmware-only state — **nothing on disk restores it** (task #2). This is the recipe to bring the new
box to the same SEV-SNP end-state the old box (`secure-puppy`) reached, plus the fTPM that never took.

## Box

| | |
|---|---|
| Server | **ASUS RS720A-E12-RS12** (baseboard **K14PP-D24**) |
| BIOS | **AMI** (Aptio), **v1403**, 2024-05-06 |
| CPU | dual-socket **2× AMD EPYC 9224** (Genoa) |
| Remote access | **BMC present** (`/dev/ipmi0`) → use the **ASUS ASMB iKVM HTML5 remote console** to enter BIOS; no physical access needed. (Need the BMC IP + creds — separate from the host IP.) |

## Starting state (confirmed 2026-06-30)

- ✅ **IOMMU already enabled** (78 groups) — **leave it on**, don't toggle.
- ✅ `/proc/cmdline` clean — **no `iommu=pt`** (SNP cannot init in IOMMU passthrough mode — keep it absent).
- ❌ **SMEE OFF** → `ccp: SEV: memory encryption not enabled by BIOS` (this is the master gate).
- ❌ **SEV-SNP / SEV-ES** not enabled (no `sev_snp` cpu flag).
- ❌ **fTPM absent** (`NO /dev/tpm*`).

---

## What to change (AMD CBS — option names are AMD-standard; AMI shows them verbatim)

Enter setup via the iKVM console → **Advanced → AMD CBS**.

### 1. SMEE — the master switch (currently OFF, must be ON)
- **AMD CBS → CPU Common Options → SMEE → `Enabled`**
  Without this, SEV / SEV-ES / SEV-SNP stay off no matter what else is set. This is the single setting
  the *"memory encryption not enabled by BIOS"* message is pointing at.

### 2. SEV-SNP + the ASID split
- **AMD CBS → CPU Common Options →**
  - **SEV-ES ASID Space Limit Control → `Manual`**
  - **SEV-ES ASID Space Limit → `100`**
    (This is the split boundary. `100` reproduces the old box exactly: SEV-SNP/SEV-ES on **ASIDs 1–99**,
    plain SEV on **ASIDs 100–1006**.)
  - If a master **`SEV Control`** toggle exists, set it **`Enabled`**.
- ⚠️ **On this ASUS board `SEV-SNP Support` lives under `NBIO Common Options`** (not CPU Common Options) — confirmed 2026-06-30. Set **SEV-SNP Support → `Enabled`** there.
- ⚠️ **REQUIRED — `SNP Memory (RMP Table) Coverage → Enabled`** (also under NBIO Common Options, near SEV-SNP Support; on some boards under CPU Common Options). **`SEV-SNP Support` alone is NOT enough** — this is the option that makes the BIOS reserve the RMP table. Confirmed on this box 2026-06-30: with SEV-SNP Support enabled but RMP Coverage off, the kernel logs *"SEV-SNP: Memory for the RMP table has not been reserved by BIOS"*, `sev_snp=N`, and SNP never comes up (SEV + SEV-ES work fine). Per AMD docs, the RMP must cover all of host memory. This is **not** optional here — the old box happened to auto-reserve; this one does not.

### 3. Keep IOMMU on
- **AMD CBS → NBIO Common Options → IOMMU → `Enabled`** (verify it stays enabled — it already is).

### 4. fTPM — enable (it was absent on both boxes; old box's fTPM once "did not take")
- **Advanced → Trusted Computing → Security Device Support → `Enable`**
- **TPM Device Selection / AMD fTPM switch → `Firmware TPM` (AMD CPU fTPM)** (not Discrete TPM).
- ⚠️ If `/dev/tpm0` is still missing after reboot, re-enter and confirm the device-selection field
  actually committed (this is the exact failure mode the old box hit).

### Do NOT
- Do **not** add `iommu=pt` to GRUB. Do **not** disable IOMMU. Both break SNP init.

---

## Save, reboot, verify

Save & Exit → let it reboot. Then over SSH (`ssh ubuntu@<build-box>`):

```bash
sudo dmesg | grep -iE 'SEV|ccp.*SEV|AMD-Vi: IOMMU SNP'
```
**Target end-state (matches old box's as-built):**
```
kvm_amd: SEV enabled (ASIDs 100 - 1006)
kvm_amd: SEV-ES enabled (ASIDs 1 - 99)
kvm_amd: SEV-SNP enabled (ASIDs 1 - 99)
AMD-Vi: IOMMU SNP support enabled.
ccp 0000:07:00.5: SEV-SNP API:1.x build:x
```
Then:
```bash
cat /sys/module/kvm_amd/parameters/sev_snp   # expect: Y
grep -m1 -o sev_snp /proc/cpuinfo             # expect: sev_snp
ls -l /dev/tpm*                               # expect: /dev/tpm0  (fTPM took)
```

Notes:
- **Dual-socket:** CBS settings are global (apply to both sockets). Both PSPs (`07:00.5`, `82:00.5`)
  should be happy; SEV-SNP is a single global capability.
- This pass unblocks **confidential serving (#7)** and (via fTPM) the later **host-attestation** work.
  It is *not* on the critical path to the plain non-CC serving baseline (#6).
