# Bruk Node Preflight Hardware & Hardening Check

## Context

Bruk (Airon's open-source sovereign AI/ML platform) runs on **bare-metal, single-tenant
GPU nodes** that must be hardened "bottom-up" — from physical/firmware through OS, network,
and confidential compute — before they are allowed to join the cluster and receive secrets
(per the PDF's *Hardening, bottom-up* table and the Phase 3 *remote attestation* rule:
"before a node gets any secrets it must pass verification").

Right now the project is just documentation (`bruk-compendium.md`, the spec PDF) — there is
**no code yet**. The user wants the first executable artifact: a script that runs the
**initial hardware check** on a freshly provisioned node and reports whether it meets the
Bruk hardening posture (Ubuntu hardening, network config, confidential compute, GPU/firmware,
etc.).

**Target environment (confirmed):** the script runs **on the real bare-metal factory node**
— the GPUs are installed and the Kata/KVM VMs are up and running. So the hardware *is*
expected to be present. Absent firmware/GPU/IOMMU is therefore a genuine **FAIL** on this
machine (the node is not ready), not a graceful skip. `SKIP` is reserved only for genuinely
optional/unverifiable-from-host items (e.g. a query tool not installed, or BMC VLAN isolation
that can't be seen from inside the host).

Decisions confirmed with the user:
- **Read-only** — inspects and reports `PASS/WARN/FAIL`; never mutates the node. Safe to run
  repeatedly as a node-readiness gate.
- **Bash** with standard tools only (the hardened node image has a minimal package set / no
  compilers, so bash + coreutils is the only safe assumption).
- **Two output modes**: colored human output (default) and `--json` for the Airon Operator /
  attestation pipeline to consume.
- **Full bottom-up coverage**: Firmware → Boot chain → Node OS → Network → GPU/Fabric →
  Confidential compute.

## Approach

A small orchestrator script that sources one module per hardening layer. Each module emits
check results through shared helper functions that accumulate both the human report and a
JSON array, keeping checks declarative and files small (per coding-style: many small files,
each <400 lines).

### File layout (new, in `/home/anton/Documents/work/Bruk/preflight/`)

```
preflight/
  bruk-preflight.sh          # entrypoint: arg parsing, sources lib/, runs layers, prints summary
  lib/
    common.sh                # helpers: report_pass/warn/fail/skip, color, JSON accumulation,
                             #   has_cmd, read_kernel_cmdline, exit-code policy, root/sudo note
    check_firmware.sh        # Layer: Firmware
    check_boot.sh            # Layer: Boot chain
    check_os.sh              # Layer: Node OS (Ubuntu hardening)
    check_network.sh         # Layer: Network
    check_gpu.sh             # Layer: GPU / Fabric
    check_confidential.sh    # Layer: Confidential compute
  README.md                  # what it checks, how to run, exit codes, sample output
```

### Shared mechanics (`lib/common.sh`)

- `report <id> <pass|warn|fail|skip> <message>` — single sink. Appends a colored line to
  human output and pushes `{"id","layer","status","detail"}` to a JSON buffer. Increments
  per-status counters. (Immutable-style: builds output by appending, never rewrites prior
  results.)
- `begin_layer <name>` — sets current layer label used in both outputs.
- `has_cmd <name>` — `command -v` guard. A missing **query tool** (e.g. `tpm2-tools` not
  installed) yields `SKIP` with a note; but missing **hardware/state** the node is supposed
  to have (no `/dev/tpm0`, no `nvidia-smi`/GPU, IOMMU off) is a real `FAIL` — this is the
  live factory node, the hardware must be there.
- Reads that need root (e.g. `tpm2_pcrread`, some `dmidecode`) note "re-run with sudo for
  full check"; the script is intended to be run with sufficient privilege on the node.
- Constants (no magic values): expected GPU vendor id, required kernel cmdline tokens
  (`intel_iommu=on` / `amd_iommu=on`, `iommu=pt`), CC-relevant paths.

### Checks per layer (mapped to the PDF "Hardening, bottom-up" + Infrastructure stack)

**Firmware** (`check_firmware.sh`)
- TPM 2.0 present: `/dev/tpm0` / `/dev/tpmrm0`, `/sys/class/tpm/tpm0/tpm_version_major == 2`.
- Secure Boot enabled: `mokutil --sb-state` or `bootctl status`, fallback to the EFI var
  `SecureBoot-*` under `/sys/firmware/efi/efivars`.
- Booted in UEFI mode: presence of `/sys/firmware/efi`.
- IOMMU enabled: kernel cmdline has `intel_iommu=on`/`amd_iommu=on` **and**
  `/sys/class/iommu/` is non-empty (DMAR/IVRS groups present).
- Virtualization extensions present: `vmx`/`svm` in `/proc/cpuinfo` (needed for Kata/KVM).

**Boot chain** (`check_boot.sh`)
- Measured boot: PCRs 0–7 populated and non-zero via `tpm2_pcrread sha256:0,4,7`
  (SKIP if `tpm2-tools` absent / no root).
- dm-verity on rootfs: `veritysetup status` or a `verity` target in `dmsetup table`
  (WARN, not FAIL, on a dev box where the immutable image isn't flashed).
- UKI / signed kernel hint: single `.efi` UKI in ESP, or `sbverify`/`bootctl` shows a
  signed kernel.

**Node OS — Ubuntu hardening** (`check_os.sh`)
- AppArmor enforce: `aa-status` — FAIL if any profile in complain mode or AppArmor disabled.
- auditd active: `systemctl is-active auditd`.
- FIPS kernel: `/proc/sys/crypto/fips_enabled == 1` (WARN if absent — Ubuntu Pro feature).
- CIS-ish spot checks (cheap, high-signal subset — not a full CIS scan): a few key sysctls,
  `umask`, root-only cron, no empty-password accounts in `/etc/shadow`.
- Minimal package set: WARN if compilers/package managers reachable (`gcc`, `apt` in an
  interactive path) per "no compilers/build tools" posture.

**Network** (`check_network.sh`)
- SSH hardening: `sshd -T` shows `permitrootlogin no`, `passwordauthentication no`.
- Network sysctls: `rp_filter=1`, `accept_redirects=0`, `send_redirects=0`,
  `accept_source_route=0` (CIS network params).
- Host firewall present: `ufw status` / `nft list ruleset` non-empty (WARN if none).
- Management-plane note: detect a separate BMC/management interface if discoverable
  (informational — full VLAN isolation can't be verified from inside the host).

**GPU / Fabric** (`check_gpu.sh`) — hardware expected present on this node
- NVIDIA GPUs present + model/count: `nvidia-smi --query-gpu=name` (**FAIL** if absent —
  this is a GPU node).
- NVLink up: `nvidia-smi nvlink --status` (WARN if not active).
- VFIO ready for Kata passthrough: `vfio-pci` module loaded, IOMMU groups viable.
- KVM / live VMs: `/dev/kvm` present and `virsh list` / running QEMU procs confirm the Kata
  microVMs are actually up (matches "VMs up and running").
- BlueField-3 SuperNIC / DPU present: `lspci | grep -i mellanox/bluefield` (informational).

**Confidential compute** (`check_confidential.sh`)
- CPU TEE: Intel TDX (`/sys/firmware/tdx` or `tdx` in cpuinfo flags) **or** AMD SEV-SNP
  (`/sys/module/kvm_amd/parameters/sev_snp == Y`, `sev` in cpuinfo). Reports which TEE.
- NVIDIA CC-mode: `nvidia-smi conf-compute -f` / `--query-gpu=cc.mode` reports `ON`
  (WARN if GPU present but CC-mode off — matches the sample preview).
- Reported as **readiness** (WARN when unavailable), since CC is "planned, post-evaluation"
  in the spec — not a hard FAIL on day one.

### Output & exit codes

- Human mode: grouped by layer, `[PASS]/[WARN]/[FAIL]/[SKIP]` colored lines, then a summary
  (`Result: N FAIL, M WARN — node {READY|NOT ready}`).
- `--json`: `{"node":<hostname>,"timestamp":...,"checks":[...],"summary":{pass,warn,fail,skip}}`.
- Exit code: `0` if no FAIL, `1` if any FAIL (so it can gate onboarding in a pipeline).
  `--strict` optionally promotes WARN to failure.
- Flags: `--json`, `--strict`, `--layer <name>` (run one layer), `-h/--help`.

## Files to create

- `preflight/bruk-preflight.sh` (entrypoint)
- `preflight/lib/common.sh` (helpers — the one piece all modules reuse)
- `preflight/lib/check_firmware.sh`
- `preflight/lib/check_boot.sh`
- `preflight/lib/check_os.sh`
- `preflight/lib/check_network.sh`
- `preflight/lib/check_gpu.sh`
- `preflight/lib/check_confidential.sh`
- `preflight/README.md`

No existing code to reuse (project is docs-only); all checks rely on standard CLI tools
already implied by the hardened-Ubuntu stack (`mokutil`, `tpm2-tools`, `aa-status`,
`auditd`, `nvidia-smi`, `lspci`, `sshd`, `sysctl`).

## Verification

The script is meant to run **on the live factory node** (GPUs installed, VMs running), so
verification happens there against real hardware:

1. `bash -n preflight/bruk-preflight.sh && bash -n preflight/lib/*.sh` — syntax check all files
   (safe to run anywhere, including before copying to the node).
2. `shellcheck preflight/**/*.sh` if available — lint (WARN-clean).
3. On the node: `sudo ./preflight/bruk-preflight.sh` — confirm it reports **real** results:
   TPM/Secure Boot/IOMMU from firmware, the actual NVIDIA GPU count/model, NVLink status,
   running KVM/Kata VMs, and CPU-TEE / NVIDIA CC-mode state. A correctly hardened node should
   come back all-PASS (CC may WARN per spec); any genuine gap shows as FAIL.
4. `sudo ./preflight/bruk-preflight.sh --json | jq .` — valid JSON with `checks` + `summary`,
   suitable for the attestation pipeline.
5. `sudo ./preflight/bruk-preflight.sh --layer gpu` (and `firmware`, `confidential`) — confirm
   single-layer selection works against the real devices.
6. Confirm exit code: `echo $?` is `1` when any FAIL present, `0` otherwise (so it can gate
   onboarding).
7. Cross-check a couple of results by hand on the node (`nvidia-smi`, `tpm2_pcrread`,
   `aa-status`) to confirm the script reads true state, not stubbed values.

(If syntax/lint steps 1–2 are run on a non-node machine first, hardware checks there would
correctly report FAIL/SKIP — that's expected; the authoritative run is step 3 on the node.)
