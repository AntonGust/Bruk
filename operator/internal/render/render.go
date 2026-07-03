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

// Package render turns Bruk CRs into the confidential vLLM workload objects.
// Pure functions, no client, no context: the golden tests in this package
// prove the output is semantically identical to the hand-written manifests
// (manifests/h100-vllm-cc-smoke.yaml, manifests/h100-vllm-cc.yaml).
package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	brukv1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

// Fixed platform boilerplate — deliberately NOT configurable in v1alpha1.
const (
	// RuntimeClassName selects the confidential guest + CC GPU runtime.
	// CC is a node-level, provisioning-time property, never a per-CR knob.
	RuntimeClassName = "kata-qemu-nvidia-gpu-snp"

	// InitDataAnnotation carries base64(gzip(initdata.toml)) into the guest:
	// mirror redirect + serial layer unpack (manifests/registry/initdata.toml).
	InitDataAnnotation = "io.katacontainers.config.hypervisor.cc_init_data"

	// TrustedStoreDevicePath is the magic path the kata-agent LUKS2-formats
	// in-guest and mounts over /run/kata-containers/image before guest-pull.
	TrustedStoreDevicePath = "/dev/trusted_store"

	// WeightsCacheMountPath is where vLLM caches Hugging Face downloads; the
	// backing emptyDir is block-encrypted by the SNP runtime's emptydir_mode.
	WeightsCacheMountPath = "/root/.cache/huggingface"

	// GaiConfigMapName holds the glibc IPv4-first override: the CC guest
	// resolves IPv6-first but has no v6 route, which kills HF downloads.
	GaiConfigMapName = "gai-ipv4-first"

	servingPort   = 8000
	containerName = "vllm"
	dshmSizeLimit = "8Gi"

	// Readiness failure thresholds: guest-pull alone (~15-25 min) vs
	// pull + weights download + quantize + CUDA graphs.
	defaultFailureThreshold                          = int32(120)
	weightsCacheFailureThreshold                     = int32(240)
	readinessInitialDelaySeconds                     = int32(60)
	readinessPeriodSeconds                           = int32(10)
	gpuResourceName              corev1.ResourceName = "nvidia.com/pgpu"
)

// Config carries the per-cluster inputs (from BrukTenant) the renderer needs.
type Config struct {
	// InitDataB64 is the cc_init_data annotation value.
	InitDataB64 string
	// DefaultImage is the digest-pinned engine image used when the
	// InferenceService does not override it.
	DefaultImage string
}

// ServiceName returns the Service name for an InferenceService.
func ServiceName(isvc *brukv1alpha1.InferenceService) string {
	return isvc.Name + "-svc"
}

// ResolveImage returns the digest-pinned image for the workload: the
// InferenceService override when set, the cluster default otherwise.
func ResolveImage(isvc *brukv1alpha1.InferenceService, cfg Config) (string, error) {
	if isvc.Spec.Engine.Image != "" {
		return isvc.Spec.Engine.Image, nil
	}
	if cfg.DefaultImage != "" {
		return cfg.DefaultImage, nil
	}
	return "", fmt.Errorf("no engine image: neither spec.engine.image nor the tenant defaultImage is set")
}

// Deployment renders the confidential vLLM Deployment for an InferenceService
// and its BrukModel.
func Deployment(isvc *brukv1alpha1.InferenceService, model *brukv1alpha1.BrukModel, cfg Config) (*appsv1.Deployment, error) {
	if model.Spec.Source.HuggingFace == nil {
		return nil, fmt.Errorf("BrukModel %s has no huggingFace source; v1alpha1 supports only huggingFace", model.Name)
	}
	image, err := ResolveImage(isvc, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.InitDataB64 == "" {
		return nil, fmt.Errorf("no initdata blob: refusing to render a CC pod without the mirror-redirect annotation")
	}

	hasWeightsCache := isvc.Spec.Storage.WeightsCache != nil
	labels := map[string]string{"app": isvc.Name}

	container := corev1.Container{
		Name:  containerName,
		Image: image,
		Args:  engineArgs(isvc, model),
		Ports: []corev1.ContainerPort{{ContainerPort: servingPort}},
		Env:   engineEnv(model),
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				gpuResourceName:       *resource.NewQuantity(int64(isvc.Spec.Resources.GPUs), resource.DecimalSI),
				corev1.ResourceMemory: isvc.Spec.Resources.Memory.Limit,
				corev1.ResourceCPU:    isvc.Spec.Resources.CPU.Limit,
			},
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: requestOrLimit(isvc.Spec.Resources.Memory),
				corev1.ResourceCPU:    requestOrLimit(isvc.Spec.Resources.CPU),
			},
		},
		VolumeMounts: volumeMounts(hasWeightsCache),
		VolumeDevices: []corev1.VolumeDevice{
			{Name: "trusted-image", DevicePath: TrustedStoreDevicePath},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt32(servingPort)},
			},
			InitialDelaySeconds: readinessInitialDelaySeconds,
			PeriodSeconds:       readinessPeriodSeconds,
			FailureThreshold:    failureThreshold(isvc),
		},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isvc.Name,
			Namespace: isvc.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrInt32(1),
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{InitDataAnnotation: cfg.InitDataB64},
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: ptrString(RuntimeClassName),
					Containers:       []corev1.Container{container},
					Volumes:          volumes(isvc, hasWeightsCache),
				},
			},
		},
	}, nil
}

// Service renders the ClusterIP Service in front of the workload.
func Service(isvc *brukv1alpha1.InferenceService) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceName(isvc),
			Namespace: isvc.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": isvc.Name},
			Ports: []corev1.ServicePort{
				{Port: servingPort, TargetPort: intstr.FromInt32(servingPort)},
			},
		},
	}
}

// GaiConfigMap renders the glibc IPv4-first override mounted into workloads
// that download weights from Hugging Face.
func GaiConfigMap(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GaiConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			"gai.conf": "precedence ::ffff:0:0/96 100\n",
		},
	}
}

// EndpointURL returns the cluster-internal OpenAI-compatible base URL.
func EndpointURL(isvc *brukv1alpha1.InferenceService) string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/v1", ServiceName(isvc), isvc.Namespace, servingPort)
}

// InitDataHash returns a short sha256 of the initdata blob so humans can
// correlate "why did my pods roll" across BrukTenant and InferenceService
// statuses without diffing multi-KB annotations.
func InitDataHash(initDataB64 string) string {
	sum := sha256.Sum256([]byte(initDataB64))
	return hex.EncodeToString(sum[:])[:12]
}

func engineArgs(isvc *brukv1alpha1.InferenceService, model *brukv1alpha1.BrukModel) []string {
	args := []string{
		"--model=" + model.Spec.Source.HuggingFace.Repo,
		"--served-model-name=" + model.ServedModelName(),
	}
	if isvc.Spec.Engine.Quantization != "" {
		args = append(args, "--quantization="+isvc.Spec.Engine.Quantization)
	}
	args = append(args,
		fmt.Sprintf("--max-model-len=%d", isvc.Spec.Engine.MaxModelLen),
		fmt.Sprintf("--gpu-memory-utilization=0.%02d", isvc.Spec.Engine.GPUMemoryUtilizationPercent),
		"--host=0.0.0.0",
		fmt.Sprintf("--port=%d", servingPort),
	)
	return args
}

func engineEnv(model *brukv1alpha1.BrukModel) []corev1.EnvVar {
	var env []corev1.EnvVar
	if ref := model.Spec.Source.HuggingFace.TokenSecretRef; ref != nil {
		env = append(env, corev1.EnvVar{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
					Key:                  ref.Key,
				},
			},
		})
	}
	env = append(env, corev1.EnvVar{Name: "VLLM_ATTENTION_BACKEND", Value: "FLASH_ATTN"})
	return env
}

func volumeMounts(hasWeightsCache bool) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{}
	if hasWeightsCache {
		mounts = append(mounts, corev1.VolumeMount{Name: "models", MountPath: WeightsCacheMountPath})
	}
	mounts = append(mounts, corev1.VolumeMount{Name: "dshm", MountPath: "/dev/shm"})
	if hasWeightsCache {
		mounts = append(mounts, corev1.VolumeMount{Name: "gai", MountPath: "/etc/gai.conf", SubPath: "gai.conf"})
	}
	return mounts
}

func volumes(isvc *brukv1alpha1.InferenceService, hasWeightsCache bool) []corev1.Volume {
	dshmLimit := resource.MustParse(dshmSizeLimit)
	volumes := []corev1.Volume{}
	if hasWeightsCache {
		// A non-Memory emptyDir: the SNP runtime's emptydir_mode makes it a
		// dm-crypt-encrypted disk image on host NVMe, keyed in-guest.
		volumes = append(volumes, corev1.Volume{
			Name: "models",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: isvc.Spec.Storage.WeightsCache.SizeLimit,
				},
			},
		})
	}
	volumes = append(volumes, corev1.Volume{
		Name: "dshm",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium:    corev1.StorageMediumMemory,
				SizeLimit: &dshmLimit,
			},
		},
	})
	if hasWeightsCache {
		volumes = append(volumes, corev1.Volume{
			Name: "gai",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: GaiConfigMapName},
				},
			},
		})
	}
	volumes = append(volumes, corev1.Volume{
		Name: "trusted-image",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: isvc.Spec.Storage.TrustedStore.ExistingClaim,
			},
		},
	})
	return volumes
}

func failureThreshold(isvc *brukv1alpha1.InferenceService) int32 {
	if isvc.Spec.Probes != nil && isvc.Spec.Probes.ReadinessFailureThreshold != nil {
		return *isvc.Spec.Probes.ReadinessFailureThreshold
	}
	if isvc.Spec.Storage.WeightsCache != nil {
		return weightsCacheFailureThreshold
	}
	return defaultFailureThreshold
}

func requestOrLimit(pair brukv1alpha1.ResourcePair) resource.Quantity {
	if pair.Request != nil {
		return *pair.Request
	}
	return pair.Limit
}

func ptrInt32(v int32) *int32    { return &v }
func ptrString(v string) *string { return &v }
