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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

var _ = Describe("BrukTenant Controller", func() {
	Context("When reconciling a resource", func() {
		// Cluster-scoped singleton: CEL enforces metadata.name == "cluster".
		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: brukv1alpha1.BrukTenantName,
		}
		bruktenant := &brukv1alpha1.BrukTenant{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind BrukTenant")
			err := k8sClient.Get(ctx, typeNamespacedName, bruktenant)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, validTenant())).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &brukv1alpha1.BrukTenant{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance BrukTenant")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("marks a valid cluster contract Ready and records the initdata hash", func() {
			By("Reconciling the created resource")
			controllerReconciler := &BrukTenantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			got := &brukv1alpha1.BrukTenant{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())
			ready := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
			Expect(ready).To(HaveField("Status", metav1.ConditionTrue))
			Expect(got.Status.AppliedInitDataHash).To(HaveLen(12))
			Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		})

		It("rejects a blob that is base64 but not gzip", func() {
			got := &brukv1alpha1.BrukTenant{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())
			got.Spec.Confidential.InitDataB64 = "bm90LWd6aXAtanVzdC1iYXNlNjQ="
			Expect(k8sClient.Update(ctx, got)).To(Succeed())

			controllerReconciler := &BrukTenantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())
			ready := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
			Expect(ready).To(HaveField("Status", metav1.ConditionFalse))
			Expect(ready).To(HaveField("Reason", brukv1alpha1.ReasonInvalidConfig))
		})
	})
})
