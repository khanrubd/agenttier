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

// Package governance resolves and enforces AgentTier governance policies
// (per-cluster and per-namespace limits) that control sandbox creation.
//
// Policies are stored as Kubernetes ConfigMaps so they persist across Router
// restarts and can be edited by admins through the Web UI Settings page. The
// same ConfigMap backend is used for the warm pool config — intentional, since
// the Router has no relational datastore and we don't want to require one for
// the common case.
package governance

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

const (
	// ConfigMapName is the name of the ConfigMap holding all governance policies.
	ConfigMapName = "agenttier-governance"
	// ConfigMapNamespace is the namespace of the governance ConfigMap.
	ConfigMapNamespace = "agenttier"
	// clusterPolicyKey is the reserved key inside the ConfigMap holding the
	// cluster-wide default policy.
	clusterPolicyKey = "__cluster__"
)

// Policy describes the governance limits that apply to a scope
// (cluster-wide or per-namespace). Zero or empty values mean "no limit".
type Policy struct {
	// MaxSandboxesPerUser caps how many sandboxes a single user may own
	// in this scope. 0 = unlimited.
	MaxSandboxesPerUser int `json:"maxSandboxesPerUser,omitempty"`

	// MaxSandboxesTotal caps the total sandboxes across all users in this
	// scope. 0 = unlimited.
	MaxSandboxesTotal int `json:"maxSandboxesTotal,omitempty"`

	// MaxCPU is the CPU limit per sandbox, as a Kubernetes resource quantity
	// (e.g. "4" or "2000m"). Empty = no limit.
	MaxCPU string `json:"maxCpu,omitempty"`

	// MaxMemory is the memory limit per sandbox (e.g. "8Gi"). Empty = no limit.
	MaxMemory string `json:"maxMemory,omitempty"`

	// MaxStorage is the PVC size limit per sandbox (e.g. "50Gi"). Empty = no limit.
	MaxStorage string `json:"maxStorage,omitempty"`

	// MaxTimeout caps the sandbox `spec.timeout` as a Go duration string
	// (e.g. "24h"). Empty = no limit.
	MaxTimeout string `json:"maxTimeout,omitempty"`

	// MaxIdleTimeout caps the sandbox `spec.idleTimeout` as a Go duration
	// string. Empty = no limit.
	MaxIdleTimeout string `json:"maxIdleTimeout,omitempty"`

	// AllowedTemplates restricts which `ClusterSandboxTemplate` names can be
	// used. Nil/empty = any template is allowed.
	AllowedTemplates []string `json:"allowedTemplates,omitempty"`

	// ApprovedRegistries restricts custom image overrides to these registry
	// prefixes (e.g. "ghcr.io/agenttier", "582483581248.dkr.ecr.us-east-1.amazonaws.com").
	// Nil/empty = any image is allowed.
	ApprovedRegistries []string `json:"approvedRegistries,omitempty"`

	// Description is a human-readable note shown in the UI.
	Description string `json:"description,omitempty"`
}

// IsEmpty reports whether the policy contains no effective limits.
func (p Policy) IsEmpty() bool {
	return p.MaxSandboxesPerUser == 0 &&
		p.MaxSandboxesTotal == 0 &&
		p.MaxCPU == "" &&
		p.MaxMemory == "" &&
		p.MaxStorage == "" &&
		p.MaxTimeout == "" &&
		p.MaxIdleTimeout == "" &&
		len(p.AllowedTemplates) == 0 &&
		len(p.ApprovedRegistries) == 0
}

// ScopedPolicy is a Policy with the scope it applies to. Scope is either
// an empty string (cluster default) or a namespace name.
type ScopedPolicy struct {
	Scope  string `json:"scope"`
	Policy Policy `json:"policy"`
}

// Store is the persistence backend for governance policies.
//
// We intentionally decouple the store from the enforcement engine so tests can
// swap in an in-memory implementation.
type Store interface {
	ListPolicies(ctx context.Context) ([]ScopedPolicy, error)
	GetPolicy(ctx context.Context, scope string) (Policy, error)
	SetPolicy(ctx context.Context, scope string, policy Policy) error
	DeletePolicy(ctx context.Context, scope string) error
}

// ConfigMapStore stores policies in a single Kubernetes ConfigMap.
type ConfigMapStore struct {
	Client client.Client
}

// NewConfigMapStore returns a Store backed by the ConfigMap at
// `agenttier/agenttier-governance`.
func NewConfigMapStore(c client.Client) *ConfigMapStore {
	return &ConfigMapStore{Client: c}
}

func (s *ConfigMapStore) load(ctx context.Context) (map[string]Policy, string, error) {
	cm := &corev1.ConfigMap{}
	err := s.Client.Get(ctx, types.NamespacedName{
		Name:      ConfigMapName,
		Namespace: ConfigMapNamespace,
	}, cm)
	if err != nil && !errors.IsNotFound(err) {
		return nil, "", fmt.Errorf("load governance configmap: %w", err)
	}

	policies := make(map[string]Policy)
	if cm.Data != nil {
		if raw, ok := cm.Data["policies"]; ok && raw != "" {
			if err := json.Unmarshal([]byte(raw), &policies); err != nil {
				return nil, "", fmt.Errorf("parse governance configmap: %w", err)
			}
		}
	}
	return policies, cm.ResourceVersion, nil
}

func (s *ConfigMapStore) save(ctx context.Context, policies map[string]Policy, resourceVersion string) error {
	raw, err := json.Marshal(policies)
	if err != nil {
		return fmt.Errorf("serialize governance policies: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: ConfigMapNamespace,
		},
	}
	if resourceVersion == "" {
		cm.Data = map[string]string{"policies": string(raw)}
		if err := s.Client.Create(ctx, cm); err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("create governance configmap: %w", err)
			}
			// Racing with another writer; fall through to update.
			if err := s.Client.Get(ctx, types.NamespacedName{Name: ConfigMapName, Namespace: ConfigMapNamespace}, cm); err != nil {
				return fmt.Errorf("reload governance configmap: %w", err)
			}
		} else {
			return nil
		}
	} else {
		cm.ResourceVersion = resourceVersion
	}

	cm.Data = map[string]string{"policies": string(raw)}
	if err := s.Client.Update(ctx, cm); err != nil {
		return fmt.Errorf("update governance configmap: %w", err)
	}
	return nil
}

// ListPolicies returns every policy stored, cluster-wide first.
func (s *ConfigMapStore) ListPolicies(ctx context.Context) ([]ScopedPolicy, error) {
	policies, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ScopedPolicy, 0, len(policies))
	if p, ok := policies[clusterPolicyKey]; ok {
		out = append(out, ScopedPolicy{Scope: "", Policy: p})
	}
	for scope, p := range policies {
		if scope == clusterPolicyKey {
			continue
		}
		out = append(out, ScopedPolicy{Scope: scope, Policy: p})
	}
	return out, nil
}

// GetPolicy returns the policy for a specific scope. Pass an empty string to
// fetch the cluster default. A missing policy returns an empty Policy (no error).
func (s *ConfigMapStore) GetPolicy(ctx context.Context, scope string) (Policy, error) {
	policies, _, err := s.load(ctx)
	if err != nil {
		return Policy{}, err
	}
	key := scope
	if key == "" {
		key = clusterPolicyKey
	}
	return policies[key], nil
}

// SetPolicy upserts a policy for the given scope.
func (s *ConfigMapStore) SetPolicy(ctx context.Context, scope string, policy Policy) error {
	policies, rv, err := s.load(ctx)
	if err != nil {
		return err
	}
	if policies == nil {
		policies = map[string]Policy{}
	}
	key := scope
	if key == "" {
		key = clusterPolicyKey
	}
	policies[key] = policy
	return s.save(ctx, policies, rv)
}

// DeletePolicy removes the policy for a scope. Deleting the cluster default
// resets to "no governance" (everything allowed).
func (s *ConfigMapStore) DeletePolicy(ctx context.Context, scope string) error {
	policies, rv, err := s.load(ctx)
	if err != nil {
		return err
	}
	key := scope
	if key == "" {
		key = clusterPolicyKey
	}
	delete(policies, key)
	return s.save(ctx, policies, rv)
}

// --- Effective-policy resolution ---

// Resolve returns the effective policy that applies to a namespace by merging
// the cluster default and the namespace-specific policy. Namespace policy
// values override cluster defaults field-by-field; empty values fall through.
func Resolve(ctx context.Context, store Store, namespace string) (Policy, error) {
	cluster, err := store.GetPolicy(ctx, "")
	if err != nil {
		return Policy{}, err
	}
	if namespace == "" {
		return cluster, nil
	}
	ns, err := store.GetPolicy(ctx, namespace)
	if err != nil {
		return Policy{}, err
	}
	return mergePolicies(cluster, ns), nil
}

func mergePolicies(parent, child Policy) Policy {
	out := parent
	if child.MaxSandboxesPerUser != 0 {
		out.MaxSandboxesPerUser = child.MaxSandboxesPerUser
	}
	if child.MaxSandboxesTotal != 0 {
		out.MaxSandboxesTotal = child.MaxSandboxesTotal
	}
	if child.MaxCPU != "" {
		out.MaxCPU = child.MaxCPU
	}
	if child.MaxMemory != "" {
		out.MaxMemory = child.MaxMemory
	}
	if child.MaxStorage != "" {
		out.MaxStorage = child.MaxStorage
	}
	if child.MaxTimeout != "" {
		out.MaxTimeout = child.MaxTimeout
	}
	if child.MaxIdleTimeout != "" {
		out.MaxIdleTimeout = child.MaxIdleTimeout
	}
	if len(child.AllowedTemplates) > 0 {
		out.AllowedTemplates = append([]string(nil), child.AllowedTemplates...)
	}
	if len(child.ApprovedRegistries) > 0 {
		out.ApprovedRegistries = append([]string(nil), child.ApprovedRegistries...)
	}
	if child.Description != "" {
		out.Description = child.Description
	}
	return out
}

// Usage reports current consumption for a scope — used to evaluate quota rules.
type Usage struct {
	TotalSandboxes int
	UserSandboxes  int
}

// CountUsage computes the current usage across a namespace from the provided
// sandbox list. Pass the caller's user sub so per-user counts are accurate.
func CountUsage(list *agenttierv1alpha1.SandboxList, userSub string) Usage {
	var u Usage
	for i := range list.Items {
		sb := list.Items[i]
		// Creating/Running/Stopped all count — only actively Deleting/Error do not.
		switch sb.Status.Phase {
		case agenttierv1alpha1.SandboxPhaseDeleting, agenttierv1alpha1.SandboxPhaseError:
			continue
		}
		u.TotalSandboxes++
		if sb.Spec.CreatedBy != nil && sb.Spec.CreatedBy.Sub == userSub {
			u.UserSandboxes++
		}
	}
	return u
}
