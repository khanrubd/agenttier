/*
Copyright 2024 AgentTier Authors.

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

package router

import (
	"testing"
	"time"
)

// TestParseDuration guards the bug where parseDuration always returned an
// error, silently dropping user-supplied timeout / idleTimeout on create.
func TestParseDuration(t *testing.T) {
	valid := map[string]time.Duration{
		"30m": 30 * time.Minute,
		"8h":  8 * time.Hour,
		"24h": 24 * time.Hour,
		"90s": 90 * time.Second,
	}
	for in, want := range valid {
		d, err := parseDuration(in)
		if err != nil {
			t.Fatalf("parseDuration(%q) returned error: %v", in, err)
		}
		if d == nil || d.Duration != want {
			t.Errorf("parseDuration(%q) = %v, want %v", in, d, want)
		}
	}

	invalid := []string{"", "notaduration", "0s", "-5m", "10"}
	for _, in := range invalid {
		if _, err := parseDuration(in); err == nil {
			t.Errorf("parseDuration(%q) expected an error, got nil", in)
		}
	}
}
