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

package controller

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

// Test fixtures mirroring config/samples (the smoke workload shapes).

const (
	testVLLMImage = "docker.io/vllm/vllm-openai@sha256:d5b12dfb74d605615f8b29ebafaa52294c118bcac7bc9e941785c4108fdb913a"
	// base64(gzip(comment)) — syntactically valid, semantically inert in envtest.
	testInitDataB64 = "H4sIAAAAAAAAA1NWKMhJTE7NyM9JSS1SyMzLLElJLElUSMsvUihOzC3ISS3WTzVKVXjUMEXBzz9EIVGhKDUxRyEpJz+JCwCpEyiVOwAAAA=="

	// Shared test literals (satisfy goconst).
	secretTokenKey     = "token"
	pinnedModelName    = "pinned-model"
	tokenLeakModelName = "model-token-leak"
)

func validModelSpec() brukv1alpha1.BrukModelSpec {
	return brukv1alpha1.BrukModelSpec{
		Source: brukv1alpha1.ModelSource{
			HuggingFace: &brukv1alpha1.HuggingFaceSource{
				Repo: "Qwen/Qwen2.5-0.5B-Instruct",
			},
		},
		Catalog: brukv1alpha1.CatalogSpec{
			DisplayName:   "Qwen2.5 0.5B Instruct",
			ContextLength: 32768,
			License:       "Apache-2.0",
		},
	}
}

func validInferenceServiceSpec(modelName string) brukv1alpha1.InferenceServiceSpec {
	return brukv1alpha1.InferenceServiceSpec{
		ModelRef: brukv1alpha1.LocalRef{Name: modelName},
		Engine: brukv1alpha1.EngineSpec{
			MaxModelLen:                 8192,
			GPUMemoryUtilizationPercent: 30,
		},
		Resources: brukv1alpha1.WorkloadResources{
			GPUs: 1,
			Memory: brukv1alpha1.ResourcePair{
				Limit:   resource.MustParse("32Gi"),
				Request: ptrQuantity("16Gi"),
			},
			CPU: brukv1alpha1.ResourcePair{
				Limit:   resource.MustParse("8"),
				Request: ptrQuantity("4"),
			},
		},
		Storage: brukv1alpha1.StorageSpec{
			TrustedStore: brukv1alpha1.TrustedStoreSpec{
				ExistingClaim: "trusted-image-smoke",
			},
		},
	}
}

func validTenant() *brukv1alpha1.BrukTenant {
	return &brukv1alpha1.BrukTenant{
		ObjectMeta: metav1.ObjectMeta{Name: brukv1alpha1.BrukTenantName},
		Spec: brukv1alpha1.BrukTenantSpec{
			DisplayName: "envtest cluster",
			Infrastructure: brukv1alpha1.InfrastructureConfig{
				NodeHostname:       "anton-bruk",
				StorageVolumeGroup: "bruk",
			},
			Engine:       brukv1alpha1.EngineDefaults{DefaultImage: testVLLMImage},
			Confidential: brukv1alpha1.ConfidentialConfig{InitDataB64: testInitDataB64},
		},
	}
}

func ptrQuantity(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

func mustQ(s string) resource.Quantity {
	return resource.MustParse(s)
}
