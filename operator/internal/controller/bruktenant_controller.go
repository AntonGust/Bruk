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
	"encoding/base64"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
	"github.com/AntonGust/Bruk/operator/internal/render"
)

// BrukTenantReconciler reconciles a BrukTenant object
type BrukTenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=bruk.airon.ai,resources=bruktenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bruk.airon.ai,resources=bruktenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bruk.airon.ai,resources=bruktenants/finalizers,verbs=update

// Reconcile validates the cluster contract and records the result. It renders
// no workloads in v1alpha1 — its value is observability: a stale or malformed
// initdata blob is the one misconfiguration that rolls every CC pod on the
// cluster, so `kubectl get bt` must answer "is the cluster config sane".
func (r *BrukTenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	tenant := &brukv1alpha1.BrukTenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := metav1.ConditionTrue
	reason := brukv1alpha1.ReasonValid
	message := "cluster contract is valid"
	if err := validateInitData(tenant.Spec.Confidential.InitDataB64); err != nil {
		status = metav1.ConditionFalse
		reason = brukv1alpha1.ReasonInvalidConfig
		message = fmt.Sprintf("initDataB64 invalid: %v (expected base64(gzip(initdata.toml)) from build-initdata.sh; an unsubstituted ${INITDATA_B64} placeholder fails CRD validation earlier)", err)
	}

	meta.SetStatusCondition(&tenant.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: tenant.Generation,
	})
	tenant.Status.AppliedInitDataHash = render.InitDataHash(tenant.Spec.Confidential.InitDataB64)
	tenant.Status.ObservedGeneration = tenant.Generation
	return ctrl.Result{}, r.Status().Update(ctx, tenant)
}

// validateInitData checks the blob is base64-decodable gzip — the format the
// kata-agent requires (plain base64 of the TOML fails in-guest with a gzip
// header error, 20 minutes into a pod start, with empty logs).
func validateInitData(blob string) error {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return fmt.Errorf("not valid base64: %w", err)
	}
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return fmt.Errorf("decoded payload is not gzip (missing 1f 8b magic)")
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BrukTenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&brukv1alpha1.BrukTenant{}).
		Named("bruktenant").
		Complete(r)
}
