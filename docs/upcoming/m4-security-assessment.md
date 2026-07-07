# M4 Security Assessment

Status: draft assessment, 2026-07-06

## Verdict

M4 is solid for a controlled single-tenant pilot. It covers the big obvious self-inflicted risks: no public vLLM endpoint, no hardcoded Hugging Face token, digest-pinned operator deployment, constrained CRD surface, least-privilege-oriented operator RBAC, GitOps reconciliation, and confidential storage mechanics.

It does not justify a claim that breaches are impossible or that a determined state-level attacker cannot compromise the system. The largest remaining risks are around the GitHub/GitOps control plane, host hardening, missing host attestation, and the not-yet-built production ingress/auth/rate-limit layer.

## Highest Risks

1. GitHub `main` is effectively production control.

   Flux follows `main`. The repository ruleset `main-requires-pr-and-operator-ci` is active, but GitHub reports `required_approving_review_count: 0`, `require_code_owner_review: false`, `require_last_push_approval: false`, and an always-on repository-role bypass. That protects against force-push/deletion and requires PR/checks, but it is not strong enough against account compromise or admin bypass.

   Recommended hardening: require at least one reviewer, add `CODEOWNERS`, require last-push approval, remove or tightly scope bypass, and require signed release tags or commits where practical.

2. Manifest/GitOps changes need their own required security gate.

   Operator changes have lint/test/e2e/govulncheck coverage, but manifest-only changes can skip most operator jobs even though `manifests/` and `gitops/` are live cluster config.

   Recommended hardening: add required `gitops-ci` for `kubectl kustomize`, schema validation, secret scanning, and policy checks forbidding `hostNetwork`, `LoadBalancer`, `NodePort`, privileged pods, `hostPath`, broad RBAC, and unpinned images.

3. Host attestation gate is not built.

   The docs correctly place fleet plane and host-attestation verifier as designed, not built. Today, secrets such as the Flux deploy key and `hf-token` still depend on manual trust in the box. This is acceptable for the pilot, but not for a hostile-production posture.

4. Production ingress, auth, and rate limiting are not built.

   Current workload Services are ClusterIP, which is the right M4 posture. Before public exposure, traffic must go through the planned enforcement layer: TLS, authentication, authorization, tenant isolation, and rate limiting. Do not expose `vllm-cc-svc` or `vllm-cc-smoke-svc` directly.

5. Runtime and host CVE management is outside the operator CI lane.

   Go dependencies have `govulncheck`, but k3s, Cilium, GPU Operator, Kata, NVIDIA drivers, firmware, kernel, and container bases need a separate patch and vulnerability review process. The operator Dockerfile base images also float by tag during rebuilds, even though the deployed image is pinned by digest.

## Covered Well

- `hf-token` is out of Git, and a local pattern scan found no obvious committed private keys or API tokens.
- The operator can `get` Secrets but cannot list or watch them.
- Leak-guard tests protect against token and initdata values leaking into CR status.
- CRDs avoid podSpec passthrough, `hostPath`, free-form args/env, per-workload images, public endpoints, replicas, and multi-GPU in v1alpha1.
- Workload Services and the local registry are ClusterIP-only.
- Image pull integrity rests on digest-pinned references; the local HTTP registry is not trusted for integrity.
- The one-PVC guard protects against LUKS double-format corruption during CR adoption.
- The operator pod runs non-root, drops Linux capabilities, and uses seccomp.

## Practical M4 Position

M4 is good enough to proceed as a careful reference-cluster milestone. It is not yet nation-state hardened.

Before treating this as production-secure, prioritize:

1. Harden GitHub rules and review requirements.
2. Add required GitOps policy CI for `manifests/` and `gitops/`.
3. Lock down host access, firewalling, SSH, and runtime patching.
4. Implement host attestation before secret release.
5. Build the external auth, authorization, and rate-limit ingress layer.

