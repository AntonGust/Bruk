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

import "testing"

func TestValidateInitData(t *testing.T) {
	tests := []struct {
		name    string
		blob    string
		wantErr bool
	}{
		{
			name:    "valid base64(gzip(...)) blob",
			blob:    testInitDataB64,
			wantErr: false,
		},
		{
			name:    "base64 but not gzip (the classic plain-base64 mistake)",
			blob:    "bm90LWd6aXAtanVzdC1iYXNlNjQ=",
			wantErr: true,
		},
		{
			name:    "not base64 at all",
			blob:    "!!! definitely not base64 !!!",
			wantErr: true,
		},
		{
			name:    "empty",
			blob:    "",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInitData(tc.blob)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateInitData() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
