/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types and reasons for InferenceService. Ready = Configured AND
// WorkloadAvailable — kept separate because on clusters without CC/GPU
// capacity (kind, CI) Configured=True with WorkloadAvailable=False is a
// legitimate terminal state.
const (
	ConditionConfigured        = "Configured"
	ConditionWorkloadAvailable = "WorkloadAvailable"
	ConditionReady             = "Ready"
	ConditionProgressing       = "Progressing"

	ReasonWorkloadReady        = "WorkloadReady"
	ReasonWorkloadPending      = "WorkloadPending"
	ReasonConfigured           = "Configured"
	ReasonDeploying            = "Deploying"
	ReasonModelNotFound        = "ModelNotFound"
	ReasonTenantConfigMissing  = "TenantConfigMissing"
	ReasonTrustedStoreConflict = "TrustedStoreConflict"
	ReasonInvalidConfig        = "InvalidConfig"
	ReasonNotImplemented       = "NotImplemented"
)

// EngineSpec configures the vLLM engine for one deployment. There is
// deliberately no extraArgs escape hatch: the operator is deterministic
// (ADR-0002) and free-form args would defeat the audited surface.
//
// There is also NO per-workload image field in v1alpha1 (ADR-0008): the
// serving image is always BrukTenant.spec.engine.defaultImage — one reviewed,
// digest-pinned path kept lockstep with the seeded mirror. Per-workload images,
// if ever needed, return as an admin-only field / BrukEngineProfile.
type EngineSpec struct {
	// quantization passed to vLLM (--quantization=).
	// +optional
	// +kubebuilder:validation:Enum=fp8
	Quantization string `json:"quantization,omitempty"`

	// maxModelLen is the served context window (--max-model-len=). Required:
	// it is a per-deployment capacity decision, and may be smaller than the
	// model's native catalog.contextLength (the reconciler rejects larger).
	// The schema maximum is a coarse ceiling; the reconciler enforces the
	// tighter per-model contextLength bound.
	// +required
	// +kubebuilder:validation:Minimum=256
	// +kubebuilder:validation:Maximum=1048576
	MaxModelLen int32 `json:"maxModelLen"`

	// gpuMemoryUtilizationPercent is rendered as
	// --gpu-memory-utilization=<n/100> (no floats in CRDs).
	// +optional
	// +kubebuilder:default=90
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=99
	GPUMemoryUtilizationPercent int32 `json:"gpuMemoryUtilizationPercent,omitempty"`
}

// WorkloadResources sizes the workload pod. Platform-set: sizing is a
// capacity function, not a customer decision. Coarse ceilings (ADR-0008)
// bound resource-exhaustion; they sit well above real workloads (the 24B uses
// 64Gi / 8 CPU).
// +kubebuilder:validation:XValidation:rule="quantity(self.memory.limit).compareTo(quantity('512Gi')) <= 0",message="resources.memory.limit must not exceed 512Gi"
// +kubebuilder:validation:XValidation:rule="quantity(self.cpu.limit).compareTo(quantity('128')) <= 0",message="resources.cpu.limit must not exceed 128"
type WorkloadResources struct {
	// gpus is the nvidia.com/pgpu count. Capped at 1 in v1alpha1: single-GPU
	// TEE (SPT) is the only validated CC topology; multi-GPU CC (PPCIE)
	// waits for HGX B300 hardware. Relaxing a maximum later is
	// backward-compatible.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1
	GPUs int32 `json:"gpus,omitempty"`

	// memory limit/request for the container.
	// +required
	Memory ResourcePair `json:"memory"`

	// cpu limit/request for the container.
	// +required
	CPU ResourcePair `json:"cpu"`
}

// TrustedStoreSpec selects the volumeMode:Block device the kata-agent
// LUKS2-formats in-guest and mounts as the image store at the magic
// /dev/trusted_store devicePath. Exactly one member must be set.
// +kubebuilder:validation:XValidation:rule="has(self.existingClaim) != has(self.localVolume)",message="exactly one of existingClaim or localVolume must be set"
type TrustedStoreSpec struct {
	// existingClaim names a pre-created volumeMode:Block PVC
	// (manifests/trusted-storage.yaml). One workload per claim: a second
	// consumer would LUKS-format the store again and corrupt it — the
	// reconciler enforces this.
	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`

	// localVolume asks the operator to create the PV/PVC pair from an LVM
	// logical volume. Reserved in v1alpha1: the reconciler reports
	// NotImplemented. LV creation itself stays host-plane either way.
	// +optional
	LocalVolume *LocalVolumeSpec `json:"localVolume,omitempty"`
}

// LocalVolumeSpec identifies an LVM logical volume for the trusted store.
type LocalVolumeSpec struct {
	// lvName is the logical volume name inside
	// BrukTenant.spec.infrastructure.storageVolumeGroup; the PV path becomes
	// /dev/<vg>/<lvName>.
	// +required
	// +kubebuilder:validation:MinLength=1
	LVName string `json:"lvName"`

	// size of the PV/PVC. Rule of thumb: at least 2x the unpacked image size
	// (compressed layers and unpacked rootfs share the work dir).
	// +required
	Size resource.Quantity `json:"size"`
}

// WeightsCacheSpec enables the block-encrypted (non-Memory) emptyDir mounted
// at /root/.cache/huggingface, plus the IPv4-first gai.conf mount needed for
// Hugging Face downloads from the CC guest. Omit it for models small enough
// to live in guest ephemeral storage.
type WeightsCacheSpec struct {
	// sizeLimit caps the encrypted emptyDir; unset means a sparse image
	// bounded by node disk.
	// +optional
	SizeLimit *resource.Quantity `json:"sizeLimit,omitempty"`
}

// StorageSpec configures workload storage.
type StorageSpec struct {
	// trustedStore is the block device for the confidential image store.
	// +required
	TrustedStore TrustedStoreSpec `json:"trustedStore"`

	// weightsCache, when present, mounts an encrypted weights cache.
	// +optional
	WeightsCache *WeightsCacheSpec `json:"weightsCache,omitempty"`
}

// ProbeOverrides tunes workload probes.
type ProbeOverrides struct {
	// readinessFailureThreshold overrides the readiness failureThreshold.
	// Defaults: 120 without weightsCache (guest-pull only), 240 with it
	// (pull + weights download + quantize).
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	ReadinessFailureThreshold *int32 `json:"readinessFailureThreshold,omitempty"`
}

// InferenceServiceSpec defines the desired state of InferenceService: one
// confidential vLLM endpoint serving a BrukModel.
type InferenceServiceSpec struct {
	// modelRef names the BrukModel (same namespace) this service serves.
	// Immutable: a service is an endpoint FOR a model; serving a different
	// model is a new InferenceService.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="modelRef is immutable"
	ModelRef LocalRef `json:"modelRef"`

	// engine configures vLLM.
	// +required
	Engine EngineSpec `json:"engine"`

	// resources sizes the workload pod.
	// +required
	Resources WorkloadResources `json:"resources"`

	// storage configures the trusted store and optional weights cache.
	// +required
	Storage StorageSpec `json:"storage"`

	// probes tunes workload probes.
	// +optional
	Probes *ProbeOverrides `json:"probes,omitempty"`
}

// InferenceServiceStatus defines the observed state of InferenceService.
// CC pods have empty logs by design — status must carry enough to debug
// without them.
type InferenceServiceStatus struct {
	// observedGeneration is the most recent generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Configured (spec valid, children rendered and applied),
	// WorkloadAvailable (Deployment Available), Ready (both), Progressing.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// endpoint is the cluster-internal URL; set once the Service exists and
	// may precede Ready=True.
	// +optional
	Endpoint *EndpointStatus `json:"endpoint,omitempty"`

	// servedModelName is denormalized from the BrukModel so clients know the
	// exact string to send in the OpenAI "model" field.
	// +optional
	ServedModelName string `json:"servedModelName,omitempty"`

	// resolvedImage is the digest-pinned image actually rendered (visible
	// even when defaulted from BrukTenant).
	// +optional
	ResolvedImage string `json:"resolvedImage,omitempty"`

	// appliedInitDataHash is the short sha256 of the initdata blob rendered
	// into the pod annotation; matches BrukTenant.status.appliedInitDataHash
	// when converged. Lets humans correlate "why did my pods roll".
	// +optional
	AppliedInitDataHash string `json:"appliedInitDataHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bsvc,categories=bruk
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint.url`

// InferenceService is the Schema for the inferenceservices API
type InferenceService struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of InferenceService
	// +required
	Spec InferenceServiceSpec `json:"spec"`

	// status defines the observed state of InferenceService
	// +optional
	Status InferenceServiceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// InferenceServiceList contains a list of InferenceService
type InferenceServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []InferenceService `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &InferenceService{}, &InferenceServiceList{})
		return nil
	})
}
