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

package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fullSandboxTemplateSpec builds a spec with every optional field populated
// so DeepCopy round-trip tests exercise every branch of the generated
// DeepCopyInto for SandboxTemplateSpec and its nested harness types.
func fullSandboxTemplateSpec() SandboxTemplateSpec {
	rc := "gvisor"
	timeout := metav1.Duration{Duration: 3600}
	idleTimeout := metav1.Duration{Duration: 900}
	useHTTPExec := true
	fileMode := int32(0644)
	maxInvokes := int32(4)
	maxFileSize := resource.MustParse("100Mi")

	return SandboxTemplateSpec{
		InheritsFrom: &TemplateReference{Name: "parent", Kind: "SandboxTemplate"},
		Mode:         SandboxModeAgent,
		Description:  "a full template",
		Image: &ImageSpec{
			Repository: "ghcr.io/agenttier/sandbox-base:v1",
			PullPolicy: corev1.PullAlways,
		},
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
		Storage: &StorageSpec{Size: resource.MustParse("20Gi"), Shared: true},
		Network: &NetworkSpec{
			AllowInternet: false,
			EgressRules:   []NetworkRule{{CIDR: "10.0.0.0/8"}},
		},
		Env:          []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
		Timeout:      &timeout,
		IdleTimeout:  &idleTimeout,
		RuntimeClass: &rc,
		Harness: &HarnessSpec{
			Command:    []string{"/bin/bash"},
			Args:       []string{"-c", "echo hi"},
			WorkingDir: "/workspace",
			Shell:      "/bin/bash",
			Tools: []ToolSpec{
				{Name: "node", Version: ">=18", InstallCommand: "apt-get install -y nodejs", VerifyCommand: "node --version"},
			},
			SystemPrompt: &FileOrRef{Content: "be helpful", Path: "/etc/prompt"},
			Skills: []SkillSpec{
				{Name: "web-search", Content: &FileOrRef{Content: "skill body"}},
			},
			Hooks: &HooksSpec{OnStart: "echo start", OnStop: "echo stop", OnIdle: "echo idle", OnResume: "echo resume"},
			Constraints: &ConstraintsSpec{
				MaxFileSize:         &maxFileSize,
				MaxCommandTimeout:   &timeout,
				RestrictedCommands:  []string{"rm"},
				RestrictedPaths:     []string{"/etc"},
				AllowedNetworkDests: []string{"api.example.com"},
				DeniedNetworkDests:  []string{"evil.example.com"},
			},
			Agent: &AgentSpec{
				Entrypoint:           []string{"python", "agent.py"},
				InstallCommand:       []string{"pip", "install", "-r", "requirements.txt"},
				WorkingDir:           "/workspace",
				Env:                  []corev1.EnvVar{{Name: "MODEL", Value: "claude"}},
				MaxConcurrentInvokes: &maxInvokes,
				DefaultInvokeTimeout: &idleTimeout,
			},
			UseHTTPExec: &useHTTPExec,
		},
		InitScripts: []string{"echo init"},
		Files: []FileSpec{
			{Path: "/etc/config", Content: "key=value", Mode: &fileMode},
		},
		Credentials:    []CredentialRef{{SecretName: "creds", MountAs: "file", MountPath: "/secrets"}},
		Sidecars:       []corev1.Container{{Name: "sidecar", Image: "busybox"}},
		InitContainers: []corev1.Container{{Name: "init", Image: "busybox"}},
		Security:       &SecuritySpec{Privileged: true},
		ServiceAccount: "template-sa",
	}
}

func TestSandboxTemplate_DeepCopy_ProducesIndependentCopy(t *testing.T) {
	original := &SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "base", Namespace: "default"},
		Spec:       fullSandboxTemplateSpec(),
	}
	copy := original.DeepCopy()

	if copy == original {
		t.Fatal("DeepCopy returned the same pointer as the original")
	}

	copy.Spec.Harness.Command[0] = "mutated"
	copy.Spec.Harness.Tools[0].Name = "mutated"
	copy.Spec.Harness.Constraints.RestrictedCommands[0] = "mutated"
	copy.Spec.Harness.Agent.Entrypoint[0] = "mutated"
	copy.Spec.Files[0].Content = "mutated"
	copy.Spec.InitScripts[0] = "mutated"

	fresh := fullSandboxTemplateSpec()
	if copy.Spec.Harness.Command[0] == fresh.Harness.Command[0] {
		t.Error("mutating copy.Spec.Harness.Command affected a freshly built spec's value unexpectedly (aliasing)")
	}
	if original.Spec.Harness.Command[0] != fresh.Harness.Command[0] {
		t.Error("mutating copy.Spec.Harness.Command affected the original")
	}
	if original.Spec.Harness.Tools[0].Name != fresh.Harness.Tools[0].Name {
		t.Error("mutating copy.Spec.Harness.Tools affected the original")
	}
	if original.Spec.Harness.Constraints.RestrictedCommands[0] != fresh.Harness.Constraints.RestrictedCommands[0] {
		t.Error("mutating copy.Spec.Harness.Constraints affected the original")
	}
	if original.Spec.Harness.Agent.Entrypoint[0] != fresh.Harness.Agent.Entrypoint[0] {
		t.Error("mutating copy.Spec.Harness.Agent affected the original")
	}
	if original.Spec.Files[0].Content != fresh.Files[0].Content {
		t.Error("mutating copy.Spec.Files affected the original")
	}
	if original.Spec.InitScripts[0] != fresh.InitScripts[0] {
		t.Error("mutating copy.Spec.InitScripts affected the original")
	}
}

func TestSandboxTemplate_DeepCopyObject(t *testing.T) {
	original := &SandboxTemplate{ObjectMeta: metav1.ObjectMeta{Name: "base"}, Spec: fullSandboxTemplateSpec()}
	obj := original.DeepCopyObject()

	copy, ok := obj.(*SandboxTemplate)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *SandboxTemplate", obj)
	}
	if copy.Name != original.Name {
		t.Errorf("copy.Name = %q, want %q", copy.Name, original.Name)
	}
}

func TestSandboxTemplate_DeepCopy_NilReceiver(t *testing.T) {
	var st *SandboxTemplate
	if got := st.DeepCopy(); got != nil {
		t.Errorf("DeepCopy on nil *SandboxTemplate should return nil, got %+v", got)
	}
}

func TestClusterSandboxTemplate_DeepCopy_ProducesIndependentCopy(t *testing.T) {
	original := &ClusterSandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-base"},
		Spec:       fullSandboxTemplateSpec(),
	}
	copy := original.DeepCopy()

	copy.Spec.ServiceAccount = "mutated"
	copy.Spec.Network.EgressRules[0].CIDR = "0.0.0.0/0"

	if original.Spec.ServiceAccount == "mutated" {
		t.Error("mutating copy.Spec.ServiceAccount affected the original")
	}
	if original.Spec.Network.EgressRules[0].CIDR == "0.0.0.0/0" {
		t.Error("mutating copy.Spec.Network affected the original")
	}

	obj := original.DeepCopyObject()
	if _, ok := obj.(*ClusterSandboxTemplate); !ok {
		t.Fatalf("DeepCopyObject returned %T, want *ClusterSandboxTemplate", obj)
	}
}

func TestClusterSandboxTemplate_DeepCopy_NilReceiver(t *testing.T) {
	var cst *ClusterSandboxTemplate
	if got := cst.DeepCopy(); got != nil {
		t.Errorf("DeepCopy on nil *ClusterSandboxTemplate should return nil, got %+v", got)
	}
}

func TestSandboxTemplateList_DeepCopy(t *testing.T) {
	original := &SandboxTemplateList{
		Items: []SandboxTemplate{
			{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: fullSandboxTemplateSpec()},
			{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: fullSandboxTemplateSpec()},
		},
	}
	copy := original.DeepCopy()

	if len(copy.Items) != 2 {
		t.Fatalf("copy has %d items, want 2", len(copy.Items))
	}
	copy.Items[0].Spec.ServiceAccount = "mutated"
	if original.Items[0].Spec.ServiceAccount == "mutated" {
		t.Error("mutating copy.Items affected original items (slice element aliasing)")
	}

	if _, ok := original.DeepCopyObject().(*SandboxTemplateList); !ok {
		t.Fatal("DeepCopyObject did not return *SandboxTemplateList")
	}
}

func TestClusterSandboxTemplateList_DeepCopy(t *testing.T) {
	original := &ClusterSandboxTemplateList{
		Items: []ClusterSandboxTemplate{
			{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: fullSandboxTemplateSpec()},
		},
	}
	copy := original.DeepCopy()

	copy.Items[0].Spec.Harness.Shell = "/bin/zsh"
	if original.Items[0].Spec.Harness.Shell == "/bin/zsh" {
		t.Error("mutating copy.Items affected original items (slice element aliasing)")
	}

	if _, ok := original.DeepCopyObject().(*ClusterSandboxTemplateList); !ok {
		t.Fatal("DeepCopyObject did not return *ClusterSandboxTemplateList")
	}
}

func TestSandboxTemplateSpec_DeepCopy_NilOptionalFields(t *testing.T) {
	original := &SandboxTemplateSpec{Mode: SandboxModeCode, Description: "minimal"}
	copy := original.DeepCopy()

	if copy.Description != original.Description {
		t.Errorf("copy.Description = %q, want %q", copy.Description, original.Description)
	}
	if copy.Harness != nil || copy.Image != nil || copy.Storage != nil || copy.InheritsFrom != nil {
		t.Error("expected nil optional fields to remain nil after DeepCopy")
	}
}

func TestHarnessSpec_DeepCopy_NilOptionalFields(t *testing.T) {
	original := &HarnessSpec{Shell: "/bin/bash"}
	copy := original.DeepCopy()

	if copy.Shell != original.Shell {
		t.Errorf("copy.Shell = %q, want %q", copy.Shell, original.Shell)
	}
	if copy.SystemPrompt != nil || copy.Hooks != nil || copy.Constraints != nil || copy.Agent != nil || copy.UseHTTPExec != nil {
		t.Error("expected nil optional fields to remain nil after DeepCopy")
	}
}
