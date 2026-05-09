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

// Package governance implements policy enforcement for AgentTier.
package governance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/agenttier/agenttier/pkg/mongodb"
)

// Engine evaluates governance policies for sandbox operations.
type Engine struct {
	db *mongodb.Client
}

// NewEngine creates a new governance engine.
func NewEngine(db *mongodb.Client) *Engine {
	return &Engine{db: db}
}

// EffectivePolicy holds the resolved policy for a specific context.
type EffectivePolicy struct {
	MaxSandboxes         int
	MaxSandboxesPerUser  int
	MaxCPU               string
	MaxMemory            string
	MaxStorage           string
	MaxTimeout           time.Duration
	MaxIdleTimeout       time.Duration
	AllowInfiniteTimeout bool
	BurstRate            int
	SustainedRate        int
	ApprovedRegistries   []string
	AllowedTemplates     []string
	PortForwardingEnabled bool
	AllowedPorts         []mongodb.PortRange
}

// GetEffectivePolicy resolves the hierarchical policy for a namespace and user.
// Resolution order: cluster → namespace → user (most specific wins).
func (e *Engine) GetEffectivePolicy(ctx context.Context, namespace, userID string) (*EffectivePolicy, error) {
	// Start with cluster-wide defaults
	clusterPolicy, _ := e.db.GetPolicy(ctx, mongodb.PolicyScopeCluster, "", "")

	// Override with namespace policy
	nsPolicy, _ := e.db.GetPolicy(ctx, mongodb.PolicyScopeNamespace, namespace, "")

	// Override with user policy (most specific)
	userPolicy, _ := e.db.GetPolicy(ctx, mongodb.PolicyScopeUser, namespace, userID)

	// Merge: most restrictive limit wins
	effective := &EffectivePolicy{
		AllowInfiniteTimeout:  true, // Default: allow infinite
		PortForwardingEnabled: true, // Default: enabled
	}

	// Apply cluster policy
	if clusterPolicy != nil {
		applyPolicy(effective, clusterPolicy)
	}

	// Apply namespace policy (overrides cluster)
	if nsPolicy != nil {
		applyPolicy(effective, nsPolicy)
	}

	// Apply user policy (most specific)
	if userPolicy != nil {
		applyPolicy(effective, userPolicy)
	}

	return effective, nil
}

// CheckSandboxCreation validates that creating a new sandbox is allowed.
func (e *Engine) CheckSandboxCreation(ctx context.Context, namespace, userID string, requestedCPU, requestedMemory, requestedStorage string) error {
	policy, err := e.GetEffectivePolicy(ctx, namespace, userID)
	if err != nil {
		return fmt.Errorf("failed to get effective policy: %w", err)
	}

	// Check per-user sandbox count
	if policy.MaxSandboxesPerUser > 0 {
		// TODO: Count user's current sandboxes from K8s API
		// For now, this is a placeholder
	}

	// Check per-namespace sandbox count
	if policy.MaxSandboxes > 0 {
		// TODO: Count namespace sandboxes
	}

	return nil
}

// CheckImageRegistry validates that the requested image is from an approved registry.
func (e *Engine) CheckImageRegistry(ctx context.Context, namespace, image string) error {
	policy, err := e.GetEffectivePolicy(ctx, namespace, "")
	if err != nil {
		return err
	}

	if len(policy.ApprovedRegistries) == 0 {
		return nil // No registry restrictions
	}

	for _, registry := range policy.ApprovedRegistries {
		if strings.HasPrefix(image, registry) {
			return nil
		}
	}

	return fmt.Errorf("image %q is not from an approved registry (approved: %v)", image, policy.ApprovedRegistries)
}

// CheckTemplateAccess validates that the template is allowed in the namespace.
func (e *Engine) CheckTemplateAccess(ctx context.Context, namespace, templateName string) error {
	policy, err := e.GetEffectivePolicy(ctx, namespace, "")
	if err != nil {
		return err
	}

	if len(policy.AllowedTemplates) == 0 {
		return nil // No template restrictions
	}

	for _, allowed := range policy.AllowedTemplates {
		if allowed == templateName || allowed == "*" {
			return nil
		}
	}

	return fmt.Errorf("template %q is not allowed in namespace %q", templateName, namespace)
}

// CheckPortForwarding validates that port forwarding is allowed.
func (e *Engine) CheckPortForwarding(ctx context.Context, namespace string, port int) error {
	policy, err := e.GetEffectivePolicy(ctx, namespace, "")
	if err != nil {
		return err
	}

	if !policy.PortForwardingEnabled {
		return fmt.Errorf("port forwarding is disabled in namespace %q", namespace)
	}

	if len(policy.AllowedPorts) == 0 {
		return nil // No port restrictions
	}

	for _, pr := range policy.AllowedPorts {
		if port >= pr.Min && port <= pr.Max {
			return nil
		}
	}

	return fmt.Errorf("port %d is not in the allowed range for namespace %q", port, namespace)
}

// CheckRateLimit validates that the user hasn't exceeded creation rate limits.
func (e *Engine) CheckRateLimit(ctx context.Context, namespace, userID string) error {
	policy, err := e.GetEffectivePolicy(ctx, namespace, userID)
	if err != nil {
		return err
	}

	if policy.BurstRate == 0 && policy.SustainedRate == 0 {
		return nil // No rate limiting
	}

	// TODO: Count recent creations from audit_events
	// For now, this is a placeholder
	return nil
}

// EffectiveTimeout returns the effective timeout for a sandbox.
func (e *Engine) EffectiveTimeout(ctx context.Context, namespace, userID string) (maxTimeout, maxIdle time.Duration, allowsInfinite bool, err error) {
	policy, err := e.GetEffectivePolicy(ctx, namespace, userID)
	if err != nil {
		return 0, 0, true, err
	}

	return policy.MaxTimeout, policy.MaxIdleTimeout, policy.AllowInfiniteTimeout, nil
}

// applyPolicy merges a policy into the effective policy (most restrictive wins).
func applyPolicy(effective *EffectivePolicy, policy *mongodb.GovernancePolicy) {
	limits := policy.Limits

	if limits.MaxSandboxes > 0 {
		if effective.MaxSandboxes == 0 || limits.MaxSandboxes < effective.MaxSandboxes {
			effective.MaxSandboxes = limits.MaxSandboxes
		}
	}
	if limits.MaxSandboxesPerUser > 0 {
		if effective.MaxSandboxesPerUser == 0 || limits.MaxSandboxesPerUser < effective.MaxSandboxesPerUser {
			effective.MaxSandboxesPerUser = limits.MaxSandboxesPerUser
		}
	}
	if limits.MaxCPU != "" {
		effective.MaxCPU = limits.MaxCPU
	}
	if limits.MaxMemory != "" {
		effective.MaxMemory = limits.MaxMemory
	}
	if limits.MaxStorage != "" {
		effective.MaxStorage = limits.MaxStorage
	}
	if limits.MaxTimeout != "" {
		d, err := time.ParseDuration(limits.MaxTimeout)
		if err == nil && (effective.MaxTimeout == 0 || d < effective.MaxTimeout) {
			effective.MaxTimeout = d
		}
	}
	if limits.MaxIdleTimeout != "" {
		d, err := time.ParseDuration(limits.MaxIdleTimeout)
		if err == nil && (effective.MaxIdleTimeout == 0 || d < effective.MaxIdleTimeout) {
			effective.MaxIdleTimeout = d
		}
	}
	// AllowInfiniteTimeout: false overrides true (more restrictive)
	if !limits.AllowInfiniteTimeout {
		effective.AllowInfiniteTimeout = false
	}

	// Rate limits
	if policy.RateLimit.BurstRate > 0 {
		effective.BurstRate = policy.RateLimit.BurstRate
	}
	if policy.RateLimit.SustainedRate > 0 {
		effective.SustainedRate = policy.RateLimit.SustainedRate
	}

	// Approved registries (override, not merge)
	if len(policy.ApprovedRegistries) > 0 {
		effective.ApprovedRegistries = policy.ApprovedRegistries
	}

	// Allowed templates (override, not merge)
	if len(policy.AllowedTemplates) > 0 {
		effective.AllowedTemplates = policy.AllowedTemplates
	}

	// Port forwarding
	if policy.PortForwardingEnabled != nil {
		effective.PortForwardingEnabled = *policy.PortForwardingEnabled
	}
	if len(policy.AllowedPorts) > 0 {
		effective.AllowedPorts = policy.AllowedPorts
	}
}
