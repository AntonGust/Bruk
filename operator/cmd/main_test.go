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

package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestClientOptionsDisablesSecretCache locks the fix for the get-only-RBAC
// caching bug: with least-privilege RBAC (secrets: get only, ADR-0008), the
// operator must NOT cache Secrets, or controller-runtime serves Get(secret) via
// a list+watch informer that RBAC forbids — which breaks gated BrukModel
// reconciles (tokenSecretRef). If a refactor drops this, the test fails before
// it ships.
func TestClientOptionsDisablesSecretCache(t *testing.T) {
	opts := clientOptions()

	if opts.Cache == nil {
		t.Fatal("clientOptions().Cache is nil; expected Secret excluded from cache")
	}

	found := false
	for _, obj := range opts.Cache.DisableFor {
		if _, ok := obj.(*corev1.Secret); ok {
			found = true
			break
		}
	}
	if !found {
		t.Error("Secret must be in Cache.DisableFor so Get(secret) bypasses the informer (get-only RBAC, ADR-0008)")
	}
}
