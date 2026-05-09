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
