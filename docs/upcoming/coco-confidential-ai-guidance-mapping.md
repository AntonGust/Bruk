# Bruk Mapping To CoCo Confidential AI Guidance

This note explains how Bruk maps to the Confidential Containers guidance for Confidential AI and federated learning.

Source: <https://confidentialcontainers.org/docs/use-cases/confidential-ai/>

The CoCo guidance says secure AI/federated-learning workloads need three big things:

1. Run the aggregator in a Confidential Container.
2. Run each client in a Confidential Container.
3. Only allow communication after correct attestation.

It also says Trustee can issue credentials such as TLS certificates or JWTs only after successful attestation.

## How Bruk Maps To That Guidance

Bruk today is focused on confidential model serving, not federated learning yet.

So the CoCo federated-learning language maps to Bruk like this:

```text
CoCo FL Aggregator
    ~= future Bruk central model/router/training coordinator

CoCo FL Client
    ~= each confidential model-serving or training workload

CoCo attested communication
    ~= future Bruk Trustee/KBS-issued credentials
```

For the current Bruk stack:

```text
Customer / platform request
        ↓
Bruk CRs: BrukModel + InferenceService
        ↓
Airon Operator
        ↓
Kubernetes Deployment
        ↓
Kata confidential container
        ↓
vLLM on H100 confidential GPU
```

## 1. Aggregator In A Confidential Container

Bruk does not have the federated-learning aggregator yet.

But the same pattern is already planned: future central Bruk services that coordinate models, tenants, routing, catalog, or training should run inside the same confidential-container setup.

Current status:

```text
Implemented for FL aggregator: no
Foundation: yes
Future work: run router/catalog/aggregator inside CoCo
```

## 2. Each Client In A Confidential Container

This is the part Bruk already does well.

The current vLLM workload runs with:

```yaml
runtimeClassName: kata-qemu-nvidia-gpu-snp
```

That means the model server runs inside a Kata confidential guest using SEV-SNP and the confidential GPU path.

Bruk also protects the serving stack with:

```text
digest-pinned vLLM image
confidential initdata
trusted image block storage
encrypted guest-side model cache
H100 confidential GPU mode
```

So Bruk's current confidential vLLM pod is already a CoCo-protected AI participant.

## 3. Protect Local Data And Model Weights

Bruk handles this with two storage patterns:

```text
Image storage:
trusted block PVC mounted as /dev/trusted_store

Model weights:
encrypted guest-side storage / confidential emptyDir behavior
```

The host should not see plaintext model weights or image contents. The guest pulls and uses them inside the confidential environment.

This matches the CoCo idea that the infrastructure provider should not be able to freely inspect or tamper with AI workload data.

## 4. Aggregator Only Accepts Correctly Attesting Clients

This is not fully implemented yet.

Bruk has already proven attestation in earlier milestones, but Phase 3.2 does not yet build the full mutual-trust system where services only talk after verifying each other.

Current state:

```text
Attestation: proven / foundation exists
Mutual service-to-service attestation: future
Trustee/KBS credential gating: future
```

For future federated learning, Bruk would need:

```text
Client confidential container attests
        ↓
Trustee verifies measurement
        ↓
Trustee gives client TLS cert or JWT
        ↓
Aggregator accepts update only with that credential
```

## 5. Clients Only Accept Global Weights From A Correct Aggregator

This is also future work.

For Bruk, a model-training client should refuse global model updates unless the aggregator has proven it is the expected confidential workload.

The clean design would be:

```text
Aggregator attests successfully
        ↓
Trustee issues aggregator credential
        ↓
Client verifies aggregator credential
        ↓
Client accepts model weights/update
```

Without that, a malicious or compromised infrastructure component could try to feed clients bad weights.

## 6. Trustee Instead Of Verification Everywhere

This is aligned with Bruk's future direction.

Instead of every Bruk component manually verifying SEV-SNP/GPU/container measurements, Bruk can centralize policy in Trustee/KBS:

```text
Workload starts
        ↓
Workload attests to Trustee
        ↓
Trustee checks policy
        ↓
If valid, Trustee releases:
    TLS certificate
    JWT
    model decryption key
    registry credential
    storage key
```

Bruk is not fully there yet, but the plan already points toward KBS/Trustee as a later phase.

## Summary

For confidential inference, Bruk is already close to the CoCo recommended shape:

```text
vLLM runs inside Kata confidential container
H100 confidential GPU path is used
image is digest-pinned
initdata controls trusted pulling
model/image storage is protected
```

For attestation, Bruk has the foundation:

```text
Attestation has been proven,
but it is not yet used to issue live communication credentials.
```

For federated learning, Bruk still needs more work:

```text
No confidential aggregator yet
No Trustee-issued TLS/JWT between clients and aggregator yet
No client/aggregator mutual acceptance policy yet
```

Honest conclusion:

```text
For confidential inference: Bruk is already close to the CoCo recommended shape.

For federated learning: Bruk has the right foundation, but still needs Trustee/KBS-based mutual trust and a confidential aggregator/client protocol.
```
