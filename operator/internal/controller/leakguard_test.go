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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

// Leak-guard: locks in the audited invariant (ADR-0008) that sensitive
// material never reaches an observable surface. The HF token and the full
// initdata blob must appear in NO status condition (message or reason) on any
// CR — status carries only a 12-char hash of the blob, and the token is only
// ever a name+key reference.
const (
	sentinelToken = "SENTINEL-HF-TOKEN-do-not-leak-9f3a2b"
)

func allConditionText(conds []metav1.Condition) string {
	var b strings.Builder
	for _, c := range conds {
		b.WriteString(c.Reason)
		b.WriteString("\n")
		b.WriteString(c.Message)
		b.WriteString("\n")
	}
	return b.String()
}

var _ = Describe("Leak-guard: no secret/blob in status", func() {
	ctx := context.Background()

	It("keeps the initdata blob out of BrukTenant and InferenceService status (hash only)", func() {
		ensureTenant(ctx)
		ensureModel(ctx, "model-leak")
		createISVC(ctx, "isvc-leak", "model-leak", "claim-leak")

		_, err := (&BrukTenantReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}).Reconcile(ctx,
			reconcile.Request{NamespacedName: types.NamespacedName{Name: brukv1alpha1.BrukTenantName}})
		Expect(err).NotTo(HaveOccurred())
		Expect(reconcileISVC(ctx, "isvc-leak")).To(Succeed())

		tenant := &brukv1alpha1.BrukTenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: brukv1alpha1.BrukTenantName}, tenant)).To(Succeed())
		tenantText := allConditionText(tenant.Status.Conditions)
		Expect(tenantText).NotTo(ContainSubstring(testInitDataB64), "raw initdata blob leaked into BrukTenant status")
		Expect(tenant.Status.AppliedInitDataHash).NotTo(Equal(testInitDataB64))
		Expect(tenant.Status.AppliedInitDataHash).To(HaveLen(12))

		isvc := fetchISVC(ctx, "isvc-leak")
		isvcText := allConditionText(isvc.Status.Conditions)
		Expect(isvcText).NotTo(ContainSubstring(testInitDataB64), "raw initdata blob leaked into InferenceService status")
	})

	It("keeps the HF token value out of BrukModel status", func() {
		By("creating a Secret whose token value is a recognizable sentinel")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "hf-token-leak", Namespace: testNS},
			Data:       map[string][]byte{secretTokenKey: []byte(sentinelToken)},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		model := &brukv1alpha1.BrukModel{
			ObjectMeta: metav1.ObjectMeta{Name: tokenLeakModelName, Namespace: testNS},
			Spec:       validModelSpec(),
		}
		model.Spec.Source.HuggingFace.TokenSecretRef = &brukv1alpha1.SecretKeyRef{Name: "hf-token-leak", Key: secretTokenKey}
		Expect(k8sClient.Create(ctx, model)).To(Succeed())

		reconciler := &BrukModelReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: tokenLeakModelName, Namespace: testNS},
		})
		Expect(err).NotTo(HaveOccurred())

		got := &brukv1alpha1.BrukModel{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tokenLeakModelName, Namespace: testNS}, got)).To(Succeed())
		Expect(allConditionText(got.Status.Conditions)).NotTo(ContainSubstring(sentinelToken), "HF token value leaked into BrukModel status")
	})
})
