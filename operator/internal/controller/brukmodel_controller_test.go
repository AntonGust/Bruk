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
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

var _ = Describe("BrukModel Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
			gatedModelName    = "gated-model"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		brukmodel := &brukv1alpha1.BrukModel{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind BrukModel")
			err := k8sClient.Get(ctx, typeNamespacedName, brukmodel)
			if err != nil && errors.IsNotFound(err) {
				resource := &brukv1alpha1.BrukModel{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: validModelSpec(),
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &brukv1alpha1.BrukModel{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance BrukModel")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("marks an ungated model Ready", func() {
			By("Reconciling the created resource")
			controllerReconciler := &BrukModelReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			got := &brukv1alpha1.BrukModel{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())
			ready := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
			Expect(ready).To(HaveField("Status", metav1.ConditionTrue))
			Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		})

		It("warns (non-fatal) when the HuggingFace revision is unpinned", func() {
			// validModelSpec() sets no revision, so the sample is unpinned.
			controllerReconciler := &BrukModelReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			got := &brukv1alpha1.BrukModel{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())
			ready := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
			Expect(ready).To(HaveField("Status", metav1.ConditionTrue)) // non-fatal
			Expect(ready).To(HaveField("Reason", brukv1alpha1.ReasonUnpinnedRevision))
		})

		It("marks a pinned model Ready with reason Valid", func() {
			pinned := &brukv1alpha1.BrukModel{
				ObjectMeta: metav1.ObjectMeta{Name: pinnedModelName, Namespace: resourceNamespace},
				Spec:       validModelSpec(),
			}
			pinned.Spec.Source.HuggingFace.Revision = "e0bcfd9c94b0d3d1e8b0a3d5b0e0e0e0e0e0e0e0"
			Expect(k8sClient.Create(ctx, pinned)).To(Succeed())

			controllerReconciler := &BrukModelReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: pinnedModelName, Namespace: resourceNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			got := &brukv1alpha1.BrukModel{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pinnedModelName, Namespace: resourceNamespace}, got)).To(Succeed())
			ready := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
			Expect(ready).To(HaveField("Reason", brukv1alpha1.ReasonValid))
		})

		It("marks a gated model with a missing token secret not Ready", func() {
			gated := &brukv1alpha1.BrukModel{
				ObjectMeta: metav1.ObjectMeta{Name: gatedModelName, Namespace: resourceNamespace},
				Spec:       validModelSpec(),
			}
			gated.Spec.Source.HuggingFace.TokenSecretRef = &brukv1alpha1.SecretKeyRef{
				Name: "no-such-secret", Key: secretTokenKey,
			}
			Expect(k8sClient.Create(ctx, gated)).To(Succeed())

			controllerReconciler := &BrukModelReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: gatedModelName, Namespace: resourceNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			got := &brukv1alpha1.BrukModel{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gatedModelName, Namespace: resourceNamespace}, got)).To(Succeed())
			ready := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
			Expect(ready).To(HaveField("Status", metav1.ConditionFalse))
			Expect(ready).To(HaveField("Reason", brukv1alpha1.ReasonTokenSecretMissing))
		})
	})
})
