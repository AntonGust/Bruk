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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

// BrukModelReconciler reconciles a BrukModel object
type BrukModelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=bruk.airon.ai,resources=brukmodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=bruk.airon.ai,resources=brukmodels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile validates a BrukModel and records the result as a Ready
// condition. Validation-only in v1alpha1: the workload belongs to
// InferenceService. The token-secret presence check exists because the
// alternative failure signal is a CC pod dying 20 minutes into start with
// empty logs.
func (r *BrukModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	model := &brukv1alpha1.BrukModel{}
	if err := r.Get(ctx, req.NamespacedName, model); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := metav1.ConditionTrue
	reason := brukv1alpha1.ReasonValid
	message := "model spec is valid"

	if ref := model.Spec.Source.HuggingFace.TokenSecretRef; ref != nil {
		secret := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: model.Namespace}, secret)
		switch {
		case apierrors.IsNotFound(err):
			status = metav1.ConditionFalse
			reason = brukv1alpha1.ReasonTokenSecretMissing
			message = fmt.Sprintf("token Secret %q not found in namespace %s (delivered out-of-band, never in Git)", ref.Name, model.Namespace)
		case err != nil:
			return ctrl.Result{}, err
		case len(secret.Data[ref.Key]) == 0:
			status = metav1.ConditionFalse
			reason = brukv1alpha1.ReasonTokenSecretMissing
			message = fmt.Sprintf("token Secret %q has no key %q", ref.Name, ref.Key)
		}
	}

	meta.SetStatusCondition(&model.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: model.Generation,
	})
	model.Status.ObservedGeneration = model.Generation
	return ctrl.Result{}, r.Status().Update(ctx, model)
}

// SetupWithManager sets up the controller with the Manager.
func (r *BrukModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&brukv1alpha1.BrukModel{}).
		Named("brukmodel").
		Complete(r)
}
