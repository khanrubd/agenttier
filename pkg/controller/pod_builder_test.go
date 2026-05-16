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
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func TestPodBuilder_DefaultSecurityContext(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/workspace",
		PVCName:   "test-workspace",
	}

	pod := builder.Build(sandbox, config)
	container := pod.Spec.Containers[0]
	sc := container.SecurityContext

	if sc == nil {
		t.Fatal("expected security context to be set")
	}
	if !*sc.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true")
	}
	if *sc.RunAsUser != 1000 {
		t.Errorf("expected RunAsUser=1000, got %d", *sc.RunAsUser)
	}
	if !*sc.ReadOnlyRootFilesystem {
		t.Error("expected ReadOnlyRootFilesystem=true")
	}
	if *sc.AllowPrivilegeEscalation {
		t.Error("expected AllowPrivilegeEscalation=false")
	}
	if len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Error("expected capabilities drop ALL")
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("expected seccomp RuntimeDefault")
	}
}

func TestPodBuilder_PrivilegedRelaxesSecurity(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:      "test:v1",
		MountPath:  "/workspace",
		PVCName:    "test-workspace",
		Privileged: true,
	}

	pod := builder.Build(sandbox, config)
	container := pod.Spec.Containers[0]
	sc := container.SecurityContext

	if sc == nil {
		t.Fatal("expected security context")
	}
	// Privileged mode should NOT have read-only root fs or dropped caps
	if sc.ReadOnlyRootFilesystem != nil && *sc.ReadOnlyRootFilesystem {
		t.Error("privileged mode should not enforce read-only root fs")
	}
	if sc.Capabilities != nil {
		t.Error("privileged mode should not drop capabilities")
	}
}

func TestPodBuilder_NoHostPath(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/workspace",
		PVCName:   "test-workspace",
	}

	pod := builder.Build(sandbox, config)

	// Verify no hostPath volumes
	for _, v := range pod.Spec.Volumes {
		if v.HostPath != nil {
			t.Errorf("hostPath volume found: %s", v.Name)
		}
	}

	// Verify no host networking
	if pod.Spec.HostNetwork {
		t.Error("hostNetwork should be false")
	}
	if pod.Spec.HostPID {
		t.Error("hostPID should be false")
	}
	if pod.Spec.HostIPC {
		t.Error("hostIPC should be false")
	}
}

func TestPodBuilder_EnforceSecurityInvariants(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/workspace",
		PVCName:   "test-workspace",
	}

	pod := builder.Build(sandbox, config)

	// Manually inject dangerous fields (simulating adversarial input)
	pod.Spec.HostNetwork = true
	pod.Spec.HostPID = true
	pod.Spec.HostIPC = true
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: "evil",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/etc/shadow"},
		},
	})

	// Re-enforce invariants
	builder.enforceSecurityInvariants(pod)

	if pod.Spec.HostNetwork {
		t.Error("hostNetwork should be stripped")
	}
	if pod.Spec.HostPID {
		t.Error("hostPID should be stripped")
	}
	if pod.Spec.HostIPC {
		t.Error("hostIPC should be stripped")
	}
	for _, v := range pod.Spec.Volumes {
		if v.HostPath != nil {
			t.Error("hostPath volume should be stripped")
		}
	}
}

func TestPodBuilder_WorkspaceVolumeMount(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/custom/workspace",
		PVCName:   "test-workspace",
	}

	pod := builder.Build(sandbox, config)
	container := pod.Spec.Containers[0]

	found := false
	for _, vm := range container.VolumeMounts {
		if vm.Name == workspaceVolumeName && vm.MountPath == "/custom/workspace" {
			found = true
		}
	}
	if !found {
		t.Error("expected workspace volume mounted at /custom/workspace")
	}
}

func TestPodBuilder_CredentialEnvInjection(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/workspace",
		PVCName:   "test-workspace",
		Credentials: []agenttierv1alpha1.CredentialRef{
			{SecretName: "aws-creds", MountAs: "env", EnvPrefix: "AWS_"},
		},
	}

	pod := builder.Build(sandbox, config)
	container := pod.Spec.Containers[0]

	if len(container.EnvFrom) != 1 {
		t.Fatalf("expected 1 envFrom, got %d", len(container.EnvFrom))
	}
	if container.EnvFrom[0].SecretRef.Name != "aws-creds" {
		t.Errorf("expected secret ref aws-creds, got %s", container.EnvFrom[0].SecretRef.Name)
	}
	if container.EnvFrom[0].Prefix != "AWS_" {
		t.Errorf("expected prefix AWS_, got %s", container.EnvFrom[0].Prefix)
	}
}

func TestPodBuilder_CredentialFileMount(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/workspace",
		PVCName:   "test-workspace",
		Credentials: []agenttierv1alpha1.CredentialRef{
			{SecretName: "gcp-key", MountAs: "file", MountPath: "/var/secrets/gcp"},
		},
	}

	pod := builder.Build(sandbox, config)
	container := pod.Spec.Containers[0]

	// Check volume mount exists
	found := false
	for _, vm := range container.VolumeMounts {
		if vm.MountPath == "/var/secrets/gcp" && vm.ReadOnly {
			found = true
		}
	}
	if !found {
		t.Error("expected credential file mount at /var/secrets/gcp")
	}
}

func TestPodBuilder_InitContainersFromScripts(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:       "test:v1",
		MountPath:   "/workspace",
		PVCName:     "test-workspace",
		InitScripts: []string{"apt-get update", "apt-get install -y git"},
	}

	pod := builder.Build(sandbox, config)

	if len(pod.Spec.InitContainers) < 1 {
		t.Fatal("expected at least 1 init container for scripts")
	}

	// Find the init-scripts container
	found := false
	for _, ic := range pod.Spec.InitContainers {
		if ic.Name == "init-scripts" {
			found = true
			// Verify it has workspace mount
			hasMount := false
			for _, vm := range ic.VolumeMounts {
				if vm.Name == workspaceVolumeName {
					hasMount = true
				}
			}
			if !hasMount {
				t.Error("init-scripts container should mount workspace volume")
			}
		}
	}
	if !found {
		t.Error("expected init-scripts container")
	}
}

func TestPodBuilder_Labels(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "my-sandbox", Namespace: "dev"},
		Status: agenttierv1alpha1.SandboxStatus{
			ResolvedTemplate: "general-coding",
		},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/workspace",
		PVCName:   "my-sandbox-workspace",
	}

	pod := builder.Build(sandbox, config)

	if pod.Labels["agenttier.io/sandbox"] != "my-sandbox" {
		t.Error("expected sandbox label")
	}
	if pod.Labels["agenttier.io/managed"] != "true" {
		t.Error("expected managed label")
	}
	if pod.Labels["agenttier.io/template"] != "general-coding" {
		t.Error("expected template label")
	}
}

func TestPodBuilder_RestartPolicyNever(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	config := &MergedPodConfig{
		Image:     "test:v1",
		MountPath: "/workspace",
		PVCName:   "test-workspace",
	}

	pod := builder.Build(sandbox, config)

	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("expected RestartPolicy=Never, got %s", pod.Spec.RestartPolicy)
	}
}


// TestPodBuilder_FileDeployerHandlesEOFMarker verifies that file content
// containing the literal string "AGENTLOFT_EOF" on its own line round-trips
// through the file deployer init container without truncation. The previous
// implementation used a heredoc terminator with that exact string and would
// silently truncate. The new implementation pipes through `base64 -d`, which
// has no marker collision risk.
func TestPodBuilder_FileDeployerHandlesEOFMarker(t *testing.T) {
	builder := &PodBuilder{DefaultImage: "default:latest"}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	mode := int32(0644)
	// Content that would have terminated the old heredoc on the second line.
	dangerous := "first line\nAGENTLOFT_EOF\nthird line should survive\n"
	config := &MergedPodConfig{
		Image:     "test:latest",
		MountPath: "/workspace",
		PVCName:   "test-pvc",
		Files: []agenttierv1alpha1.FileSpec{
			{Path: "/workspace/danger.txt", Content: dangerous, Mode: &mode},
		},
	}

	pod := builder.Build(sandbox, config)

	// Find the file-deployer init container.
	var script string
	for _, ic := range pod.Spec.InitContainers {
		if ic.Name == "file-deployer" {
			if len(ic.Command) < 3 {
				t.Fatalf("file-deployer Command malformed: %v", ic.Command)
			}
			script = ic.Command[2]
			break
		}
	}
	if script == "" {
		t.Fatal("file-deployer init container not found")
	}

	// The new script must NOT contain a heredoc — that's the source of
	// the bug. base64 + printf is the right pattern.
	if containsAny(script, "<<", "AGENTLOFT_EOF") {
		t.Errorf("script still uses heredoc/marker pattern: %q", script)
	}
	// Must use base64 -d to decode the payload.
	if !containsAll(script, "base64 -d", "/workspace/danger.txt") {
		t.Errorf("script doesn't use base64 -d: %q", script)
	}

	// Decoding the base64 payload from the script should round-trip the
	// original content exactly. We extract the base64 token (it sits
	// between the printf format and the pipe) and decode it.
	encoded := extractBase64Token(t, script)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("script's embedded payload is not valid base64: %v", err)
	}
	if string(decoded) != dangerous {
		t.Errorf("payload round-trip mismatch:\n  want: %q\n  got:  %q", dangerous, string(decoded))
	}
}

// containsAll returns true if all substrings appear in s.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// containsAny returns true if any substring appears in s.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// extractBase64Token pulls the base64 payload out of the printf | base64 -d
// pipeline that the file deployer emits. The script line for one file is:
//
//	mkdir -p $(dirname '/workspace/x.txt') && printf '%s' "<base64>" | base64 -d > '/workspace/x.txt'
//
// We split on `printf '%s' ` and then on ` | base64 -d`, then strip the
// surrounding %q quoting (Go's %q wraps in double quotes and escapes inner
// chars; for the standard base64 alphabet the result is just `"<token>"`).
func extractBase64Token(t *testing.T, script string) string {
	t.Helper()
	const startMarker = "printf '%s' "
	const endMarker = " | base64 -d"
	startIdx := strings.Index(script, startMarker)
	if startIdx < 0 {
		t.Fatalf("script missing printf marker: %q", script)
	}
	rest := script[startIdx+len(startMarker):]
	endIdx := strings.Index(rest, endMarker)
	if endIdx < 0 {
		t.Fatalf("script missing base64 marker: %q", script)
	}
	quoted := rest[:endIdx]
	// Go's %q wraps in double quotes; strip them. Base64 alphabet is
	// [A-Za-z0-9+/=] so no escapes are needed inside the quotes.
	if len(quoted) >= 2 && quoted[0] == '"' && quoted[len(quoted)-1] == '"' {
		return quoted[1 : len(quoted)-1]
	}
	return quoted
}
