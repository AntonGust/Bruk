# install/ — reproduce a Bruk confidential-serving cluster

Turns the prose `docs/RUNBOOK-confidential-serving.md` into an executable, near-push-button install for
one node/cluster. This is **Level 1** reproducibility (a bootstrap script); the **Level 2** target
(GitOps/Flux + Airon Operator + fleet-plane provisioning) is described in `docs/deployment-model.md` and
not built yet.

## The one manual, per-machine step: BIOS
Firmware can't be scripted from a repo (it's per-machine, via BMC/console). Set the SEV-SNP CBS recipe
first — see `docs/h100-bios-cbs-checklist.md`. The gotcha: enable **both** `SEV-SNP Support` **and**
`SNP Memory (RMP Table) Coverage` (NBIO), plus SMEE + the ASID split. IOMMU on; no `iommu=pt`.

## Then: clone → host-setup → reboot → bootstrap
On the freshly-imaged node (Ubuntu 24.04), after BIOS:
```bash
git clone <bruk-repo> && cd Bruk

sudo install/host-setup.sh      # HWE kernel + VFIO bind + sanity checks
sudo reboot                     # ⚠️ SEV-SNP box: 20-30 min (PSP RMP init in POST) — not a hang

# after it comes back, verify SEV-SNP is live (host-setup prints the exact checks), then:
install/bootstrap.sh            # k3s → Cilium → GPU Operator → kata-deploy → CC node → mirror → workload
```
End state: `Qwen2.5-0.5B` serving inside a SEV-SNP + CC-GPU Kata guest, image pulled from the local
registry mirror. bootstrap.sh prints the three proofs (mirror pull, `dmesg` SEV-SNP, a completion).

## What each piece is
| File | Role |
|---|---|
| `host-setup.sh` | Host layer above BIOS: HWE kernel, VFIO/nouveau, initramfs, cmdline/boot-order checks. Reboot after. |
| `bootstrap.sh` | Cluster layer: k3s (+ `runtime-request-timeout=20m`) → Helm (Cilium, GPU Operator, kata-deploy) → CC-node flip → registry mirror + seed → confidential workload. Checkpointed. |
| `helm-values/*.yaml` | The Helm config as committed files (previously prose-only): `cilium`, `gpu-operator` (sandbox/kata + k3s toolkit paths), `kata-deploy` (k8sDistribution=k3s). |
| `../manifests/registry/*` | Registry, digest-pinned seed Job, `cc_init_data` initdata + `build-initdata.sh`. |
| `../manifests/h100-vllm-cc-smoke.yaml` | The confidential small-model workload (mirror annotation injected at apply). |

## Honesty / caveats
- **Not run end-to-end as a script yet.** Every step is individually verified from the 2026-07-01
  bring-up, but the scripts themselves haven't been executed whole (doing so would re-install k3s on the
  live reference box). Watch the first real run on a fresh node.
- **Small model only.** The 24B confidential path needs block-device storage (ADR-0006 Part 2); this
  install stack is the prerequisite, not that.
- **Per-machine specifics:** `GPU_DEVICE_ID` in `host-setup.sh` (default `10de:2321` = H100 NVL) and the
  boot-order fix (box-specific EFI entry ids) may need adjusting on different hardware.
- **Level 2 (GitOps/fleet)** is the path to "no bootstrap script, just point Flux at the repo, and only
  BIOS is manual per machine" — see `docs/deployment-model.md`.

## Pointers
`docs/RUNBOOK-confidential-serving.md` (the narrative + verification), `docs/deployment-model.md` (how
this fits the fleet model), `docs/adr/0006-*` (why the mirror/storage), `docs/h100-bringup-status.md`.
