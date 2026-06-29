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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true

// SandboxTemplate is a namespace-scoped reusable blueprint for creating sandboxes.
// It encapsulates the complete agent harness configuration.
type SandboxTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

// ClusterSandboxTemplate is a cluster-scoped variant of SandboxTemplate
// that can be referenced from any namespace.
type ClusterSandboxTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxTemplateSpec `json:"spec,omitempty"`
}

// SandboxTemplateSpec defines the desired configuration for sandboxes created from this template.
type SandboxTemplateSpec struct {
	// InheritsFrom references a parent template for composition.
	// Child fields merge over or override parent fields.
	// +optional
	InheritsFrom *TemplateReference `json:"inheritsFrom,omitempty"`

	// Mode is the default Sandbox.spec.mode for sandboxes created from this
	// template ("code" or "agent"). Defaults to "code".
	// +kubebuilder:default=code
	// +optional
	Mode SandboxMode `json:"mode,omitempty"`

	// Description provides a human-readable description for UI display.
	// +optional
	Description string `json:"description,omitempty"`

	// Image defines the default container image for sandboxes.
	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// Resources defines default CPU and memory requests/limits.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Storage defines default persistent volume configuration.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// Network defines default network isolation rules.
	// +optional
	Network *NetworkSpec `json:"network,omitempty"`

	// Env defines default environment variables.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Timeout defines the default maximum runtime duration.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// IdleTimeout defines the default idle timeout.
	// +optional
	IdleTimeout *metav1.Duration `json:"idleTimeout,omitempty"`

	// RuntimeClass defines the default container runtime.
	// +optional
	RuntimeClass *string `json:"runtimeClass,omitempty"`

	// Harness defines the agent runtime configuration.
	// +optional
	Harness *HarnessSpec `json:"harness,omitempty"`

	// InitScripts are shell commands executed in order when the sandbox first starts.
	// +optional
	InitScripts []string `json:"initScripts,omitempty"`

	// Files are deployed into the sandbox filesystem at startup.
	// +optional
	Files []FileSpec `json:"files,omitempty"`

	// Credentials defines default credential references for sandboxes.
	// +optional
	Credentials []CredentialRef `json:"credentials,omitempty"`

	// Sidecars defines default sidecar containers.
	// +optional
	Sidecars []corev1.Container `json:"sidecars,omitempty"`

	// InitContainers defines default init containers.
	// +optional
	InitContainers []corev1.Container `json:"initContainers,omitempty"`

	// Security defines default security settings.
	// +optional
	Security *SecuritySpec `json:"security,omitempty"`

	// ServiceAccount is the name of a Kubernetes ServiceAccount that
	// sandboxes created from this template run under. Use this to attach a
	// scoped cloud identity (EKS IRSA / GKE Workload Identity) so every
	// sandbox from this template gets the same per-template cloud
	// credentials instead of the namespace default ServiceAccount. A sandbox
	// may override this via its own spec.serviceAccount. The ServiceAccount
	// must already exist in the sandbox's namespace.
	// +optional
	ServiceAccount string `json:"serviceAccount,omitempty"`
}

// HarnessSpec defines the agent runtime configuration within a template.
type HarnessSpec struct {
	// Command is the agent binary or script to execute.
	// +optional
	Command []string `json:"command,omitempty"`

	// Args are command-line arguments for the agent command.
	// +optional
	Args []string `json:"args,omitempty"`

	// WorkingDir is the working directory for the agent process.
	// +optional
	WorkingDir string `json:"workingDir,omitempty"`

	// Shell is the entrypoint for interactive terminal sessions.
	// +kubebuilder:default="/bin/bash"
	// +optional
	Shell string `json:"shell,omitempty"`

	// Tools declares tools to be installed or verified at sandbox startup.
	// +optional
	Tools []ToolSpec `json:"tools,omitempty"`

	// SystemPrompt defines the agent's behavioral instructions and constraints.
	// Deployed into the sandbox filesystem at a well-known path.
	// +optional
	SystemPrompt *FileOrRef `json:"systemPrompt,omitempty"`

	// Skills are skill definitions deployed into the sandbox filesystem.
	// +optional
	Skills []SkillSpec `json:"skills,omitempty"`

	// Hooks defines lifecycle scripts executed at specific points.
	// +optional
	Hooks *HooksSpec `json:"hooks,omitempty"`

	// Constraints defines operational boundaries for the agent.
	// +optional
	Constraints *ConstraintsSpec `json:"constraints,omitempty"`

	// Agent declares how POST /configure and POST /invoke run code inside this
	// sandbox. Required when the sandbox is created with mode: "agent" — the
	// Router uses Entrypoint as the command for /invoke and runs InstallCommand
	// once at /configure time. Ignored for mode: "code".
	// +optional
	Agent *AgentSpec `json:"agent,omitempty"`

	// UseHTTPExec, when true, instructs the Router to proxy /exec, /files,
	// and /invoke calls to the in-pod sandbox-runtime HTTP server (port
	// 9000) instead of going through the legacy SPDY exec path. The
	// sandbox image must ship the runtime binary (today: only
	// sandbox-general from v0.3.6+); enabling this on an image that
	// doesn't ship the runtime will produce a 502 from the Router on
	// every request. Defaults to false (= today's SPDY behavior).
	//
	// When true, the controller also injects an AGENTTIER_RUNTIME_TOKEN
	// env var sourced from a per-sandbox Secret and adds an ingress rule
	// to the NetworkPolicy permitting the Router pod to reach :9000 on
	// the sandbox.
	// +optional
	UseHTTPExec *bool `json:"useHTTPExec,omitempty"`
}

// AgentSpec describes how an agent-mode sandbox accepts configuration and
// services /invoke calls. Tied to HarnessSpec because the agent runtime
// shares the same shell, working directory, and env conventions as the
// interactive code-mode harness — only the calling pattern differs.
type AgentSpec struct {
	// Entrypoint is the shell command AgentTier runs on every POST /invoke.
	// Required for mode: "agent". Receives the request body on stdin.
	// Example: ["python", "/workspace/agent.py"]
	// +optional
	Entrypoint []string `json:"entrypoint,omitempty"`

	// InstallCommand runs once during POST /configure to install dependencies.
	// Idempotent: re-configures with the same files + command short-circuit.
	// Example: ["pip", "install", "-r", "/workspace/requirements.txt"]
	// +optional
	InstallCommand []string `json:"installCommand,omitempty"`

	// WorkingDir is the working directory for the entrypoint and install.
	// Defaults to /workspace.
	// +optional
	WorkingDir string `json:"workingDir,omitempty"`

	// Env are environment variables additive over the template's harness env.
	// Useful for selecting a model provider or routing memory traffic.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// MaxConcurrentInvokes caps parallel /invoke calls per sandbox. The Router
	// returns HTTP 429 over this. 0 (the default) means unlimited.
	// +optional
	MaxConcurrentInvokes *int32 `json:"maxConcurrentInvokes,omitempty"`

	// DefaultInvokeTimeout is the wall-clock timeout per invoke. Defaults to
	// 30 minutes when unset. Callers can lower (but not raise) this per call.
	// +optional
	DefaultInvokeTimeout *metav1.Duration `json:"defaultInvokeTimeout,omitempty"`
}

// ToolSpec declares a tool to be installed or verified in the sandbox.
type ToolSpec struct {
	// Name of the tool (e.g., "node", "python", "git").
	Name string `json:"name"`

	// Version constraint (e.g., ">=18", "3.11").
	// +optional
	Version string `json:"version,omitempty"`

	// InstallCommand to run if the tool is not present.
	// +optional
	InstallCommand string `json:"installCommand,omitempty"`

	// VerifyCommand to check if the tool is installed (e.g., "node --version").
	// +optional
	VerifyCommand string `json:"verifyCommand,omitempty"`
}

// FileOrRef represents content that can be inline or referenced from a ConfigMap/Secret.
type FileOrRef struct {
	// Content is inline file content.
	// +optional
	Content string `json:"content,omitempty"`

	// ConfigMapRef references a ConfigMap key.
	// +optional
	ConfigMapRef *corev1.ConfigMapKeySelector `json:"configMapRef,omitempty"`

	// SecretRef references a Secret key.
	// +optional
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`

	// Path where the file is deployed in the sandbox filesystem.
	// +optional
	Path string `json:"path,omitempty"`
}

// SkillSpec defines a skill to be deployed into the sandbox.
type SkillSpec struct {
	// Name of the skill.
	Name string `json:"name"`

	// Content is the skill definition (inline or referenced).
	// +optional
	Content *FileOrRef `json:"content,omitempty"`
}

// HooksSpec defines lifecycle scripts for sandbox state transitions.
type HooksSpec struct {
	// OnStart is executed after the pod is ready.
	// +optional
	OnStart string `json:"onStart,omitempty"`

	// OnStop is executed before pod termination.
	// +optional
	OnStop string `json:"onStop,omitempty"`

	// OnIdle is executed when idle timeout triggers.
	// +optional
	OnIdle string `json:"onIdle,omitempty"`

	// OnResume is executed after sandbox resumes from stopped state.
	// +optional
	OnResume string `json:"onResume,omitempty"`
}

// ConstraintsSpec defines operational boundaries for the agent.
type ConstraintsSpec struct {
	// MaxFileSize is the maximum file size the agent can create.
	// +optional
	MaxFileSize *resource.Quantity `json:"maxFileSize,omitempty"`

	// MaxCommandTimeout is the maximum duration for a single command execution.
	// +optional
	MaxCommandTimeout *metav1.Duration `json:"maxCommandTimeout,omitempty"`

	// RestrictedCommands are commands the agent is not allowed to execute.
	// +optional
	RestrictedCommands []string `json:"restrictedCommands,omitempty"`

	// RestrictedPaths are filesystem paths the agent cannot access.
	// +optional
	RestrictedPaths []string `json:"restrictedPaths,omitempty"`

	// AllowedNetworkDests are network destinations the agent is allowed to reach.
	// +optional
	AllowedNetworkDests []string `json:"allowedNetworkDests,omitempty"`

	// DeniedNetworkDests are network destinations the agent is blocked from reaching.
	// +optional
	DeniedNetworkDests []string `json:"deniedNetworkDests,omitempty"`
}

// FileSpec defines a file to be deployed into the sandbox filesystem.
type FileSpec struct {
	// Path in the sandbox filesystem where the file is written.
	Path string `json:"path"`

	// Content is inline file content.
	// +optional
	Content string `json:"content,omitempty"`

	// ConfigMapRef references a ConfigMap key for the file content.
	// +optional
	ConfigMapRef *corev1.ConfigMapKeySelector `json:"configMapRef,omitempty"`

	// SecretRef references a Secret key for sensitive file content.
	// +optional
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`

	// Mode is the file permission mode (e.g., 0644).
	// +kubebuilder:default=420
	// +optional
	Mode *int32 `json:"mode,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxTemplateList contains a list of SandboxTemplate resources.
type SandboxTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxTemplate `json:"items"`
}

// +kubebuilder:object:root=true

// ClusterSandboxTemplateList contains a list of ClusterSandboxTemplate resources.
type ClusterSandboxTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterSandboxTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&SandboxTemplate{}, &SandboxTemplateList{},
		&ClusterSandboxTemplate{}, &ClusterSandboxTemplateList{},
	)
}
