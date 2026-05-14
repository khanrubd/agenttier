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

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func TestMergeEnvVars_Empty(t *testing.T) {
	result := mergeEnvVars(nil, nil)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d items", len(result))
	}
}

func TestMergeEnvVars_ParentOnly(t *testing.T) {
	parent := []corev1.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "BAZ", Value: "qux"},
	}
	result := mergeEnvVars(parent, nil)
	if len(result) != 2 {
		t.Errorf("expected 2 items, got %d", len(result))
	}
}

func TestMergeEnvVars_ChildOverridesParent(t *testing.T) {
	parent := []corev1.EnvVar{
		{Name: "FOO", Value: "parent_value"},
		{Name: "SHARED", Value: "parent"},
	}
	child := []corev1.EnvVar{
		{Name: "SHARED", Value: "child"},
		{Name: "NEW", Value: "new_value"},
	}

	result := mergeEnvVars(parent, child)

	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result))
	}

	// Verify child wins on conflict
	for _, env := range result {
		if env.Name == "SHARED" && env.Value != "child" {
			t.Errorf("expected SHARED=child, got SHARED=%s", env.Value)
		}
	}

	// Verify parent value preserved
	found := false
	for _, env := range result {
		if env.Name == "FOO" && env.Value == "parent_value" {
			found = true
		}
	}
	if !found {
		t.Error("expected FOO=parent_value to be preserved")
	}
}

func TestMergeFiles_ChildOverridesPath(t *testing.T) {
	parent := []agenttierv1alpha1.FileSpec{
		{Path: "/workspace/.bashrc", Content: "parent_content"},
		{Path: "/workspace/README.md", Content: "readme"},
	}
	child := []agenttierv1alpha1.FileSpec{
		{Path: "/workspace/.bashrc", Content: "child_content"},
		{Path: "/workspace/new_file.txt", Content: "new"},
	}

	result := mergeFiles(parent, child)

	if len(result) != 3 {
		t.Fatalf("expected 3 files, got %d", len(result))
	}

	// Verify child wins on path conflict
	for _, f := range result {
		if f.Path == "/workspace/.bashrc" && f.Content != "child_content" {
			t.Errorf("expected .bashrc=child_content, got %s", f.Content)
		}
	}
}

func TestMergeTemplateSpecs_ScalarOverride(t *testing.T) {
	parentImage := &agenttierv1alpha1.ImageSpec{Repository: "parent:latest"}
	childImage := &agenttierv1alpha1.ImageSpec{Repository: "child:v1"}

	parent := agenttierv1alpha1.SandboxTemplateSpec{
		Image:       parentImage,
		Description: "parent description",
	}
	child := agenttierv1alpha1.SandboxTemplateSpec{
		Image:       childImage,
		Description: "child description",
	}

	result := mergeTemplateSpecs(parent, child)

	if result.Image.Repository != "child:v1" {
		t.Errorf("expected child image, got %s", result.Image.Repository)
	}
	if result.Description != "child description" {
		t.Errorf("expected child description, got %s", result.Description)
	}
}

func TestMergeTemplateSpecs_ParentPreservedWhenChildNil(t *testing.T) {
	parentStorage := &agenttierv1alpha1.StorageSpec{
		Size: resource.MustParse("20Gi"),
	}

	parent := agenttierv1alpha1.SandboxTemplateSpec{
		Storage: parentStorage,
	}
	child := agenttierv1alpha1.SandboxTemplateSpec{
		// Storage is nil — parent should be preserved
	}

	result := mergeTemplateSpecs(parent, child)

	if result.Storage == nil {
		t.Fatal("expected parent storage to be preserved")
	}
	if result.Storage.Size.String() != "20Gi" {
		t.Errorf("expected 20Gi, got %s", result.Storage.Size.String())
	}
}

func TestMergeTemplateSpecs_InitScriptsConcatenated(t *testing.T) {
	parent := agenttierv1alpha1.SandboxTemplateSpec{
		InitScripts: []string{"echo parent1", "echo parent2"},
	}
	child := agenttierv1alpha1.SandboxTemplateSpec{
		InitScripts: []string{"echo child1"},
	}

	result := mergeTemplateSpecs(parent, child)

	if len(result.InitScripts) != 3 {
		t.Fatalf("expected 3 init scripts, got %d", len(result.InitScripts))
	}
	if result.InitScripts[0] != "echo parent1" {
		t.Error("expected parent scripts first")
	}
	if result.InitScripts[2] != "echo child1" {
		t.Error("expected child scripts last")
	}
}

func TestMergeSandboxWithTemplate_SandboxOverridesTemplate(t *testing.T) {
	sandboxImage := &agenttierv1alpha1.ImageSpec{Repository: "sandbox:v2"}
	templateImage := &agenttierv1alpha1.ImageSpec{Repository: "template:v1"}

	sandbox := &agenttierv1alpha1.SandboxSpec{
		Image: sandboxImage,
		Env: []corev1.EnvVar{
			{Name: "SANDBOX_VAR", Value: "sandbox"},
		},
	}
	template := &agenttierv1alpha1.SandboxTemplateSpec{
		Image: templateImage,
		Env: []corev1.EnvVar{
			{Name: "TEMPLATE_VAR", Value: "template"},
		},
	}

	config := MergeSandboxWithTemplate(sandbox, template, nil)

	if config.Image != "sandbox:v2" {
		t.Errorf("expected sandbox image override, got %s", config.Image)
	}
	if len(config.Env) != 2 {
		t.Fatalf("expected 2 env vars (merged), got %d", len(config.Env))
	}
}

func TestMergeSandboxWithTemplate_DefaultsFillGaps(t *testing.T) {
	sandbox := &agenttierv1alpha1.SandboxSpec{}
	defaults := &ControllerDefaults{
		Image:   "default:latest",
		Storage: "10Gi",
	}

	config := MergeSandboxWithTemplate(sandbox, nil, defaults)

	if config.Image != "default:latest" {
		t.Errorf("expected default image, got %s", config.Image)
	}
}

func TestMergeTemplateSpecs_ModeAndAgentInherit(t *testing.T) {
	parent := agenttierv1alpha1.SandboxTemplateSpec{
		Mode:        agenttierv1alpha1.SandboxModeCode,
		Description: "parent",
		Harness: &agenttierv1alpha1.HarnessSpec{
			Shell: "/bin/bash",
		},
	}
	child := agenttierv1alpha1.SandboxTemplateSpec{
		Mode: agenttierv1alpha1.SandboxModeAgent,
		Harness: &agenttierv1alpha1.HarnessSpec{
			Agent: &agenttierv1alpha1.AgentSpec{
				Entrypoint:     []string{"python", "/workspace/agent.py"},
				InstallCommand: []string{"pip", "install", "-r", "requirements.txt"},
				WorkingDir:     "/workspace",
			},
		},
	}

	merged := mergeTemplateSpecs(parent, child)

	if merged.Mode != agenttierv1alpha1.SandboxModeAgent {
		t.Errorf("expected child mode 'agent' to win, got %q", merged.Mode)
	}
	if merged.Description != "parent" {
		t.Errorf("expected parent description preserved, got %q", merged.Description)
	}
	if merged.Harness == nil || merged.Harness.Shell != "/bin/bash" {
		t.Error("expected parent harness.shell preserved")
	}
	if merged.Harness == nil || merged.Harness.Agent == nil {
		t.Fatal("expected child agent spec inherited")
	}
	if got := merged.Harness.Agent.Entrypoint; len(got) != 2 || got[0] != "python" {
		t.Errorf("expected entrypoint inherited, got %v", got)
	}
}

func TestMergeAgent_ChildOverridesScalars(t *testing.T) {
	maxParent := int32(2)
	maxChild := int32(8)
	parent := &agenttierv1alpha1.AgentSpec{
		Entrypoint:           []string{"old"},
		WorkingDir:           "/old",
		MaxConcurrentInvokes: &maxParent,
		Env: []corev1.EnvVar{
			{Name: "PARENT_ONLY", Value: "p"},
			{Name: "SHARED", Value: "parent"},
		},
	}
	child := &agenttierv1alpha1.AgentSpec{
		Entrypoint:           []string{"python", "agent.py"},
		WorkingDir:           "/workspace",
		MaxConcurrentInvokes: &maxChild,
		Env: []corev1.EnvVar{
			{Name: "SHARED", Value: "child"},
			{Name: "CHILD_ONLY", Value: "c"},
		},
	}

	merged := mergeAgent(parent, child)

	if got := merged.Entrypoint; len(got) != 2 || got[1] != "agent.py" {
		t.Errorf("expected child entrypoint to win, got %v", got)
	}
	if merged.WorkingDir != "/workspace" {
		t.Errorf("expected child workingDir to win, got %q", merged.WorkingDir)
	}
	if merged.MaxConcurrentInvokes == nil || *merged.MaxConcurrentInvokes != 8 {
		t.Errorf("expected child maxConcurrentInvokes=8, got %v", merged.MaxConcurrentInvokes)
	}
	envByKey := map[string]string{}
	for _, e := range merged.Env {
		envByKey[e.Name] = e.Value
	}
	if envByKey["PARENT_ONLY"] != "p" {
		t.Errorf("expected PARENT_ONLY preserved, got %q", envByKey["PARENT_ONLY"])
	}
	if envByKey["SHARED"] != "child" {
		t.Errorf("expected SHARED=child, got %q", envByKey["SHARED"])
	}
	if envByKey["CHILD_ONLY"] != "c" {
		t.Errorf("expected CHILD_ONLY=c, got %q", envByKey["CHILD_ONLY"])
	}
}

// Silence unused-import warnings in the rare case the tests above are the
// only reference to a particular package.
var _ = resource.Quantity{}

func TestMergeSandboxWithTemplate_InjectsMemorySidecarForAgentMode(t *testing.T) {
	sb := &agenttierv1alpha1.SandboxSpec{
		Mode: agenttierv1alpha1.SandboxModeAgent,
	}
	defaults := &ControllerDefaults{
		Image:                   "ghcr.io/agenttier/sandbox-langgraph:v0.3.0",
		MountPath:               "/workspace",
		AgentMemorySidecarImage: "mem0/mem0:0.1.115",
	}

	config := MergeSandboxWithTemplate(sb, nil, defaults)

	if len(config.Sidecars) != 1 {
		t.Fatalf("expected 1 sidecar (mem0), got %d", len(config.Sidecars))
	}
	if config.Sidecars[0].Name != "mem0" {
		t.Errorf("expected sidecar name mem0, got %s", config.Sidecars[0].Name)
	}
	if config.Sidecars[0].Image != "mem0/mem0:0.1.115" {
		t.Errorf("expected mem0 image, got %s", config.Sidecars[0].Image)
	}

	// MEM0_BASE_URL should be set in the agent container's env.
	var found bool
	for _, e := range config.Env {
		if e.Name == "MEM0_BASE_URL" && e.Value == "http://localhost:11434" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected MEM0_BASE_URL in agent env, got %v", config.Env)
	}
}

func TestMergeSandboxWithTemplate_NoSidecarForCodeMode(t *testing.T) {
	sb := &agenttierv1alpha1.SandboxSpec{
		Mode: agenttierv1alpha1.SandboxModeCode,
	}
	defaults := &ControllerDefaults{
		AgentMemorySidecarImage: "mem0/mem0:0.1.115",
	}

	config := MergeSandboxWithTemplate(sb, nil, defaults)

	if len(config.Sidecars) != 0 {
		t.Errorf("expected zero sidecars for code-mode sandbox, got %d", len(config.Sidecars))
	}
	for _, e := range config.Env {
		if e.Name == "MEM0_BASE_URL" {
			t.Error("MEM0_BASE_URL leaked into a code-mode sandbox")
		}
	}
}

func TestMergeSandboxWithTemplate_NoSidecarWhenFlagOff(t *testing.T) {
	sb := &agenttierv1alpha1.SandboxSpec{
		Mode: agenttierv1alpha1.SandboxModeAgent,
	}
	defaults := &ControllerDefaults{} // empty AgentMemorySidecarImage = feature off

	config := MergeSandboxWithTemplate(sb, nil, defaults)

	if len(config.Sidecars) != 0 {
		t.Errorf("expected no sidecar when flag is off, got %d", len(config.Sidecars))
	}
}
