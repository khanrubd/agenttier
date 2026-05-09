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

// Package credentials implements credential injection for AgentTier sandboxes.
package credentials

import (
	"context"
	"fmt"
)

// Provider is the interface for credential injection mechanisms.
type Provider interface {
	// Name returns the provider name (e.g., "irsa", "workload-identity", "secret").
	Name() string

	// GetSessionCredentials returns temporary credentials for a terminal session.
	// These are injected as environment variables into the exec session.
	GetSessionCredentials(ctx context.Context, config *CredentialConfig) (map[string]string, error)
}

// CredentialConfig holds the configuration for credential injection.
type CredentialConfig struct {
	// Type: "irsa", "workload-identity", "secret", "custom"
	Type string

	// RoleARN for IRSA (EKS)
	RoleARN string

	// ServiceAccount for Workload Identity (GKE)
	GCPServiceAccount string

	// SecretName for Kubernetes Secret-based credentials
	SecretName string

	// Namespace of the sandbox
	Namespace string
}

// Registry holds all registered credential providers.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new credential provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry.
func (r *Registry) Register(provider Provider) {
	r.providers[provider.Name()] = provider
}

// GetSessionCredentials fetches credentials from the appropriate provider.
func (r *Registry) GetSessionCredentials(ctx context.Context, config *CredentialConfig) (map[string]string, error) {
	provider, ok := r.providers[config.Type]
	if !ok {
		return nil, fmt.Errorf("unknown credential provider type: %s", config.Type)
	}
	return provider.GetSessionCredentials(ctx, config)
}

// --- IRSA Provider (EKS) ---

// IRSAProvider injects AWS credentials via IAM Roles for Service Accounts.
type IRSAProvider struct{}

func (p *IRSAProvider) Name() string { return "irsa" }

func (p *IRSAProvider) GetSessionCredentials(ctx context.Context, config *CredentialConfig) (map[string]string, error) {
	// In production: call STS AssumeRoleWithWebIdentity
	// The pod already has credentials via IRSA — for session injection,
	// we'd generate short-lived STS tokens scoped to the session.
	// For now, return the role ARN as a reference.
	return map[string]string{
		"AWS_ROLE_ARN": config.RoleARN,
		// AWS_WEB_IDENTITY_TOKEN_FILE is auto-injected by EKS webhook
	}, nil
}

// --- Workload Identity Provider (GKE) ---

// WorkloadIdentityProvider injects GCP credentials via Workload Identity.
type WorkloadIdentityProvider struct{}

func (p *WorkloadIdentityProvider) Name() string { return "workload-identity" }

func (p *WorkloadIdentityProvider) GetSessionCredentials(ctx context.Context, config *CredentialConfig) (map[string]string, error) {
	// GKE Workload Identity auto-injects credentials into the pod.
	// For session injection, we reference the service account.
	return map[string]string{
		"GOOGLE_APPLICATION_CREDENTIALS": "/var/run/secrets/gcp/key.json",
	}, nil
}

// --- Secret Provider ---

// SecretProvider injects credentials from Kubernetes Secrets.
type SecretProvider struct{}

func (p *SecretProvider) Name() string { return "secret" }

func (p *SecretProvider) GetSessionCredentials(ctx context.Context, config *CredentialConfig) (map[string]string, error) {
	// In production: read the Secret from K8s API and return key-value pairs.
	// Credentials are already mounted in the pod via envFrom or volume.
	// For per-session injection, we'd read fresh values from the Secret.
	return map[string]string{
		"_CREDENTIAL_SOURCE": config.SecretName,
	}, nil
}
