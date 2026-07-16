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

package version

import "testing"

// TestDefaults guards the fallback values seen by any binary that is not
// built with the -ldflags that stamp in the real Version/GitCommit, e.g.
// `go test`, `go run`, or `go build` without the Makefile's ldflags.
func TestDefaults(t *testing.T) {
	if Version != "dev" {
		t.Errorf("Version = %q, want %q", Version, "dev")
	}
	if GitCommit != "unknown" {
		t.Errorf("GitCommit = %q, want %q", GitCommit, "unknown")
	}
}

// TestOverridable guards that Version and GitCommit remain plain package
// variables (not constants), since the build sets them via -ldflags
// -X pkg/version.Version=... at link time.
func TestOverridable(t *testing.T) {
	origVersion, origGitCommit := Version, GitCommit
	t.Cleanup(func() {
		Version = origVersion
		GitCommit = origGitCommit
	})

	Version = "v1.2.3"
	GitCommit = "abc1234"

	if Version != "v1.2.3" {
		t.Errorf("Version = %q, want %q", Version, "v1.2.3")
	}
	if GitCommit != "abc1234" {
		t.Errorf("GitCommit = %q, want %q", GitCommit, "abc1234")
	}
}
