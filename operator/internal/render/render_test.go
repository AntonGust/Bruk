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

package render_test

// Golden-manifest contract tests (the operator's OUTPUT CONTRACT).
//
// The fixtures are the real committed manifests the cluster runs today:
//   manifests/h100-vllm-cc-smoke.yaml  (slice 1: no weights cache)
//   manifests/h100-vllm-cc.yaml        (slice 2: weights cache + gated model)
//
// Comparison scope (render contract, docs/plan Phase 3.2 contract B):
//   Deployment: spec.replicas, spec.strategy, spec.selector, and the full
//     spec.template (labels, the cc_init_data annotation, runtimeClassName,
//     container image/args/env/ports/resources/volumeMounts/volumeDevices/
//     readinessProbe, volumes).
//   Service: spec.selector, spec.ports, spec.type.
//   ConfigMap (gai-ipv4-first): data.
// Normalization: resource quantities compared semantically; env, volumes and
// volumeMounts sorted by name (k8s treats them as sets for our usage).
// Fields outside this list (ownerRefs, managed-by labels, status, defaulted
// server-side fields) are NOT part of the contract and may differ freely.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
	"github.com/AntonGust/Bruk/operator/internal/render"
)

const (
	smokeManifestPath = "../../../manifests/h100-vllm-cc-smoke.yaml"
	prodManifestPath  = "../../../manifests/h100-vllm-cc.yaml"

	testNamespace   = "default"
	testVLLMImage   = "docker.io/vllm/vllm-openai@sha256:d5b12dfb74d605615f8b29ebafaa52294c118bcac7bc9e941785c4108fdb913a"
	testInitDataB64 = "H4sIAAAAAAAAA1NWKMhJTE7NyM9JSS1SyMzLLElJLElUSMsvUihOzC3ISS3WTzVKVXjUMEXBzz9EIVGhKDUxRyEpJz+JCwCpEyiVOwAAAA=="
)

var quantityComparer = cmp.Comparer(func(a, b resource.Quantity) bool {
	return a.Cmp(b) == 0
})

func testConfig() render.Config {
	return render.Config{
		DefaultImage: testVLLMImage,
		InitDataB64:  testInitDataB64,
	}
}

func smokeModel() *brukv1alpha1.BrukModel {
	return &brukv1alpha1.BrukModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-0.5b", Namespace: testNamespace},
		Spec: brukv1alpha1.BrukModelSpec{
			Source: brukv1alpha1.ModelSource{
				HuggingFace: &brukv1alpha1.HuggingFaceSource{
					Repo: "Qwen/Qwen2.5-0.5B-Instruct",
				},
			},
			Catalog: brukv1alpha1.CatalogSpec{
				DisplayName:   "Qwen2.5 0.5B Instruct",
				ContextLength: 32768,
			},
		},
	}
}

func smokeService() *brukv1alpha1.InferenceService {
	return &brukv1alpha1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "vllm-cc-smoke", Namespace: testNamespace},
		Spec: brukv1alpha1.InferenceServiceSpec{
			ModelRef: brukv1alpha1.LocalRef{Name: "qwen-0.5b"},
			Engine: brukv1alpha1.EngineSpec{
				MaxModelLen:                 8192,
				GPUMemoryUtilizationPercent: 30,
			},
			Resources: brukv1alpha1.WorkloadResources{
				GPUs:   1,
				Memory: brukv1alpha1.ResourcePair{Limit: resource.MustParse("32Gi"), Request: ptrQ("16Gi")},
				CPU:    brukv1alpha1.ResourcePair{Limit: resource.MustParse("8"), Request: ptrQ("4")},
			},
			Storage: brukv1alpha1.StorageSpec{
				TrustedStore: brukv1alpha1.TrustedStoreSpec{ExistingClaim: "trusted-image-smoke"},
			},
		},
	}
}

func prodModel() *brukv1alpha1.BrukModel {
	return &brukv1alpha1.BrukModel{
		ObjectMeta: metav1.ObjectMeta{Name: "mistral-small-3.1-24b", Namespace: testNamespace},
		Spec: brukv1alpha1.BrukModelSpec{
			Source: brukv1alpha1.ModelSource{
				HuggingFace: &brukv1alpha1.HuggingFaceSource{
					Repo:           "mistralai/Mistral-Small-3.1-24B-Instruct-2503",
					TokenSecretRef: &brukv1alpha1.SecretKeyRef{Name: "hf-token", Key: "token"},
				},
			},
			ServedName: "mistral-small-3.1",
			Catalog: brukv1alpha1.CatalogSpec{
				DisplayName:   "Mistral Small 3.1 24B",
				ContextLength: 131072,
			},
		},
	}
}

func prodService() *brukv1alpha1.InferenceService {
	return &brukv1alpha1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "vllm-cc", Namespace: testNamespace},
		Spec: brukv1alpha1.InferenceServiceSpec{
			ModelRef: brukv1alpha1.LocalRef{Name: "mistral-small-3.1-24b"},
			Engine: brukv1alpha1.EngineSpec{
				Quantization:                "fp8",
				MaxModelLen:                 32768,
				GPUMemoryUtilizationPercent: 90,
			},
			Resources: brukv1alpha1.WorkloadResources{
				GPUs:   1,
				Memory: brukv1alpha1.ResourcePair{Limit: resource.MustParse("64Gi"), Request: ptrQ("32Gi")},
				CPU:    brukv1alpha1.ResourcePair{Limit: resource.MustParse("8"), Request: ptrQ("4")},
			},
			Storage: brukv1alpha1.StorageSpec{
				TrustedStore: brukv1alpha1.TrustedStoreSpec{ExistingClaim: "trusted-image-24b"},
				WeightsCache: &brukv1alpha1.WeightsCacheSpec{},
			},
		},
	}
}

func ptrQ(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

// goldenObjects decodes the multi-doc manifest at path with the initdata
// variable substituted, returning whatever of Deployment/Service/ConfigMap
// it contains.
func goldenObjects(t *testing.T, path string) (*appsv1.Deployment, *corev1.Service, *corev1.ConfigMap) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden manifest: %v", err)
	}
	substituted := strings.ReplaceAll(string(raw), "${INITDATA_B64}", testInitDataB64)

	var deployment *appsv1.Deployment
	var service *corev1.Service
	var configMap *corev1.ConfigMap
	for doc := range strings.SplitSeq(substituted, "\n---\n") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		// The split separator consumes the doc's final newline, which would
		// silently truncate trailing-newline-significant block scalars.
		doc += "\n"
		var meta metav1.TypeMeta
		if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
			t.Fatalf("decoding golden doc type: %v", err)
		}
		switch meta.Kind {
		case "Deployment":
			deployment = &appsv1.Deployment{}
			if err := yaml.UnmarshalStrict([]byte(doc), deployment); err != nil {
				t.Fatalf("decoding golden Deployment: %v", err)
			}
		case "Service":
			service = &corev1.Service{}
			if err := yaml.UnmarshalStrict([]byte(doc), service); err != nil {
				t.Fatalf("decoding golden Service: %v", err)
			}
		case "ConfigMap":
			configMap = &corev1.ConfigMap{}
			if err := yaml.UnmarshalStrict([]byte(doc), configMap); err != nil {
				t.Fatalf("decoding golden ConfigMap: %v", err)
			}
		default:
			t.Fatalf("unexpected kind %q in golden manifest", meta.Kind)
		}
	}
	return deployment, service, configMap
}

// normalizeDeployment sorts set-like lists so comparison is order-insensitive.
func normalizeDeployment(d *appsv1.Deployment) *appsv1.Deployment {
	d = d.DeepCopy()
	podSpec := &d.Spec.Template.Spec
	slices.SortFunc(podSpec.Volumes, func(a, b corev1.Volume) int {
		return strings.Compare(a.Name, b.Name)
	})
	for c := range podSpec.Containers {
		container := &podSpec.Containers[c]
		slices.SortFunc(container.Env, func(a, b corev1.EnvVar) int {
			return strings.Compare(a.Name, b.Name)
		})
		slices.SortFunc(container.VolumeMounts, func(a, b corev1.VolumeMount) int {
			return strings.Compare(a.Name, b.Name)
		})
		slices.SortFunc(container.VolumeDevices, func(a, b corev1.VolumeDevice) int {
			return strings.Compare(a.Name, b.Name)
		})
	}
	return d
}

func assertDeploymentMatchesGolden(t *testing.T, rendered, golden *appsv1.Deployment) {
	t.Helper()
	rendered = normalizeDeployment(rendered)
	golden = normalizeDeployment(golden)

	if diff := cmp.Diff(golden.Spec.Replicas, rendered.Spec.Replicas, quantityComparer); diff != "" {
		t.Errorf("replicas mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Strategy, rendered.Spec.Strategy, quantityComparer); diff != "" {
		t.Errorf("strategy mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Selector, rendered.Spec.Selector, quantityComparer); diff != "" {
		t.Errorf("selector mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Template.Labels, rendered.Spec.Template.Labels); diff != "" {
		t.Errorf("template labels mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Template.Annotations, rendered.Spec.Template.Annotations); diff != "" {
		t.Errorf("template annotations mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Template.Spec, rendered.Spec.Template.Spec, quantityComparer); diff != "" {
		t.Errorf("pod spec mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Name, rendered.Name); diff != "" {
		t.Errorf("name mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Labels, rendered.Labels); diff != "" {
		t.Errorf("labels mismatch (-golden +rendered):\n%s", diff)
	}
}

func assertServiceMatchesGolden(t *testing.T, rendered, golden *corev1.Service) {
	t.Helper()
	if diff := cmp.Diff(golden.Name, rendered.Name); diff != "" {
		t.Errorf("service name mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Selector, rendered.Spec.Selector); diff != "" {
		t.Errorf("service selector mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Ports, rendered.Spec.Ports); diff != "" {
		t.Errorf("service ports mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(golden.Spec.Type, rendered.Spec.Type); diff != "" {
		t.Errorf("service type mismatch (-golden +rendered):\n%s", diff)
	}
}

func TestRenderSmokeWorkloadMatchesGoldenManifest(t *testing.T) {
	// Arrange
	goldenDeployment, goldenService, goldenConfigMap := goldenObjects(t, smokeManifestPath)
	if goldenConfigMap != nil {
		t.Fatalf("smoke manifest unexpectedly contains a ConfigMap")
	}

	// Act
	rendered, err := render.Deployment(smokeService(), smokeModel(), testConfig())
	if err != nil {
		t.Fatalf("rendering deployment: %v", err)
	}
	renderedSvc := render.Service(smokeService())

	// Assert
	assertDeploymentMatchesGolden(t, rendered, goldenDeployment)
	assertServiceMatchesGolden(t, renderedSvc, goldenService)
}

func TestRenderProdWorkloadMatchesGoldenManifest(t *testing.T) {
	// Arrange
	goldenDeployment, goldenService, goldenConfigMap := goldenObjects(t, prodManifestPath)
	if goldenConfigMap == nil {
		t.Fatalf("prod manifest is missing the gai-ipv4-first ConfigMap")
	}

	// Act
	rendered, err := render.Deployment(prodService(), prodModel(), testConfig())
	if err != nil {
		t.Fatalf("rendering deployment: %v", err)
	}
	renderedSvc := render.Service(prodService())
	renderedCM := render.GaiConfigMap(testNamespace)

	// Assert
	assertDeploymentMatchesGolden(t, rendered, goldenDeployment)
	assertServiceMatchesGolden(t, renderedSvc, goldenService)
	if diff := cmp.Diff(goldenConfigMap.Name, renderedCM.Name); diff != "" {
		t.Errorf("configmap name mismatch (-golden +rendered):\n%s", diff)
	}
	if diff := cmp.Diff(goldenConfigMap.Data, renderedCM.Data); diff != "" {
		t.Errorf("configmap data mismatch (-golden +rendered):\n%s", diff)
	}
}

func TestRenderImageOverrideWinsOverDefault(t *testing.T) {
	// Arrange
	override := "docker.io/vllm/vllm-openai@sha256:" + strings.Repeat("ab", 32)
	isvc := smokeService()
	isvc.Spec.Engine.Image = override

	// Act
	rendered, err := render.Deployment(isvc, smokeModel(), testConfig())
	if err != nil {
		t.Fatalf("rendering deployment: %v", err)
	}

	// Assert
	if got := rendered.Spec.Template.Spec.Containers[0].Image; got != override {
		t.Errorf("image = %q, want override %q", got, override)
	}
}

func TestRenderProbeOverride(t *testing.T) {
	// Arrange
	threshold := int32(42)
	isvc := smokeService()
	isvc.Spec.Probes = &brukv1alpha1.ProbeOverrides{ReadinessFailureThreshold: &threshold}

	// Act
	rendered, err := render.Deployment(isvc, smokeModel(), testConfig())
	if err != nil {
		t.Fatalf("rendering deployment: %v", err)
	}

	// Assert
	if got := rendered.Spec.Template.Spec.Containers[0].ReadinessProbe.FailureThreshold; got != threshold {
		t.Errorf("failureThreshold = %d, want %d", got, threshold)
	}
}

func TestRenderRequestsDefaultToLimits(t *testing.T) {
	// Arrange
	isvc := smokeService()
	isvc.Spec.Resources.Memory.Request = nil
	isvc.Spec.Resources.CPU.Request = nil

	// Act
	rendered, err := render.Deployment(isvc, smokeModel(), testConfig())
	if err != nil {
		t.Fatalf("rendering deployment: %v", err)
	}

	// Assert
	requests := rendered.Spec.Template.Spec.Containers[0].Resources.Requests
	if got := requests[corev1.ResourceMemory]; got.Cmp(resource.MustParse("32Gi")) != 0 {
		t.Errorf("memory request = %s, want limit 32Gi", got.String())
	}
	if got := requests[corev1.ResourceCPU]; got.Cmp(resource.MustParse("8")) != 0 {
		t.Errorf("cpu request = %s, want limit 8", got.String())
	}
}

func TestRenderRejectsMissingModelSource(t *testing.T) {
	// Arrange
	model := smokeModel()
	model.Spec.Source.HuggingFace = nil

	// Act
	_, err := render.Deployment(smokeService(), model, testConfig())

	// Assert
	if err == nil {
		t.Fatal("expected an error for a model without a source, got nil")
	}
}

func TestRenderRejectsEmptyImage(t *testing.T) {
	// Arrange: no override and no tenant default.
	cfg := render.Config{InitDataB64: testInitDataB64}

	// Act
	_, err := render.Deployment(smokeService(), smokeModel(), cfg)

	// Assert
	if err == nil {
		t.Fatal("expected an error when no image is resolvable, got nil")
	}
}
