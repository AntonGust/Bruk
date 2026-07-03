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

// Package lockstep holds repo-consistency tests: invariants that span the
// operator and the hand-written manifests it must stay in lockstep with.
package lockstep

import (
	"os"
	"regexp"
	"testing"

	"sigs.k8s.io/yaml"

	v1alpha1 "github.com/AntonGust/Bruk/operator/api/v1alpha1"
)

const (
	seedJobPath      = "../../../manifests/registry/seed-job.yaml"
	tenantSamplePath = "../../config/samples/bruk_v1alpha1_bruktenant.yaml"
)

var imageDigestRe = regexp.MustCompile(`docker\.io/vllm/vllm-openai@(sha256:[a-f0-9]{64})`)

// The registry mirror only serves digests the seed job copied, and image-rs
// verifies pulls by digest — so the sample BrukTenant's defaultImage must
// reference exactly the seeded digest or CC guests cannot pull it.
func TestDefaultImageMatchesMirrorSeed(t *testing.T) {
	// Arrange
	seedRaw, err := os.ReadFile(seedJobPath)
	if err != nil {
		t.Fatalf("reading seed job manifest: %v", err)
	}
	seedMatch := imageDigestRe.FindSubmatch(seedRaw)
	if seedMatch == nil {
		t.Fatalf("no vLLM image digest found in %s", seedJobPath)
	}
	seededDigest := string(seedMatch[1])

	sampleRaw, err := os.ReadFile(tenantSamplePath)
	if err != nil {
		t.Fatalf("reading BrukTenant sample: %v", err)
	}
	var tenant v1alpha1.BrukTenant
	if err := yaml.UnmarshalStrict(sampleRaw, &tenant); err != nil {
		t.Fatalf("decoding BrukTenant sample: %v", err)
	}

	// Act
	sampleMatch := imageDigestRe.FindStringSubmatch(tenant.Spec.Engine.DefaultImage)

	// Assert
	if sampleMatch == nil {
		t.Fatalf("sample defaultImage %q is not a digest-pinned vLLM ref", tenant.Spec.Engine.DefaultImage)
	}
	if sampleMatch[1] != seededDigest {
		t.Errorf("defaultImage digest %s does not match mirror seed digest %s — update both together (seed-job.yaml + BrukTenant)",
			sampleMatch[1], seededDigest)
	}
}
