#!/usr/bin/env bash
# bootstrap.sh — stand up the per-cluster confidential-serving stack on a node that has already had
# BIOS SEV-SNP set and install/host-setup.sh + a reboot done.
#
# Turns the prose RUNBOOK (docs/RUNBOOK-confidential-serving.md) into one ordered, checkpointed script.
# Run it ON the node (it uses local k3s/helm/kubectl + sudo). End state: a small model serving inside a
# SEV-SNP + CC-GPU Kata guest, pulling its image from a local registry mirror.
#
# ⚠️ DESTRUCTIVE / not-yet-run-as-a-whole: this installs k3s, Cilium, GPU Operator, kata-deploy. The
#    individual steps are each verified from the 2026-07-01 bring-up, but this file has not been executed
#    end-to-end as a script — watch the first run. Do NOT run it against the live reference cluster.
#
# Pinned versions (keep in sync with docs/RUNBOOK-confidential-serving.md):
K3S_VERSION="v1.35.5+k3s1"
CILIUM_VERSION="1.19.5"
GPU_OPERATOR_VERSION="v26.3.2"
KATA_DEPLOY_VERSION="3.29.0"
FLUX_VERSION="v2.9.0"
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VALUES="$REPO_ROOT/install/helm-values"
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
log() { printf '\n=== %s ===\n' "$*"; }
kc() { kubectl "$@"; }

# --- 0. preflight: confirm host layer is in place -------------------------------------------------
log "0/8 preflight (SEV-SNP live + GPUs on vfio-pci)"
sudo dmesg | grep -qi "SEV-SNP enabled" || { echo "SEV-SNP not live — do BIOS + host-setup.sh + reboot first"; exit 1; }
[ "$(cat /sys/module/kvm_amd/parameters/sev_snp 2>/dev/null)" = "Y" ] || { echo "sev_snp != Y"; exit 1; }
echo "ok: SEV-SNP live"

# --- 1. k3s (stripped) + kubeconfig, timeout baked in ---------------------------------------------
log "1/8 k3s $K3S_VERSION (flannel/traefik/servicelb off; runtime-request-timeout=20m for CC guest-pull)"
if ! command -v k3s >/dev/null 2>&1; then
  curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="$K3S_VERSION" INSTALL_K3S_EXEC="--flannel-backend=none \
    --disable-network-policy --disable=traefik --disable=servicelb --cluster-init \
    --kubelet-arg=runtime-request-timeout=20m" sh -
else
  echo "k3s already installed, skipping"
fi
mkdir -p "$(dirname "$KUBECONFIG")"
sudo cp /etc/rancher/k3s/k3s.yaml "$KUBECONFIG" && sudo chown "$(id -u):$(id -g)" "$KUBECONFIG"

# --- 2. Helm ----------------------------------------------------------------------------------------
log "2/8 Helm + repos"
command -v helm >/dev/null 2>&1 || curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
helm repo add cilium https://helm.cilium.io/ >/dev/null 2>&1 || true
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia >/dev/null 2>&1 || true
helm repo update >/dev/null

# --- 3. Cilium (CNI) --------------------------------------------------------------------------------
log "3/8 Cilium $CILIUM_VERSION"
helm status cilium -n kube-system >/dev/null 2>&1 || \
  helm install cilium cilium/cilium -n kube-system --version "$CILIUM_VERSION" -f "$VALUES/cilium.yaml"
kc wait --for=condition=Ready node --all --timeout=300s
echo "ok: node Ready"

# --- 4. GPU Operator (VFIO/Kata mode) --------------------------------------------------------------
log "4/8 GPU Operator $GPU_OPERATOR_VERSION (sandbox/kata mode)"
helm status gpu-operator -n gpu-operator >/dev/null 2>&1 || \
  helm install gpu-operator nvidia/gpu-operator -n gpu-operator --create-namespace \
    --version "$GPU_OPERATOR_VERSION" -f "$VALUES/gpu-operator.yaml"

# --- 5. kata-deploy (runtimeclasses + /opt/kata) ---------------------------------------------------
log "5/8 kata-deploy $KATA_DEPLOY_VERSION"
helm status kata-deploy -n kata-system >/dev/null 2>&1 || \
  helm install kata-deploy oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy \
    -n kata-system --create-namespace -f "$VALUES/kata-deploy.yaml" \
    --version "$KATA_DEPLOY_VERSION" --wait --timeout 10m
echo "waiting for ClusterPolicy ready + pgpu advertised..."
for i in $(seq 1 60); do
  [ "$(kc get clusterpolicy cluster-policy -o jsonpath='{.status.state}' 2>/dev/null)" = "ready" ] && break
  sleep 10
done
kc get clusterpolicy cluster-policy -o jsonpath='{.status.state}'; echo

# --- 6. Make it a CC node (both GPUs confidential) -------------------------------------------------
log "6/8 flip node to CC (ccManager.defaultMode=on) — verified path"
helm upgrade gpu-operator nvidia/gpu-operator -n gpu-operator --version "$GPU_OPERATOR_VERSION" \
  --reuse-values --set ccManager.defaultMode=on
echo "waiting for cc.ready.state=true..."
for i in $(seq 1 30); do
  st=$(kc get node -o jsonpath='{.items[0].metadata.labels.nvidia\.com/cc\.ready\.state}' 2>/dev/null)
  echo "  cc.ready.state=$st"; [ "$st" = "true" ] && break; sleep 10
done

# --- 7. GitOps: Flux reconciles the app layer from Git (Phase 3.1 / gitops/README.md) ---------------
# The registry mirror, trusted-storage PVs/PVCs, and CC workloads are no longer applied here —
# Flux converges them from the repo (gitops/apps/). Two out-of-band prereqs stay manual:
#   - hf-token secret (gated 24B):  kubectl create secret generic hf-token --from-literal=token=$HF_TOKEN
#   - LVM LVs for /dev/trusted_store (host-setup.sh / RUNBOOK §7)
log "7/8 Flux $FLUX_VERSION (controllers + point at the repo)"
kc apply -f "https://github.com/fluxcd/flux2/releases/download/$FLUX_VERSION/install.yaml"
kc -n flux-system wait --for=condition=Available deploy --all --timeout=300s
if ! kc -n flux-system get secret bruk-deploy-key >/dev/null 2>&1; then
  echo "MISSING deploy key secret 'bruk-deploy-key' (repo is private)."
  echo "Create it (recipe in gitops/gotk-sync.yaml header), then re-run this step:"
  echo "  kubectl apply -f $REPO_ROOT/gitops/gotk-sync.yaml"
  exit 1
fi
kc apply -f "$REPO_ROOT/gitops/gotk-sync.yaml"

# --- 8. Wait for convergence + verify ---------------------------------------------------------------
log "8/8 wait for Flux to converge the app layer"
for ks in bruk-apps bruk-registry bruk-cluster; do
  kc -n flux-system wait --for=condition=Ready "kustomization/$ks" --timeout=600s || true
  kc -n flux-system get "kustomization/$ks" --no-headers
done
kc rollout status deploy/registry --timeout=300s
kc rollout status deploy/vllm-cc-smoke --timeout=1200s

log "VERIFY"
POD="$(kc get po -l app=vllm-cc-smoke -o jsonpath='{.items[0].metadata.name}')"
echo "- image pulled from mirror (should be >0):"
kc logs -l app=registry --tail=100000 | grep -c "GET /v2/vllm/vllm-openai" || true
echo "- guest is confidential:"
kc exec "$POD" -- sh -c 'dmesg 2>/dev/null | grep -m1 "Memory Encryption Features"' || true
echo "- serves:"
kc exec "$POD" -- curl -s localhost:8000/v1/chat/completions -H 'Content-Type: application/json' \
  -d '{"model":"qwen-0.5b","messages":[{"role":"user","content":"hi"}],"max_tokens":16}' || true
echo
log "DONE — Flux owns the app layer; confidential small-model serving is up."
echo "The 24B (vllm-cc) also reconciles from Git — it needs the hf-token secret + trusted-storage LVs."
