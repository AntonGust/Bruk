# BIOS settings to set — ASUS RS720A-E12-RS12 (dual AMD EPYC 9224)

> Hand-off sheet for the datacenter technician. Plain BIOS steps only — no OS/Linux work required.
> (The detailed engineering rationale + post-reboot verification lives in `h100-bios-cbs-checklist.md`.)

Hi — please set the following BIOS options on this server. It has an AMI BIOS; all the main settings
live under the **AMD CBS** menu.

**Machine:** ASUS RS720A-E12-RS12, 2× AMD EPYC 9224, 2× NVIDIA H100.
_(Rack / asset tag / serial: ________________ — please confirm you're on the right box.)_

**Enter setup** (DEL or F2 at boot) → go to the **Advanced** menu → **AMD CBS**.

## 1. Enable memory encryption (do this first — the others depend on it)
- `AMD CBS` → `CPU Common Options` → **SMEE** → set to **Enabled**

## 2. Enable SEV-SNP and set the ASID split
- `AMD CBS` → `CPU Common Options` → **SEV-ES ASID Space Limit Control** → **Manual**
- then **SEV-ES ASID Space Limit** → **100**
  _(this field only appears after the line above is set to Manual)_
- `AMD CBS` → `NBIO Common Options` → **SEV-SNP Support** → **Enabled**
  _(on this board it's under NBIO, not CPU Common Options — you already found/set this)_
- `AMD CBS` → `NBIO Common Options` → **SNP Memory (RMP Table) Coverage** → **Enabled**
  _(**this is the one still missing.** SEV-SNP Support alone doesn't reserve the "RMP table"; this option does. May be right next to SEV-SNP Support, or under CPU Common Options.)_

## 3. Leave IOMMU ON (confirm only — do not turn it off)
- `AMD CBS` → `NBIO Common Options` → **IOMMU** → should be **Enabled** (leave as-is)

## 4. Firmware TPM — SKIP for now (a TPM module is on order)
- This board shows **"No security device found"** (no firmware TPM, no module installed yet). A discrete
  TPM module is being ordered and will be fitted later. **For this pass, please don't spend time on the
  TPM / Trusted Computing settings** — leave them as they are. Not needed for what we're doing today.

## 5. Save and reboot
- Press **F10** (Save & Exit) and let it reboot normally.

## Please do NOT change anything else
- No boot-order changes, do not disable IOMMU, do not touch any other CBS values.

## Before you save, please:
- Confirm on screen that **SMEE = Enabled** and **SEV-SNP Support = Enabled**.
- Take a **photo of each screen** showing the final values (SMEE, the two SEV-ES ASID lines,
  SEV-SNP Support, IOMMU, and the TPM selection).
- If any of these options is **missing or greyed out**, please note which one and don't force
  anything — just let me know.

Thanks!
