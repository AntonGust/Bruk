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
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

// TestReconcileNilHuggingFaceSource exercises the defense-in-depth guard for a
// BrukModel whose spec.source has no huggingFace member. The CRD CEL rule
// (has(self.huggingFace)) rejects this at admission, so it cannot be created
// through envtest — a fake client is used to inject the unvalidated object
// directly. The reconciler must report InvalidConfig, not panic.
func TestReconcileNilHuggingFaceSource(t *testing.T) {
	// Arrange
	scheme := runtime.NewScheme()
	if err := brukv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding scheme: %v", err)
	}

	model := &brukv1alpha1.BrukModel{
		ObjectMeta: metav1.ObjectMeta{Name: "sourceless", Namespace: "default"},
		Spec: brukv1alpha1.BrukModelSpec{
			Source: brukv1alpha1.ModelSource{}, // HuggingFace is nil
			Catalog: brukv1alpha1.CatalogSpec{
				DisplayName:   "sourceless",
				ContextLength: 4096,
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		WithStatusSubresource(&brukv1alpha1.BrukModel{}).
		Build()
	reconciler := &BrukModelReconciler{Client: c, Scheme: scheme}
	key := types.NamespacedName{Name: "sourceless", Namespace: "default"}

	// Act — must not panic on the nil huggingFace deref.
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})

	// Assert
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	got := &brukv1alpha1.BrukModel{}
	if err := c.Get(context.Background(), key, got); err != nil {
		t.Fatalf("fetching model: %v", err)
	}
	ready := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
	if ready == nil {
		t.Fatal("Ready condition not set")
	}
	if ready.Status != metav1.ConditionFalse {
		t.Errorf("Ready status = %q, want False", ready.Status)
	}
	if ready.Reason != brukv1alpha1.ReasonInvalidConfig {
		t.Errorf("Ready reason = %q, want %q", ready.Reason, brukv1alpha1.ReasonInvalidConfig)
	}
}
