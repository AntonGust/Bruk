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

package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServedModelNameDefaultsToMetadataName(t *testing.T) {
	// Arrange
	model := &BrukModel{ObjectMeta: metav1.ObjectMeta{Name: "qwen-0.5b"}}

	// Act + Assert
	if got := model.ServedModelName(); got != "qwen-0.5b" {
		t.Errorf("ServedModelName() = %q, want metadata.name %q", got, "qwen-0.5b")
	}
}

func TestServedModelNameUsesExplicitSpecValue(t *testing.T) {
	// Arrange
	model := &BrukModel{
		ObjectMeta: metav1.ObjectMeta{Name: "mistral-small-3.1-24b"},
		Spec:       BrukModelSpec{ServedName: "mistral-small-3.1"},
	}

	// Act + Assert
	if got := model.ServedModelName(); got != "mistral-small-3.1" {
		t.Errorf("ServedModelName() = %q, want spec.servedName %q", got, "mistral-small-3.1")
	}
}
