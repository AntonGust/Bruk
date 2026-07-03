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

// Modality is an input/output modality of a model.
// +kubebuilder:validation:Enum=text;image
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
)

// ModelFeature is an optional capability of a model.
// +kubebuilder:validation:Enum=tool-calling;structured-output
type ModelFeature string

const (
	FeatureToolCalling      ModelFeature = "tool-calling"
	FeatureStructuredOutput ModelFeature = "structured-output"
)

// ModelSource says how the engine obtains the model. Exactly one member must
// be set. v1alpha1 ships HuggingFace-download only (ADR-0006 Part 2: first-run
// download into encrypted storage); ociArtifact/preStaged are future members.
// +kubebuilder:validation:XValidation:rule="has(self.huggingFace)",message="a model source must be set (huggingFace)"
type ModelSource struct {
	// huggingFace downloads the model from Hugging Face at pod start.
	// +optional
	HuggingFace *HuggingFaceSource `json:"huggingFace,omitempty"`
}

// HuggingFaceSource identifies a Hugging Face model repository.
type HuggingFaceSource struct {
	// repo is the Hugging Face repository, e.g. "Qwen/Qwen2.5-0.5B-Instruct".
	// Rendered as --model=.
	// +required
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`
	Repo string `json:"repo"`

	// revision pins a Hugging Face commit for deterministic weights.
	// +optional
	Revision string `json:"revision,omitempty"`

	// tokenSecretRef names the Secret holding the HF token for gated models
	// (delivered out-of-band, never in Git). Presence means "gated".
	// +optional
	TokenSecretRef *SecretKeyRef `json:"tokenSecretRef,omitempty"`
}

// PricingSpec is OpenRouter-style pricing metadata, per 1M tokens. Decimal
// strings, never floats.
type PricingSpec struct {
	// promptPerMTokens is the price per 1M prompt tokens.
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	PromptPerMTokens string `json:"promptPerMTokens,omitempty"`

	// completionPerMTokens is the price per 1M completion tokens.
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	CompletionPerMTokens string `json:"completionPerMTokens,omitempty"`

	// currency of the prices.
	// +optional
	// +kubebuilder:validation:Enum=EUR;USD
	// +kubebuilder:default=EUR
	Currency string `json:"currency,omitempty"`
}

// CatalogSpec is the OpenRouter-style catalog metadata for a model. It is
// served by the (future) catalog service; the operator only validates it.
type CatalogSpec struct {
	// displayName is the human-readable model name.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName"`

	// description of the model for the catalog.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Description string `json:"description,omitempty"`

	// contextLength is the model's NATIVE maximum context window (a catalog
	// fact). A deployment may serve a smaller window
	// (InferenceService.spec.engine.maxModelLen).
	// +required
	// +kubebuilder:validation:Minimum=256
	ContextLength int32 `json:"contextLength"`

	// inputModalities the model accepts.
	// +optional
	// +listType=atomic
	InputModalities []Modality `json:"inputModalities,omitempty"`

	// outputModalities the model produces.
	// +optional
	// +listType=atomic
	OutputModalities []Modality `json:"outputModalities,omitempty"`

	// features the model supports.
	// +optional
	// +listType=atomic
	Features []ModelFeature `json:"features,omitempty"`

	// license is the SPDX id, e.g. "Apache-2.0". The model library is
	// OSI-licensed only (CONTEXT.md).
	// +optional
	// +kubebuilder:validation:MaxLength=64
	License string `json:"license,omitempty"`

	// pricing metadata for the catalog.
	// +optional
	Pricing *PricingSpec `json:"pricing,omitempty"`
}

// BrukModelSpec defines the desired state of BrukModel: model identity
// (what/where from) plus catalog metadata. The source is platform-curated;
// customers pick from the library, they don't author sources.
type BrukModelSpec struct {
	// source says how the engine obtains the model.
	// +required
	Source ModelSource `json:"source"`

	// servedName is the public model id: rendered as --served-model-name and
	// later the OpenRouter-style catalog id. Defaults to metadata.name.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	ServedName string `json:"servedName,omitempty"`

	// catalog is the OpenRouter-style metadata for this model.
	// +required
	Catalog CatalogSpec `json:"catalog"`
}

// BrukModelStatus defines the observed state of BrukModel.
type BrukModelStatus struct {
	// observedGeneration is the most recent generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the BrukModel resource.
	// Ready means the spec validated and, if a token secret is referenced,
	// it exists.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bm,categories=bruk
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.catalog.displayName`
// +kubebuilder:printcolumn:name="Context",type=integer,JSONPath=`.spec.catalog.contextLength`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// BrukModel is the Schema for the brukmodels API
type BrukModel struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of BrukModel
	// +required
	Spec BrukModelSpec `json:"spec"`

	// status defines the observed state of BrukModel
	// +optional
	Status BrukModelStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BrukModelList contains a list of BrukModel
type BrukModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BrukModel `json:"items"`
}

// ServedModelName returns spec.servedName, defaulting to metadata.name.
func (m *BrukModel) ServedModelName() string {
	if m.Spec.ServedName != "" {
		return m.Spec.ServedName
	}
	return m.Name
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &BrukModel{}, &BrukModelList{})
		return nil
	})
}
