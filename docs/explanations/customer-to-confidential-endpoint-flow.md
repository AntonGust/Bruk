# Customer-To-Confidential-Endpoint Flow

This explains how the planned Airon Operator flow connects the customer experience to the Bruk confidential serving stack.

Bruk has three main layers:

```text
Website / API
    ↓
Bruk product objects
    ↓
Kubernetes confidential serving stack
```

The customer should not need to know about Kubernetes, Flux, Kata, GPUs, PVCs, or vLLM. They interact with Bruk at the product level.

## 1. Customer Becomes A Tenant

A customer signs up on the Bruk website.

Behind the scenes, Bruk creates a tenant record. In the Kubernetes/operator world, that maps to tenant configuration.

In this phase, `BrukTenant` is cluster-level config:

```text
This cluster belongs to this tenant setup.
Use this node.
Use this confidential runtime setup.
Use this trusted storage config.
Use this default vLLM image.
Use this initdata for confidential image pulling.
```

The customer sees something like:

```text
Tenant: Acme Corp
Region/cluster: Bruk secure H100 node
```

The cluster sees something like:

```yaml
kind: BrukTenant
metadata:
  name: cluster
spec:
  infrastructure:
    nodeHostname: anton-bruk
  engine:
    defaultImage: docker.io/vllm/vllm-openai@sha256:...
  confidential:
    initDataB64: ...
```

## 2. Customer Chooses A Model

From the website, the customer chooses a model.

Examples:

```text
Mistral Small 3.1 24B
Qwen 0.5B
Llama model
Custom Hugging Face model
```

That becomes a `BrukModel`.

A `BrukModel` describes the model itself:

```text
Where does the model come from?
What is its Hugging Face repo?
Does it need an HF token?
What is its context length?
What is its display name?
What license does it have?
What modalities does it support?
```

Example:

```yaml
kind: BrukModel
metadata:
  name: mistral-small
spec:
  source:
    huggingFace:
      repo: mistralai/Mistral-Small-3.1-24B-Instruct-2503
      tokenSecretRef:
        name: hf-token
  servedName: mistral-small-3.1
  catalog:
    displayName: Mistral Small 3.1
    contextLength: 32768
```

If the customer chooses from Bruk's website catalog, Bruk creates this automatically.

If the customer brings their own Hugging Face model, the website/API would collect:

```text
HF repo name
revision
token secret if needed
context length
serving settings
```

Then Bruk creates the `BrukModel`.

## 3. Customer Starts A Serving Instance

Choosing a model does not necessarily mean it is running yet.

To actually serve it, Bruk creates an `InferenceService`.

This means:

```text
Please run this model as an API endpoint.
Use this much GPU/memory/CPU.
Use this trusted storage.
Expose it as a service.
```

Example:

```yaml
kind: InferenceService
metadata:
  name: mistral-small-api
spec:
  modelRef:
    name: mistral-small
  engine:
    quantization: fp8
    maxModelLen: 32768
    gpuMemoryUtilizationPercent: 90
  resources:
    gpus: 1
    memory: 64Gi
    cpu: "8"
  storage:
    trustedStore:
      existingClaim: trusted-image-24b
```

The product-level action is:

```text
Customer clicks Deploy model.
```

The platform-level result is:

```text
Bruk creates an InferenceService custom resource.
```

## 4. Flux Brings The Desired State Into The Cluster

Bruk uses GitOps.

That means the desired state lives in Git.

```text
Website/API creates or updates config
        ↓
Config is committed or written into the GitOps path
        ↓
Flux sees the change
        ↓
Flux applies it to the cluster
```

Flux does not deeply understand the model. It makes sure the cluster matches Git.

Flux applies:

```text
BrukTenant
BrukModel
InferenceService
```

Then the operator takes over.

## 5. Airon Operator Reconciles The Objects

The Airon Operator watches Bruk custom resources.

It sees:

```text
There is an InferenceService called mistral-small-api.
It references BrukModel mistral-small.
The cluster config is in BrukTenant.
```

Then it combines them:

```text
BrukTenant + BrukModel + InferenceService
        ↓
Kubernetes Deployment + Service
```

The generated Deployment contains the confidential-compute details:

```text
Run vLLM
Use H100 GPU
Use Kata confidential runtime
Use digest-pinned vLLM image
Use trusted image storage
Inject confidential initdata
Mount Hugging Face cache
Use HF token if needed
Expose port 8000
Check /health until ready
```

The customer never has to write that YAML.

## 6. Kubernetes Starts The Confidential vLLM Pod

Kubernetes schedules the generated Deployment.

Then the confidential serving process starts:

```text
Pod starts inside Kata confidential guest
        ↓
Guest pulls the digest-pinned vLLM image
        ↓
Image is stored on trusted encrypted block storage
        ↓
vLLM starts
        ↓
Model weights download from Hugging Face if needed
        ↓
Weights are stored inside confidential/guest-protected storage
        ↓
vLLM loads the model onto the H100
        ↓
Readiness probe becomes healthy
```

At this point the model is actually serving.

## 7. Operator Updates Status

The operator writes status back onto the `InferenceService`.

Example:

```yaml
status:
  conditions:
    - type: Ready
      status: "True"
  endpointURL: http://mistral-small-api.default.svc.cluster.local:8000
  resolvedImage: docker.io/vllm/vllm-openai@sha256:...
```

The website can read this status and show the customer:

```text
Status: Ready
Endpoint: https://api.bruk.ai/v1/acme/mistral-small
```

Internally, the Kubernetes service might be:

```text
http://mistral-small-api.default.svc.cluster.local:8000
```

Externally, Bruk would later expose a nicer customer-facing API endpoint, likely OpenRouter-style:

```text
POST /v1/chat/completions
model: mistral-small-3.1
```

## Full Flow

```text
Customer signs up
        ↓
Bruk creates tenant config
        ↓
Customer chooses model from website or HF
        ↓
Bruk creates BrukModel
        ↓
Customer clicks Deploy
        ↓
Bruk creates InferenceService
        ↓
Flux applies CRs to cluster
        ↓
Airon Operator sees the CRs
        ↓
Operator creates Deployment + Service
        ↓
Kubernetes starts confidential vLLM pod
        ↓
Model downloads/loads
        ↓
Service becomes ready
        ↓
Bruk exposes API endpoint
        ↓
Customer sends inference requests
```

## Phase 3.2 Boundary

Phase 3.2 builds the operator and CRD foundation.

The actual website, catalog service, public customer API, and OpenRouter-style routing layer are designed for later phases.

This phase makes the cluster side ready for that product experience.
