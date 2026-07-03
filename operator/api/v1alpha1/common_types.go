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
)

// LocalRef is a reference to an object in the same namespace.
type LocalRef struct {
	// name of the referenced object.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// SecretKeyRef selects a key of a Secret in the same namespace. Secrets are
// delivered out-of-band (never in Git) — the reconciler only checks presence.
type SecretKeyRef struct {
	// name of the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// key within the Secret.
	// +optional
	// +kubebuilder:default=token
	Key string `json:"key,omitempty"`
}

// ResourcePair is a limit/request pair for one resource. request defaults to
// limit at render time when omitted.
type ResourcePair struct {
	// limit for the resource.
	// +required
	Limit resource.Quantity `json:"limit"`

	// request for the resource; defaults to limit when omitted.
	// +optional
	Request *resource.Quantity `json:"request,omitempty"`
}

// EndpointStatus describes where a workload is reachable inside the cluster.
type EndpointStatus struct {
	// url is the cluster-internal base URL of the OpenAI-compatible API,
	// e.g. http://<name>-svc.<namespace>.svc.cluster.local:8000/v1.
	// The public (Envoy) endpoint is a later phase.
	// +optional
	URL string `json:"url,omitempty"`
}
