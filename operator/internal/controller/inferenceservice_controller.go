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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
	"github.com/AntonGust/Bruk/operator/internal/render"
)

// fieldOwner is the server-side-apply field manager for operator-rendered objects.
const fieldOwner = client.FieldOwner("airon-operator")

// InferenceServiceReconciler reconciles a InferenceService object
type InferenceServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=bruk.airon.ai,resources=inferenceservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bruk.airon.ai,resources=inferenceservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bruk.airon.ai,resources=inferenceservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=bruk.airon.ai,resources=brukmodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=bruk.airon.ai,resources=bruktenants,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile renders the confidential vLLM workload for an InferenceService and
// keeps status honest. Failure modes set Configured=False with a specific
// reason and deliberately do NOT touch existing children: a transiently
// missing tenant config must never tear down a serving CC pod.
func (r *InferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	isvc := &brukv1alpha1.InferenceService{}
	if err := r.Get(ctx, req.NamespacedName, isvc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	tenant := &brukv1alpha1.BrukTenant{}
	if err := r.Get(ctx, types.NamespacedName{Name: brukv1alpha1.BrukTenantName}, tenant); err != nil {
		if apierrors.IsNotFound(err) {
			return r.failConfigured(ctx, isvc, brukv1alpha1.ReasonTenantConfigMissing,
				"BrukTenant 'cluster' not found; per-cluster config (initdata, default image) unavailable")
		}
		return ctrl.Result{}, err
	}

	model := &brukv1alpha1.BrukModel{}
	if err := r.Get(ctx, types.NamespacedName{Name: isvc.Spec.ModelRef.Name, Namespace: isvc.Namespace}, model); err != nil {
		if apierrors.IsNotFound(err) {
			return r.failConfigured(ctx, isvc, brukv1alpha1.ReasonModelNotFound,
				fmt.Sprintf("BrukModel %q not found in namespace %s", isvc.Spec.ModelRef.Name, isvc.Namespace))
		}
		return ctrl.Result{}, err
	}

	if isvc.Spec.Storage.TrustedStore.LocalVolume != nil {
		return r.failConfigured(ctx, isvc, brukv1alpha1.ReasonNotImplemented,
			"storage.trustedStore.localVolume is reserved; use existingClaim in v1alpha1")
	}
	if model.Spec.Source.HuggingFace == nil {
		return r.failConfigured(ctx, isvc, brukv1alpha1.ReasonInvalidConfig,
			fmt.Sprintf("BrukModel %q has no huggingFace source", model.Name))
	}
	if isvc.Spec.Engine.MaxModelLen > model.Spec.Catalog.ContextLength {
		return r.failConfigured(ctx, isvc, brukv1alpha1.ReasonInvalidConfig,
			fmt.Sprintf("engine.maxModelLen %d exceeds the model's native contextLength %d",
				isvc.Spec.Engine.MaxModelLen, model.Spec.Catalog.ContextLength))
	}

	if conflictMsg, err := r.trustedStoreConflict(ctx, isvc); err != nil {
		return ctrl.Result{}, err
	} else if conflictMsg != "" {
		return r.failConfigured(ctx, isvc, brukv1alpha1.ReasonTrustedStoreConflict, conflictMsg)
	}

	cfg := render.Config{
		InitDataB64:  tenant.Spec.Confidential.InitDataB64,
		DefaultImage: tenant.Spec.Engine.DefaultImage,
	}

	if isvc.Spec.Storage.WeightsCache != nil {
		// Shared per-namespace object: no ownerRef, so deleting one
		// InferenceService cannot garbage-collect it out from under another.
		configMap := render.GaiConfigMap(isvc.Namespace)
		configMap.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
		if err := r.Patch(ctx, configMap, client.Apply, fieldOwner, client.ForceOwnership); err != nil { //nolint:staticcheck // SSA on typed render output; apply-configuration migration is deliberate follow-up work
			return ctrl.Result{}, fmt.Errorf("applying gai ConfigMap: %w", err)
		}
	}

	deployment, err := render.Deployment(isvc, model, cfg)
	if err != nil {
		return r.failConfigured(ctx, isvc, brukv1alpha1.ReasonInvalidConfig, err.Error())
	}
	deployment.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
	if err := controllerutil.SetControllerReference(isvc, deployment, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Patch(ctx, deployment, client.Apply, fieldOwner, client.ForceOwnership); err != nil { //nolint:staticcheck // see ConfigMap apply above
		return ctrl.Result{}, fmt.Errorf("applying Deployment: %w", err)
	}

	service := render.Service(isvc)
	service.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Service"}
	if err := controllerutil.SetControllerReference(isvc, service, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Patch(ctx, service, client.Apply, fieldOwner, client.ForceOwnership); err != nil { //nolint:staticcheck // see ConfigMap apply above
		return ctrl.Result{}, fmt.Errorf("applying Service: %w", err)
	}

	log.Info("reconciled workload", "deployment", deployment.Name)
	return ctrl.Result{}, r.updateReadyStatus(ctx, isvc, model, deployment, cfg)
}

// trustedStoreConflict enforces one workload per trusted-store PVC (a second
// consumer would LUKS2-format the store again and corrupt it). A conflict
// exists when an OLDER sibling InferenceService references the same claim
// (creationTimestamp, then name, as tie-break) or when any Deployment or
// standalone Pod not owned by this CR mounts it — the latter protects the
// hand-written workloads during adoption. Returns a non-empty message on
// conflict.
func (r *InferenceServiceReconciler) trustedStoreConflict(ctx context.Context, isvc *brukv1alpha1.InferenceService) (string, error) {
	claim := isvc.Spec.Storage.TrustedStore.ExistingClaim

	siblings := &brukv1alpha1.InferenceServiceList{}
	if err := r.List(ctx, siblings, client.InNamespace(isvc.Namespace)); err != nil {
		return "", err
	}
	for i := range siblings.Items {
		other := &siblings.Items[i]
		if other.Name == isvc.Name || other.Spec.Storage.TrustedStore.ExistingClaim != claim {
			continue
		}
		if precedes(other, isvc) {
			return fmt.Sprintf("InferenceService %q already claims trusted-store PVC %q (one workload per PVC)", other.Name, claim), nil
		}
	}

	deployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, deployments, client.InNamespace(isvc.Namespace)); err != nil {
		return "", err
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if metav1.IsControlledBy(deployment, isvc) {
			continue
		}
		if podSpecMountsClaim(&deployment.Spec.Template.Spec, claim) {
			return fmt.Sprintf("Deployment %q (not managed by this InferenceService) already mounts trusted-store PVC %q", deployment.Name, claim), nil
		}
	}

	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(isvc.Namespace)); err != nil {
		return "", err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		// Controller-owned pods are covered via their Deployment above;
		// only standalone pods can hold a claim invisibly.
		if len(pod.OwnerReferences) > 0 {
			continue
		}
		if podSpecMountsClaim(&pod.Spec, claim) {
			return fmt.Sprintf("standalone Pod %q already mounts trusted-store PVC %q", pod.Name, claim), nil
		}
	}
	return "", nil
}

// precedes reports whether a wins the claim over b: older creationTimestamp
// first, lexicographic name as the deterministic tie-break.
func precedes(a, b *brukv1alpha1.InferenceService) bool {
	if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
		return a.CreationTimestamp.Before(&b.CreationTimestamp)
	}
	return a.Name < b.Name
}

func podSpecMountsClaim(spec *corev1.PodSpec, claim string) bool {
	for _, volume := range spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == claim {
			return true
		}
	}
	return false
}

// failConfigured records a terminal-for-this-generation failure in status and
// stops without touching children.
func (r *InferenceServiceReconciler) failConfigured(ctx context.Context, isvc *brukv1alpha1.InferenceService, reason, message string) (ctrl.Result, error) {
	r.setCondition(isvc, brukv1alpha1.ConditionConfigured, metav1.ConditionFalse, reason, message)
	r.setCondition(isvc, brukv1alpha1.ConditionReady, metav1.ConditionFalse, reason, message)
	r.setCondition(isvc, brukv1alpha1.ConditionProgressing, metav1.ConditionFalse, reason, message)
	isvc.Status.ObservedGeneration = isvc.Generation
	return ctrl.Result{}, r.Status().Update(ctx, isvc)
}

// updateReadyStatus records the converged status after children were applied.
func (r *InferenceServiceReconciler) updateReadyStatus(ctx context.Context, isvc *brukv1alpha1.InferenceService,
	model *brukv1alpha1.BrukModel, deployment *appsv1.Deployment, cfg render.Config) error {
	r.setCondition(isvc, brukv1alpha1.ConditionConfigured, metav1.ConditionTrue,
		brukv1alpha1.ReasonConfigured, "workload rendered and applied")

	available := deploymentAvailable(deployment)
	if available {
		r.setCondition(isvc, brukv1alpha1.ConditionWorkloadAvailable, metav1.ConditionTrue,
			brukv1alpha1.ReasonWorkloadReady, "Deployment is Available")
		r.setCondition(isvc, brukv1alpha1.ConditionReady, metav1.ConditionTrue,
			brukv1alpha1.ReasonWorkloadReady, "workload is serving")
		r.setCondition(isvc, brukv1alpha1.ConditionProgressing, metav1.ConditionFalse,
			brukv1alpha1.ReasonWorkloadReady, "rollout complete")
	} else {
		message := "Deployment not yet Available (CC guest boot + guest-pull can take 15-25 min)"
		r.setCondition(isvc, brukv1alpha1.ConditionWorkloadAvailable, metav1.ConditionFalse,
			brukv1alpha1.ReasonWorkloadPending, message)
		r.setCondition(isvc, brukv1alpha1.ConditionReady, metav1.ConditionFalse,
			brukv1alpha1.ReasonWorkloadPending, message)
		r.setCondition(isvc, brukv1alpha1.ConditionProgressing, metav1.ConditionTrue,
			brukv1alpha1.ReasonDeploying, message)
	}

	resolvedImage, err := render.ResolveImage(isvc, cfg)
	if err != nil {
		// Unreachable after a successful render; keep status best-effort.
		resolvedImage = ""
	}
	isvc.Status.Endpoint = &brukv1alpha1.EndpointStatus{URL: render.EndpointURL(isvc)}
	isvc.Status.ServedModelName = model.ServedModelName()
	isvc.Status.ResolvedImage = resolvedImage
	isvc.Status.AppliedInitDataHash = render.InitDataHash(cfg.InitDataB64)
	isvc.Status.ObservedGeneration = isvc.Generation
	return r.Status().Update(ctx, isvc)
}

func (r *InferenceServiceReconciler) setCondition(isvc *brukv1alpha1.InferenceService, condType string,
	status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&isvc.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: isvc.Generation,
	})
}

func deploymentAvailable(deployment *appsv1.Deployment) bool {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *InferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&brukv1alpha1.InferenceService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Watches(&brukv1alpha1.BrukModel{}, handler.EnqueueRequestsFromMapFunc(r.servicesForModel)).
		Watches(&brukv1alpha1.BrukTenant{}, handler.EnqueueRequestsFromMapFunc(r.allServices)).
		Named("inferenceservice").
		Complete(r)
}

// servicesForModel re-queues every InferenceService referencing a changed BrukModel.
func (r *InferenceServiceReconciler) servicesForModel(ctx context.Context, obj client.Object) []ctrl.Request {
	services := &brukv1alpha1.InferenceServiceList{}
	if err := r.List(ctx, services, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var requests []ctrl.Request
	for i := range services.Items {
		if services.Items[i].Spec.ModelRef.Name == obj.GetName() {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&services.Items[i]),
			})
		}
	}
	return requests
}

// allServices re-queues every InferenceService (tenant config affects all —
// an initdata change rolls every CC pod, and status must explain that).
func (r *InferenceServiceReconciler) allServices(ctx context.Context, _ client.Object) []ctrl.Request {
	services := &brukv1alpha1.InferenceServiceList{}
	if err := r.List(ctx, services); err != nil {
		return nil
	}
	var requests []ctrl.Request
	for i := range services.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: client.ObjectKeyFromObject(&services.Items[i]),
		})
	}
	return requests
}
