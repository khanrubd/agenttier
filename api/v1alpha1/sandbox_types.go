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

// SandboxPhase represents the current lifecycle phase of a Sandbox.
type SandboxPhase string

const (
	// SandboxPhaseCreating indicates the sandbox is being provisioned.
	SandboxPhaseCreating SandboxPhase = "Creating"
	// SandboxPhaseRunning indicates the sandbox pod is running and ready.
	SandboxPhaseRunning SandboxPhase = "Running"
	// SandboxPhaseStopped indicates the sandbox pod has been deleted but PVC persists.
	SandboxPhaseStopped SandboxPhase = "Stopped"
	// SandboxPhaseError indicates the sandbox encountered an unrecoverable error.
	SandboxPhaseError SandboxPhase = "Error"
	// SandboxPhaseDeleting indicates the sandbox is being permanently removed.
	SandboxPhaseDeleting SandboxPhase = "Deleting"
)

// SandboxMode declares whether a sandbox runs in interactive code mode (the
// default — humans drive it via terminal, file API, port-forwards, exec) or
// in agent mode (a configured entrypoint is invoked over SSE via /invoke).
//
// Agent mode reuses the same Pod, PVC, NetworkPolicy, governance, and warm
// pool machinery — only the calling pattern differs. The CRD field defaults
// to "code" so existing sandboxes and templates keep working without changes.
// +kubebuilder:validation:Enum=code;agent
type SandboxMode string

const (
	// SandboxModeCode is the default, interactive mode used by humans.
	SandboxModeCode SandboxMode = "code"
	// SandboxModeAgent runs a configured entrypoint via /configure + /invoke.
	SandboxModeAgent SandboxMode = "agent"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.status.resolvedTemplate`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Sandbox represents a single isolated agent environment managed by AgentTier.
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// SandboxSpec defines the desired state of a Sandbox.
type SandboxSpec struct {
	// Mode controls whether this sandbox is interactive ("code", default) or
	// runs a configured agent entrypoint ("agent"). Agent-mode sandboxes are
	// driven through POST /configure + POST /invoke instead of the terminal.
	// +kubebuilder:default=code
	// +optional
	Mode SandboxMode `json:"mode,omitempty"`

	// TemplateRef references a SandboxTemplate or ClusterSandboxTemplate from which
	// this sandbox inherits its configuration.
	// +optional
	TemplateRef *TemplateReference `json:"templateRef,omitempty"`

	// Image specifies the container image for the sandbox. Overrides the template image.
	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// Resources specifies CPU and memory requests and limits for the sandbox container.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Storage configures the persistent volume for the sandbox workspace.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// Network declares egress and ingress rules for the sandbox.
	// +optional
	Network *NetworkSpec `json:"network,omitempty"`

	// Env specifies environment variables to inject into the sandbox container.
	// These are merged with template-defined environment variables.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Timeout defines the maximum duration the sandbox may remain running.
	// Use "0" or omit for infinite (subject to governance limits).
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// IdleTimeout defines how long the sandbox may remain idle before auto-stop.
	// Use "0" or omit for infinite (subject to governance limits).
	// +optional
	IdleTimeout *metav1.Duration `json:"idleTimeout,omitempty"`

	// RuntimeClass specifies an alternative container runtime (e.g., "gvisor").
	// +optional
	RuntimeClass *string `json:"runtimeClass,omitempty"`

	// ServiceAccount is the name of a Kubernetes ServiceAccount to run the
	// sandbox Pod under. Use this to attach a scoped cloud identity — an EKS
	// IRSA-annotated ServiceAccount or a GKE Workload Identity SA — so the
	// sandbox gets per-sandbox cloud credentials instead of the namespace
	// default ServiceAccount. The ServiceAccount must already exist in the
	// sandbox's namespace. Overrides the template's serviceAccount.
	// +optional
	ServiceAccount string `json:"serviceAccount,omitempty"`

	// AutoResume automatically resumes a stopped sandbox when an exec session is requested.
	// +optional
	AutoResume bool `json:"autoResume,omitempty"`

	// Security configures pod security context overrides.
	// +optional
	Security *SecuritySpec `json:"security,omitempty"`

	// Credentials references Kubernetes Secrets for credential injection into the sandbox.
	// +optional
	Credentials []CredentialRef `json:"credentials,omitempty"`

	// Sidecars defines additional containers to run alongside the sandbox container.
	// +optional
	Sidecars []corev1.Container `json:"sidecars,omitempty"`

	// InitContainers defines containers to run before the sandbox starts.
	// +optional
	InitContainers []corev1.Container `json:"initContainers,omitempty"`

	// CreatedBy stores the authenticated user identity. Set by the admission webhook.
	// +optional
	CreatedBy *UserIdentity `json:"createdBy,omitempty"`

	// Sharing defines access permissions for other users.
	// +optional
	Sharing *SharingSpec `json:"sharing,omitempty"`

	// CloneFromSnapshot, when set, instructs the controller to provision
	// this sandbox's PVC from the named VolumeSnapshot rather than as
	// an empty volume. Used internally by POST /sandboxes/{id}/clone — the
	// Router takes a VolumeSnapshot of the source sandbox's PVC and stamps
	// the snapshot's name here on the cloned Sandbox CR before creating
	// it. The reconciler's PVC builder reads the field and emits a PVC
	// with `dataSource: VolumeSnapshot{name: ...}` so the EBS CSI driver
	// hydrates the new volume from the snapshot at provision time.
	//
	// The named VolumeSnapshot MUST live in the same namespace as the
	// Sandbox; cross-namespace clones are rejected at create time.
	// +optional
	CloneFromSnapshot string `json:"cloneFromSnapshot,omitempty"`
}

// SandboxStatus defines the observed state of a Sandbox.
type SandboxStatus struct {
	// Phase is the current lifecycle phase of the sandbox.
	// +optional
	Phase SandboxPhase `json:"phase,omitempty"`

	// PodName is the name of the current sandbox pod.
	// +optional
	PodName string `json:"podName,omitempty"`

	// PVCName is the name of the persistent volume claim.
	// +optional
	PVCName string `json:"pvcName,omitempty"`

	// ResolvedTemplate is the name of the template used to create this sandbox.
	// +optional
	ResolvedTemplate string `json:"resolvedTemplate,omitempty"`

	// TemplateResourceVersion records which template version was used for auditability.
	// +optional
	TemplateResourceVersion string `json:"templateResourceVersion,omitempty"`

	// StartedAt is when the current pod started running.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// LastActivityTimestamp tracks the last exec session or API interaction.
	// +optional
	LastActivityTimestamp *metav1.Time `json:"lastActivityTimestamp,omitempty"`

	// RestartCount tracks the number of infrastructure-failure restarts.
	// +optional
	RestartCount int `json:"restartCount,omitempty"`

	// Message provides a human-readable description of the current status.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions represent the latest available observations of the sandbox's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ForwardedPorts lists active port forwards for this sandbox.
	// +optional
	ForwardedPorts []ForwardedPort `json:"forwardedPorts,omitempty"`

	// ClonedFrom records the source sandbox ID if this sandbox was cloned.
	// +optional
	ClonedFrom string `json:"clonedFrom,omitempty"`

	// AgentConfigure records the most recent /configure result for agent-mode
	// sandboxes. Set only when Spec.Mode == "agent" and POST /configure has
	// completed at least once.
	// +optional
	AgentConfigure *AgentConfigureStatus `json:"agentConfigure,omitempty"`

	// ResolvedAgentSpec is the fully merged AgentSpec from the template
	// inheritance chain, recorded on the sandbox at create time. The Router
	// reads MaxConcurrentInvokes and DefaultInvokeTimeout from here at
	// /configure time so it doesn't have to re-walk the chain (and so a
	// child template that inherits caps from a parent still gets them
	// enforced — see "Template inheritance not walked when resolving
	// agent caps" for the regression context).
	// +optional
	ResolvedAgentSpec *AgentSpec `json:"resolvedAgentSpec,omitempty"`
}

// AgentConfigureStatus records the resolved configuration applied via the most
// recent POST /configure call on an agent-mode sandbox. The Router uses
// InstallCommandHash to short-circuit re-installs on idempotent re-configures.
type AgentConfigureStatus struct {
	// LastConfiguredAt is when /configure last completed successfully.
	// +optional
	LastConfiguredAt *metav1.Time `json:"lastConfiguredAt,omitempty"`

	// InstallCommandHash is the SHA256 of the install command + uploaded files.
	// /configure calls with the same hash are no-ops.
	// +optional
	InstallCommandHash string `json:"installCommandHash,omitempty"`

	// Entrypoint is the resolved command POST /invoke executes.
	// +optional
	Entrypoint []string `json:"entrypoint,omitempty"`

	// InstallExitCode is the exit code from the most recent install command.
	// 0 indicates success; non-zero means the install failed and /invoke will
	// likely fail the same way.
	// +optional
	InstallExitCode int `json:"installExitCode,omitempty"`

	// InstallLogConfigMapRef points at the ConfigMap that holds the trailing
	// install-log bytes for this configure run. The ConfigMap lives in the
	// same namespace as the Sandbox and is owner-referenced so it's
	// garbage-collected when the Sandbox is deleted. The Router writes it
	// after every /configure run; the Web UI / SDK fetch the log on demand
	// via GET /api/v1/sandboxes/{id}/configure/install-log.
	//
	// We persist the log out-of-band (rather than inline on the CR status)
	// to keep Sandbox objects small. Writing 8 KiB of install log into
	// every Sandbox's status block bloats etcd, multiplies watch churn
	// across every controller / Router replica, and dumps unrelated noise
	// into `kubectl describe sandbox` output.
	// +optional
	InstallLogConfigMapRef *LocalObjectReference `json:"installLogConfigMapRef,omitempty"`

	// MaxConcurrentInvokes mirrors the resolved template / governance cap
	// at /configure time so the /invoke handler can enforce it without
	// re-resolving the template chain on every request.
	// +optional
	MaxConcurrentInvokes int32 `json:"maxConcurrentInvokes,omitempty"`

	// DefaultInvokeTimeoutSeconds mirrors the resolved per-invoke timeout
	// in seconds. Zero means "use the Router default" (30 minutes today).
	// +optional
	DefaultInvokeTimeoutSeconds int32 `json:"defaultInvokeTimeoutSeconds,omitempty"`
}

// LocalObjectReference points at a Kubernetes object in the same namespace
// as the referrer. We define our own (rather than reusing
// `corev1.LocalObjectReference`) to keep the v1alpha1 surface free of
// transitive corev1 imports — clients reading our CRDs only need our types.
type LocalObjectReference struct {
	// Name of the referenced object.
	Name string `json:"name"`
}

// TemplateReference identifies a SandboxTemplate or ClusterSandboxTemplate.
type TemplateReference struct {
	// Name of the template resource.
	Name string `json:"name"`

	// Kind is "SandboxTemplate" (default) or "ClusterSandboxTemplate".
	// +kubebuilder:default=SandboxTemplate
	// +optional
	Kind string `json:"kind,omitempty"`

	// Namespace of the template. Only applicable for SandboxTemplate.
	// Defaults to the sandbox's namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ImageSpec defines the container image configuration.
type ImageSpec struct {
	// Repository is the full OCI image reference (e.g., "ghcr.io/agenttier/sandbox-base:v1").
	Repository string `json:"repository"`

	// PullPolicy defines when to pull the image: Always, IfNotPresent, or Never.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`

	// PullSecret references an image pull secret for private registries.
	// +optional
	PullSecret string `json:"pullSecret,omitempty"`
}

// StorageSpec configures the persistent volume for the sandbox.
type StorageSpec struct {
	// Size of the PVC (e.g., "10Gi").
	// +kubebuilder:default="10Gi"
	// +optional
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClass name. Empty string uses the cluster default.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// MountPath inside the container where the PVC is mounted.
	// +kubebuilder:default="/workspace"
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// SnapshotOnStop creates a VolumeSnapshot before stopping the sandbox.
	// +optional
	SnapshotOnStop bool `json:"snapshotOnStop,omitempty"`

	// Shared enables ReadWriteMany access mode for inter-sandbox volume sharing.
	// Requires a storage class that supports RWX.
	// +optional
	Shared bool `json:"shared,omitempty"`
}

// NetworkSpec declares network isolation rules for the sandbox.
type NetworkSpec struct {
	// AllowInternet permits all egress traffic when true.
	// +optional
	AllowInternet bool `json:"allowInternet,omitempty"`

	// EgressRules defines allowed egress destinations.
	// +optional
	EgressRules []NetworkRule `json:"egressRules,omitempty"`

	// IngressRules defines allowed ingress sources.
	// +optional
	IngressRules []NetworkRule `json:"ingressRules,omitempty"`

	// AllowedDomains restricts egress to specific domain names.
	// Requires a DNS-aware CNI plugin (Calico, Cilium).
	// +optional
	AllowedDomains []string `json:"allowedDomains,omitempty"`

	// AllowPeerSandboxes enables inter-sandbox communication within the namespace.
	// +optional
	AllowPeerSandboxes bool `json:"allowPeerSandboxes,omitempty"`

	// PeerSandboxSelector selects which sandboxes can communicate with this one.
	// Only effective when AllowPeerSandboxes is true.
	// +optional
	PeerSandboxSelector *metav1.LabelSelector `json:"peerSandboxSelector,omitempty"`
}

// NetworkRule defines a single network access rule.
type NetworkRule struct {
	// CIDR block (e.g., "10.0.0.0/8", "0.0.0.0/0").
	// +optional
	CIDR string `json:"cidr,omitempty"`

	// Ports allowed for this rule.
	// +optional
	Ports []NetworkPort `json:"ports,omitempty"`

	// ServiceRef references a Kubernetes service as the destination.
	// +optional
	ServiceRef *ServiceReference `json:"serviceRef,omitempty"`
}

// NetworkPort defines a protocol and port number.
type NetworkPort struct {
	// Protocol (TCP or UDP). Defaults to TCP.
	// +kubebuilder:default=TCP
	// +optional
	Protocol corev1.Protocol `json:"protocol,omitempty"`

	// Port number.
	Port int32 `json:"port"`
}

// ServiceReference identifies a Kubernetes service.
type ServiceReference struct {
	// Name of the service.
	Name string `json:"name"`

	// Namespace of the service. Defaults to the sandbox namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Port on the service.
	// +optional
	Port int32 `json:"port,omitempty"`
}

// SecuritySpec configures pod security overrides.
type SecuritySpec struct {
	// Privileged relaxes the default restrictive security context.
	// Use with caution — only for sandboxes that require elevated privileges.
	// +optional
	Privileged bool `json:"privileged,omitempty"`
}

// CredentialRef references a Kubernetes Secret for credential injection.
type CredentialRef struct {
	// SecretName is the name of the Kubernetes Secret.
	SecretName string `json:"secretName"`

	// MountAs defines how to inject: "env" (environment variables) or "file" (volume mount).
	// +kubebuilder:validation:Enum=env;file
	// +kubebuilder:default=env
	// +optional
	MountAs string `json:"mountAs,omitempty"`

	// MountPath is the filesystem path for file-mounted credentials.
	// Only used when MountAs is "file".
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// EnvPrefix is prepended to secret keys when mounted as environment variables.
	// +optional
	EnvPrefix string `json:"envPrefix,omitempty"`
}

// UserIdentity stores the authenticated user's identity information.
type UserIdentity struct {
	// Sub is the OIDC subject identifier.
	Sub string `json:"sub"`

	// Email is the user's email address.
	// +optional
	Email string `json:"email,omitempty"`

	// DisplayName is the user's display name.
	// +optional
	DisplayName string `json:"displayName,omitempty"`
}

// SharingSpec defines access permissions for other users.
type SharingSpec struct {
	// Users granted access to this sandbox.
	// +optional
	Users []SharePermission `json:"users,omitempty"`

	// Groups granted access to this sandbox.
	// +optional
	Groups []SharePermission `json:"groups,omitempty"`

	// ShareLinks for temporary access without pre-registration.
	// +optional
	ShareLinks []ShareLink `json:"shareLinks,omitempty"`
}

// SharePermission grants access to a specific user or group.
type SharePermission struct {
	// Identity is the user email or group name.
	Identity string `json:"identity"`

	// Level is the permission level: "viewer" (read-only) or "collaborator" (full access).
	// +kubebuilder:validation:Enum=viewer;collaborator
	Level string `json:"level"`
}

// ShareLink provides temporary access via a shareable URL. Tokens are hashed
// before persistence — the raw token is returned exactly once at creation
// time via the create-link API response and never stored on the CR. Validators
// compare an incoming raw token against TokenHash with bcrypt.
type ShareLink struct {
	// ID is a stable, non-secret identifier for this share link, used for
	// revocation and audit. Unlike the legacy Token field, ID is safe to
	// log and surface in admin views.
	// +optional
	ID string `json:"id,omitempty"`

	// TokenHash is a bcrypt hash of the raw token. The raw token is
	// generated at create time, returned to the caller in the API
	// response, and never persisted in plaintext anywhere.
	// +optional
	TokenHash string `json:"tokenHash,omitempty"`

	// Token is the legacy plaintext token field. DEPRECATED: present only
	// for one minor release of backward compatibility while consumers
	// migrate to the create-response-only flow. New code must NOT set
	// this field. Validators read TokenHash; if Token is non-empty,
	// validators may also accept it as a fallback during the deprecation
	// window. Will be removed in the release after sharing GA.
	//
	// +optional
	// Deprecated: store TokenHash; surface raw tokens only via the create
	// API response.
	Token string `json:"token,omitempty"`

	// Level is the permission level granted by this link.
	// +kubebuilder:validation:Enum=viewer;collaborator
	Level string `json:"level"`

	// ExpiresAt is when the link expires.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// MaxUses is the maximum number of times this link can be used. 0 = unlimited.
	// +optional
	MaxUses int `json:"maxUses,omitempty"`

	// UsedCount tracks how many times the link has been used.
	// +optional
	UsedCount int `json:"usedCount,omitempty"`
}

// ForwardedPort represents an active port forward for the sandbox.
type ForwardedPort struct {
	// Port is the container port being forwarded.
	Port int32 `json:"port"`

	// PreviewURL is the publicly accessible HTTPS URL for this port.
	PreviewURL string `json:"previewUrl"`

	// Protocol is the application protocol (http, https, tcp).
	// +kubebuilder:default=http
	// +optional
	Protocol string `json:"protocol,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxList contains a list of Sandbox resources.
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Sandbox{}, &SandboxList{})
}
