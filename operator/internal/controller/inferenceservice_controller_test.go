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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
	"github.com/AntonGust/Bruk/operator/internal/render"
)

// Envtest notes: there is no kube-controller-manager, so garbage collection
// never fires (assert ownerRefs, not cascade deletion) and Deployments never
// become Available on their own (drive status by hand). CC pod logs are empty
// in production, so everything asserted here must be visible in status.

const (
	testNS           = "default"
	legacyDeployName = "legacy-workload"
)

func newISVCReconciler() *InferenceServiceReconciler {
	return &InferenceServiceReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
}

func reconcileISVC(ctx context.Context, name string) error {
	_, err := newISVCReconciler().Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: testNS},
	})
	return err
}

func ensureTenant(ctx context.Context) {
	tenant := &brukv1alpha1.BrukTenant{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: brukv1alpha1.BrukTenantName}, tenant)
	if apierrors.IsNotFound(err) {
		Expect(k8sClient.Create(ctx, validTenant())).To(Succeed())
		return
	}
	Expect(err).NotTo(HaveOccurred())
}

func ensureModel(ctx context.Context, name string) {
	model := &brukv1alpha1.BrukModel{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, model)
	if apierrors.IsNotFound(err) {
		model = &brukv1alpha1.BrukModel{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
			Spec:       validModelSpec(),
		}
		Expect(k8sClient.Create(ctx, model)).To(Succeed())
		return
	}
	Expect(err).NotTo(HaveOccurred())
}

func createISVC(ctx context.Context, name, modelName, claim string) *brukv1alpha1.InferenceService {
	isvc := &brukv1alpha1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       validInferenceServiceSpec(modelName),
	}
	isvc.Spec.Storage.TrustedStore.ExistingClaim = claim
	Expect(k8sClient.Create(ctx, isvc)).To(Succeed())
	return isvc
}

func fetchISVC(ctx context.Context, name string) *brukv1alpha1.InferenceService {
	isvc := &brukv1alpha1.InferenceService{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, isvc)).To(Succeed())
	return isvc
}

func condition(isvc *brukv1alpha1.InferenceService, condType string) *metav1.Condition {
	return meta.FindStatusCondition(isvc.Status.Conditions, condType)
}

var _ = Describe("InferenceService Controller", func() {
	ctx := context.Background()

	BeforeEach(func() {
		ensureTenant(ctx)
	})

	Context("happy path (smoke workload shape)", func() {
		const name = "isvc-happy"

		It("creates children matching render output and reports status", func() {
			ensureModel(ctx, "model-happy")
			isvc := createISVC(ctx, name, "model-happy", "claim-happy")
			Expect(reconcileISVC(ctx, name)).To(Succeed())

			By("creating the Deployment with the rendered spec")
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, deployment)).To(Succeed())
			container := deployment.Spec.Template.Spec.Containers[0]
			Expect(container.Image).To(Equal(testVLLMImage))
			Expect(container.Args).To(ContainElement("--model=Qwen/Qwen2.5-0.5B-Instruct"))
			Expect(container.Args).To(ContainElement("--served-model-name=model-happy"))
			Expect(deployment.Spec.Template.Spec.RuntimeClassName).To(HaveValue(Equal(render.RuntimeClassName)))
			Expect(deployment.Spec.Template.Annotations).To(HaveKeyWithValue(render.InitDataAnnotation, testInitDataB64))
			Expect(deployment.Spec.Strategy.Type).To(Equal(appsv1.RecreateDeploymentStrategyType))

			By("setting the controller ownerRef on both children")
			Expect(metav1.IsControlledBy(deployment, isvc)).To(BeTrue())
			service := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name + "-svc", Namespace: testNS}, service)).To(Succeed())
			Expect(metav1.IsControlledBy(service, isvc)).To(BeTrue())
			Expect(service.Spec.Selector).To(HaveKeyWithValue("app", name))

			By("reporting Configured=True but Ready=False while the workload is pending")
			got := fetchISVC(ctx, name)
			Expect(condition(got, brukv1alpha1.ConditionConfigured)).To(HaveField("Status", metav1.ConditionTrue))
			Expect(condition(got, brukv1alpha1.ConditionWorkloadAvailable)).To(HaveField("Status", metav1.ConditionFalse))
			Expect(condition(got, brukv1alpha1.ConditionReady)).To(HaveField("Status", metav1.ConditionFalse))
			Expect(condition(got, brukv1alpha1.ConditionProgressing)).To(HaveField("Status", metav1.ConditionTrue))

			By("populating the status the catalog and humans read")
			Expect(got.Status.Endpoint).NotTo(BeNil())
			Expect(got.Status.Endpoint.URL).To(Equal("http://" + name + "-svc." + testNS + ".svc.cluster.local:8000/v1"))
			Expect(got.Status.ServedModelName).To(Equal("model-happy"))
			Expect(got.Status.ResolvedImage).To(Equal(testVLLMImage))
			Expect(got.Status.AppliedInitDataHash).NotTo(BeEmpty())
			Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		})

		It("becomes Ready when the Deployment reports Available", func() {
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, deployment)).To(Succeed())
			deployment.Status.Conditions = []appsv1.DeploymentCondition{{
				Type:   appsv1.DeploymentAvailable,
				Status: corev1.ConditionTrue,
				Reason: "MinimumReplicasAvailable",
			}}
			deployment.Status.AvailableReplicas = 1
			deployment.Status.ReadyReplicas = 1
			deployment.Status.UpdatedReplicas = 1
			deployment.Status.Replicas = 1
			Expect(k8sClient.Status().Update(ctx, deployment)).To(Succeed())

			Expect(reconcileISVC(ctx, name)).To(Succeed())
			got := fetchISVC(ctx, name)
			Expect(condition(got, brukv1alpha1.ConditionWorkloadAvailable)).To(HaveField("Status", metav1.ConditionTrue))
			Expect(condition(got, brukv1alpha1.ConditionReady)).To(HaveField("Status", metav1.ConditionTrue))
			Expect(condition(got, brukv1alpha1.ConditionProgressing)).To(HaveField("Status", metav1.ConditionFalse))
		})

		It("repairs drift on the Deployment (server-side apply)", func() {
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, deployment)).To(Succeed())
			deployment.Spec.Template.Spec.Containers[0].Image = "docker.io/evil/image:latest"
			Expect(k8sClient.Update(ctx, deployment)).To(Succeed())

			Expect(reconcileISVC(ctx, name)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(testVLLMImage))
		})
	})

	Context("weights cache", func() {
		It("creates the gai-ipv4-first ConfigMap alongside the workload", func() {
			ensureModel(ctx, "model-weights")
			isvc := &brukv1alpha1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{Name: "isvc-weights", Namespace: testNS},
				Spec:       validInferenceServiceSpec("model-weights"),
			}
			isvc.Spec.Storage.TrustedStore.ExistingClaim = "claim-weights"
			isvc.Spec.Storage.WeightsCache = &brukv1alpha1.WeightsCacheSpec{}
			Expect(k8sClient.Create(ctx, isvc)).To(Succeed())

			Expect(reconcileISVC(ctx, "isvc-weights")).To(Succeed())

			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: render.GaiConfigMapName, Namespace: testNS}, configMap)).To(Succeed())
			Expect(configMap.Data).To(HaveKey("gai.conf"))
		})
	})

	Context("resource caps (CEL, admission-time)", func() {
		It("rejects a memory.limit above the ceiling at admission", func() {
			ensureModel(ctx, "model-bigmem")
			isvc := &brukv1alpha1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{Name: "isvc-bigmem", Namespace: testNS},
				Spec:       validInferenceServiceSpec("model-bigmem"),
			}
			isvc.Spec.Storage.TrustedStore.ExistingClaim = "claim-bigmem"
			isvc.Spec.Resources.Memory.Limit = mustQ("1000Gi") // over the 512Gi ceiling
			err := k8sClient.Create(ctx, isvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("memory.limit must not exceed"))
		})

		It("rejects a cpu.limit above the ceiling at admission", func() {
			ensureModel(ctx, "model-bigcpu")
			isvc := &brukv1alpha1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{Name: "isvc-bigcpu", Namespace: testNS},
				Spec:       validInferenceServiceSpec("model-bigcpu"),
			}
			isvc.Spec.Storage.TrustedStore.ExistingClaim = "claim-bigcpu"
			isvc.Spec.Resources.CPU.Limit = mustQ("256") // over the 128 ceiling
			err := k8sClient.Create(ctx, isvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cpu.limit must not exceed"))
		})
	})

	Context("failure modes", func() {
		It("reports TenantConfigMissing without touching children when the tenant is absent", func() {
			ensureModel(ctx, "model-notenant")
			createISVC(ctx, "isvc-notenant", "model-notenant", "claim-notenant")

			tenant := &brukv1alpha1.BrukTenant{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: brukv1alpha1.BrukTenantName}, tenant)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())
			DeferCleanup(func() { ensureTenant(ctx) })

			Expect(reconcileISVC(ctx, "isvc-notenant")).To(Succeed())

			got := fetchISVC(ctx, "isvc-notenant")
			ready := condition(got, brukv1alpha1.ConditionReady)
			Expect(ready).To(HaveField("Status", metav1.ConditionFalse))
			Expect(condition(got, brukv1alpha1.ConditionConfigured)).To(HaveField("Reason", brukv1alpha1.ReasonTenantConfigMissing))

			deployment := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "isvc-notenant", Namespace: testNS}, deployment)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("reports ModelNotFound for a dangling modelRef", func() {
			createISVC(ctx, "isvc-nomodel", "model-that-does-not-exist", "claim-nomodel")
			Expect(reconcileISVC(ctx, "isvc-nomodel")).To(Succeed())

			got := fetchISVC(ctx, "isvc-nomodel")
			Expect(condition(got, brukv1alpha1.ConditionConfigured)).To(HaveField("Reason", brukv1alpha1.ReasonModelNotFound))
		})

		It("reports InvalidConfig when maxModelLen exceeds the catalog contextLength", func() {
			ensureModel(ctx, "model-ctx")
			isvc := &brukv1alpha1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{Name: "isvc-ctx", Namespace: testNS},
				Spec:       validInferenceServiceSpec("model-ctx"),
			}
			isvc.Spec.Storage.TrustedStore.ExistingClaim = "claim-ctx"
			isvc.Spec.Engine.MaxModelLen = 1 << 20 // far beyond the fixture's 32768
			Expect(k8sClient.Create(ctx, isvc)).To(Succeed())

			Expect(reconcileISVC(ctx, "isvc-ctx")).To(Succeed())
			got := fetchISVC(ctx, "isvc-ctx")
			Expect(condition(got, brukv1alpha1.ConditionConfigured)).To(HaveField("Reason", brukv1alpha1.ReasonInvalidConfig))
		})

		It("reports NotImplemented for the reserved localVolume variant", func() {
			ensureModel(ctx, "model-lv")
			isvc := &brukv1alpha1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{Name: "isvc-lv", Namespace: testNS},
				Spec:       validInferenceServiceSpec("model-lv"),
			}
			isvc.Spec.Storage.TrustedStore = brukv1alpha1.TrustedStoreSpec{
				LocalVolume: &brukv1alpha1.LocalVolumeSpec{LVName: "lv-test", Size: mustQ("10Gi")},
			}
			Expect(k8sClient.Create(ctx, isvc)).To(Succeed())

			Expect(reconcileISVC(ctx, "isvc-lv")).To(Succeed())
			got := fetchISVC(ctx, "isvc-lv")
			Expect(condition(got, brukv1alpha1.ConditionConfigured)).To(HaveField("Reason", brukv1alpha1.ReasonNotImplemented))
		})
	})

	Context("watch mappings", func() {
		It("re-queues only the InferenceServices referencing a changed BrukModel", func() {
			ensureModel(ctx, "model-watched")
			ensureModel(ctx, "model-unrelated")
			createISVC(ctx, "isvc-watch-a", "model-watched", "claim-watch-a")
			createISVC(ctx, "isvc-watch-b", "model-unrelated", "claim-watch-b")

			model := &brukv1alpha1.BrukModel{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "model-watched", Namespace: testNS}, model)).To(Succeed())

			requests := newISVCReconciler().servicesForModel(ctx, model)
			names := make([]string, 0, len(requests))
			for _, request := range requests {
				names = append(names, request.Name)
			}
			Expect(names).To(ContainElement("isvc-watch-a"))
			Expect(names).NotTo(ContainElement("isvc-watch-b"))
		})

		It("re-queues every InferenceService when the tenant changes", func() {
			tenant := &brukv1alpha1.BrukTenant{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: brukv1alpha1.BrukTenantName}, tenant)).To(Succeed())

			requests := newISVCReconciler().allServices(ctx, tenant)
			Expect(len(requests)).To(BeNumerically(">=", 2))
		})
	})

	Context("one-pod-per-PVC protection (LUKS double-format guard)", func() {
		It("lets the older CR keep the claim and marks the newer one TrustedStoreConflict", func() {
			ensureModel(ctx, "model-conflict")
			createISVC(ctx, "isvc-conflict-a", "model-conflict", "claim-shared")
			createISVC(ctx, "isvc-conflict-b", "model-conflict", "claim-shared")

			Expect(reconcileISVC(ctx, "isvc-conflict-a")).To(Succeed())
			Expect(reconcileISVC(ctx, "isvc-conflict-b")).To(Succeed())

			gotA := fetchISVC(ctx, "isvc-conflict-a")
			Expect(condition(gotA, brukv1alpha1.ConditionConfigured)).To(HaveField("Status", metav1.ConditionTrue))

			gotB := fetchISVC(ctx, "isvc-conflict-b")
			Expect(condition(gotB, brukv1alpha1.ConditionConfigured)).To(HaveField("Reason", brukv1alpha1.ReasonTrustedStoreConflict))

			deployment := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "isvc-conflict-b", Namespace: testNS}, deployment)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("detects standalone Pods mounting the claim", func() {
			ensureModel(ctx, "model-pod")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "standalone-probe", Namespace: testNS},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "probe", Image: "busybox:1.36"}},
					Volumes: []corev1.Volume{{
						Name: "trusted-image",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "claim-pod"},
						},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			createISVC(ctx, "isvc-pod", "model-pod", "claim-pod")
			Expect(reconcileISVC(ctx, "isvc-pod")).To(Succeed())

			got := fetchISVC(ctx, "isvc-pod")
			Expect(condition(got, brukv1alpha1.ConditionConfigured)).To(HaveField("Reason", brukv1alpha1.ReasonTrustedStoreConflict))
		})

		It("detects unmanaged Deployments mounting the claim (adoption safety)", func() {
			ensureModel(ctx, "model-legacy")
			legacy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: legacyDeployName, Namespace: testNS},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": legacyDeployName}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": legacyDeployName}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "vllm", Image: "docker.io/vllm/vllm-openai:v0.11.1"}},
							Volumes: []corev1.Volume{{
								Name: "trusted-image",
								VolumeSource: corev1.VolumeSource{
									PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "claim-legacy"},
								},
							}},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, legacy)).To(Succeed())

			createISVC(ctx, "isvc-legacy", "model-legacy", "claim-legacy")
			Expect(reconcileISVC(ctx, "isvc-legacy")).To(Succeed())

			got := fetchISVC(ctx, "isvc-legacy")
			Expect(condition(got, brukv1alpha1.ConditionConfigured)).To(HaveField("Reason", brukv1alpha1.ReasonTrustedStoreConflict))
		})
	})
})
