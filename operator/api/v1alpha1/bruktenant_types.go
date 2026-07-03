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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// BrukTenantName is the enforced name of the singleton BrukTenant.
const BrukTenantName = "cluster"

// Condition reasons for BrukTenant and BrukModel.
const (
	ReasonValid              = "Valid"
	ReasonTokenSecretMissing = "TokenSecretMissing"
	// ReasonUnpinnedRevision is a non-fatal warning: the model is Ready but its
	// HuggingFace revision is not pinned (weights may change after review).
	ReasonUnpinnedRevision = "UnpinnedRevision"
)

// InfrastructureConfig describes the cluster's physical layout.
type InfrastructureConfig struct {
	// nodeHostname is the GPU node (one node per cluster today). Used only
	// for PV nodeAffinity when the operator creates localVolume PVs; pods
	// are pinned transitively via volume topology, exactly like the
	// hand-written manifests (no nodeSelector on Deployments).
	// +required
	// +kubebuilder:validation:MinLength=1
	NodeHostname string `json:"nodeHostname"`

	// storageVolumeGroup is the LVM VG holding trusted-store logical
	// volumes (manifests/trusted-storage.yaml).
	// +optional
	// +kubebuilder:default=bruk
	StorageVolumeGroup string `json:"storageVolumeGroup,omitempty"`
}

// EngineDefaults carries cluster-wide engine defaults.
type EngineDefaults struct {
	// defaultImage is the cluster default vLLM image, digest-pinned. MUST
	// stay in lockstep with the registry mirror seed
	// (manifests/registry/seed-job.yaml): the mirror only serves seeded
	// digests, so an unseeded digest cannot be pulled by CC guests.
	// +required
	// +kubebuilder:validation:Pattern=`^[^@]+@sha256:[a-f0-9]{64}$`
	DefaultImage string `json:"defaultImage"`
}

// ConfidentialConfig carries the confidential-computing configuration.
type ConfidentialConfig struct {
	// initDataB64 is base64(gzip(initdata.toml)) — the value of the
	// io.katacontainers.config.hypervisor.cc_init_data pod annotation. In
	// tenant Git this field carries ${INITDATA_B64} and Flux
	// postBuild.substitute fills it (gitops/apps/cluster.yaml), the same
	// pipe the static manifests use today. Changing it rolls every CC pod
	// on the cluster.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9+/=]+$`
	InitDataB64 string `json:"initDataB64"`
}

// BrukTenantSpec defines the desired state of BrukTenant. In v1alpha1 this
// object is the single-tenant CLUSTER contract (per-cluster platform config
// under ADR-0001 "the cluster is the tenant boundary") — not an end-user
// tenant abstraction. Platform-written; customers never author it.
type BrukTenantSpec struct {
	// tenantID is the fleet-plane identity (opaque). Optional until the
	// fleet plane exists.
	// +optional
	TenantID string `json:"tenantID,omitempty"`

	// displayName of the cluster/tenant.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName,omitempty"`

	// infrastructure describes the cluster's physical layout.
	// +required
	Infrastructure InfrastructureConfig `json:"infrastructure"`

	// engine carries cluster-wide engine defaults.
	// +required
	Engine EngineDefaults `json:"engine"`

	// confidential carries the confidential-computing configuration.
	// +required
	Confidential ConfidentialConfig `json:"confidential"`
}

// BrukTenantStatus defines the observed state of BrukTenant.
type BrukTenantStatus struct {
	// observedGeneration is the most recent generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready means the config validated.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// appliedInitDataHash is the short sha256 of spec.confidential
	// .initDataB64 — lets humans correlate "why did my pods roll" across
	// InferenceService statuses.
	// +optional
	AppliedInitDataHash string `json:"appliedInitDataHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=bt,categories=bruk
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="BrukTenant is a cluster-scoped singleton and must be named 'cluster'"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.spec.infrastructure.nodeHostname`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// BrukTenant is the Schema for the bruktenants API
type BrukTenant struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of BrukTenant
	// +required
	Spec BrukTenantSpec `json:"spec"`

	// status defines the observed state of BrukTenant
	// +optional
	Status BrukTenantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BrukTenantList contains a list of BrukTenant
type BrukTenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BrukTenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &BrukTenant{}, &BrukTenantList{})
		return nil
	})
}
