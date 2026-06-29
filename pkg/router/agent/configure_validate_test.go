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

package agent

import (
	"strings"
	"testing"
)

// TestConfigureValidate_RejectsShellMetacharPaths guards the fix for the
// /configure writeFiles shell-injection: a file path containing a quote /
// backtick / backslash / newline would break out of the single-quoted
// `sh -c` interpolation. validate() must reject these.
func TestConfigureValidate_RejectsShellMetacharPaths(t *testing.T) {
	bad := []string{
		`/workspace/x'$(touch /tmp/pwned)'.txt`,
		"/workspace/x`id`.txt",
		`/workspace/x\".txt`,
		"/workspace/line\nbreak.txt",
	}
	for _, p := range bad {
		req := ConfigureRequest{Files: []ConfigureFile{{Path: p, Content: "data"}}}
		err := req.validate()
		if err == nil {
			t.Errorf("validate accepted injection-prone path %q", p)
			continue
		}
		if !strings.Contains(err.Error(), "disallowed characters") {
			t.Errorf("path %q: expected 'disallowed characters' error, got %v", p, err)
		}
	}
}

// TestConfigureValidate_AcceptsCleanPath confirms a normal absolute path still
// validates (the gate is scoped to metacharacters, not all paths).
func TestConfigureValidate_AcceptsCleanPath(t *testing.T) {
	req := ConfigureRequest{Files: []ConfigureFile{{Path: "/workspace/app/main.py", Content: "print(1)"}}}
	if err := req.validate(); err != nil {
		t.Errorf("validate rejected a clean path: %v", err)
	}
}
