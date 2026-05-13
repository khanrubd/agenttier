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
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

const (
	// MaxInheritanceDepth is the maximum depth for template inheritance chains.
	MaxInheritanceDepth = 10
)

// TemplateResolver resolves and validates SandboxTemplate references.
type TemplateResolver struct {
	Client client.Client
}

// ResolvedTemplate holds the resolved template spec and metadata.
type ResolvedTemplate struct {
	Name            string
	ResourceVersion string
	Spec            agenttierv1alpha1.SandboxTemplateSpec
}

// Resolve looks up the referenced template and resolves its full inheritance chain.
// Returns the fully merged template spec (all ancestors merged bottom-up).
func (tr *TemplateResolver) Resolve(ctx context.Context, ref *agenttierv1alpha1.TemplateReference, sandboxNamespace string) (*ResolvedTemplate, error) {
	logger := log.FromContext(ctx)

	if ref == nil {
		return nil, nil // No template reference — use defaults only
	}

	// Resolve the inheritance chain
	chain, err := tr.resolveChain(ctx, ref, sandboxNamespace, 0, make(map[string]bool))
	if err != nil {
		return nil, err
	}

	if len(chain) == 0 {
		return nil, fmt.Errorf("template resolution produced empty chain")
	}

	// The last element is the directly referenced template
	directTemplate := chain[len(chain)-1]

	// Merge the chain bottom-up (root ancestor first, direct template last)
	merged := tr.mergeChain(chain)

	logger.Info("resolved template", "name", directTemplate.Name, "chainDepth", len(chain))

	return &ResolvedTemplate{
		Name:            directTemplate.Name,
		ResourceVersion: directTemplate.ResourceVersion,
		Spec:            merged,
	}, nil
}

// resolveChain recursively resolves the template inheritance chain.
// Returns templates in order: [root ancestor, ..., parent, child].
func (tr *TemplateResolver) resolveChain(ctx context.Context, ref *agenttierv1alpha1.TemplateReference, sandboxNamespace string, depth int, visited map[string]bool) ([]resolvedEntry, error) {
	if depth >= MaxInheritanceDepth {
		return nil, fmt.Errorf("template inheritance chain exceeds maximum depth of %d", MaxInheritanceDepth)
	}

	// Build unique key for circular reference detection
	key := fmt.Sprintf("%s/%s/%s", ref.Kind, ref.Namespace, ref.Name)
	if visited[key] {
		return nil, fmt.Errorf("circular template inheritance detected: %s", key)
	}
	visited[key] = true

	// Fetch the template
	spec, resourceVersion, err := tr.fetchTemplate(ctx, ref, sandboxNamespace)
	if err != nil {
		return nil, err
	}

	entry := resolvedEntry{
		Name:            ref.Name,
		ResourceVersion: resourceVersion,
		Spec:            *spec,
	}

	// If this template inherits from another, resolve the parent first
	if spec.InheritsFrom != nil {
		parentChain, err := tr.resolveChain(ctx, spec.InheritsFrom, sandboxNamespace, depth+1, visited)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve parent template %s: %w", spec.InheritsFrom.Name, err)
		}
		// Parent chain + this template
		return append(parentChain, entry), nil
	}

	// Base case: no parent
	return []resolvedEntry{entry}, nil
}

// fetchTemplate retrieves a template from the Kubernetes API.
func (tr *TemplateResolver) fetchTemplate(ctx context.Context, ref *agenttierv1alpha1.TemplateReference, sandboxNamespace string) (*agenttierv1alpha1.SandboxTemplateSpec, string, error) {
	kind := ref.Kind
	if kind == "" {
		kind = "SandboxTemplate"
	}

	switch kind {
	case "SandboxTemplate":
		ns := ref.Namespace
		if ns == "" {
			ns = sandboxNamespace
		}
		template := &agenttierv1alpha1.SandboxTemplate{}
		err := tr.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, template)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, "", fmt.Errorf("SandboxTemplate %s/%s not found", ns, ref.Name)
			}
			return nil, "", fmt.Errorf("failed to get SandboxTemplate %s/%s: %w", ns, ref.Name, err)
		}
		return &template.Spec, template.ResourceVersion, nil

	case "ClusterSandboxTemplate":
		template := &agenttierv1alpha1.ClusterSandboxTemplate{}
		err := tr.Client.Get(ctx, client.ObjectKey{Name: ref.Name}, template)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, "", fmt.Errorf("ClusterSandboxTemplate %s not found", ref.Name)
			}
			return nil, "", fmt.Errorf("failed to get ClusterSandboxTemplate %s: %w", ref.Name, err)
		}
		return &template.Spec, template.ResourceVersion, nil

	default:
		return nil, "", fmt.Errorf("unknown template kind: %s", kind)
	}
}

// resolvedEntry holds a single template in the inheritance chain.
type resolvedEntry struct {
	Name            string
	ResourceVersion string
	Spec            agenttierv1alpha1.SandboxTemplateSpec
}

// mergeChain merges the template chain bottom-up.
// Order: [root, ..., parent, child] — later entries override earlier ones.
func (tr *TemplateResolver) mergeChain(chain []resolvedEntry) agenttierv1alpha1.SandboxTemplateSpec {
	if len(chain) == 0 {
		return agenttierv1alpha1.SandboxTemplateSpec{}
	}
	if len(chain) == 1 {
		return chain[0].Spec
	}

	// Start with root ancestor
	merged := chain[0].Spec

	// Merge each subsequent template on top
	for i := 1; i < len(chain); i++ {
		merged = mergeTemplateSpecs(merged, chain[i].Spec)
	}

	return merged
}

// mergeTemplateSpecs merges child template spec over parent template spec.
// Child fields take precedence over parent fields.
// Env vars, sidecars, initContainers, credentials, files are additive.
func mergeTemplateSpecs(parent, child agenttierv1alpha1.SandboxTemplateSpec) agenttierv1alpha1.SandboxTemplateSpec {
	result := parent

	// Scalar overrides: child wins if non-nil/non-empty
	if child.Image != nil {
		result.Image = child.Image
	}
	if child.Resources != nil {
		result.Resources = child.Resources
	}
	if child.Storage != nil {
		result.Storage = child.Storage
	}
	if child.Network != nil {
		result.Network = child.Network
	}
	if child.Timeout != nil {
		result.Timeout = child.Timeout
	}
	if child.IdleTimeout != nil {
		result.IdleTimeout = child.IdleTimeout
	}
	if child.RuntimeClass != nil {
		result.RuntimeClass = child.RuntimeClass
	}
	if child.Security != nil {
		result.Security = child.Security
	}
	if child.Description != "" {
		result.Description = child.Description
	}
	if child.Mode != "" {
		result.Mode = child.Mode
	}

	// Harness: deep merge
	if child.Harness != nil {
		if result.Harness == nil {
			result.Harness = child.Harness
		} else {
			result.Harness = mergeHarness(result.Harness, child.Harness)
		}
	}

	// Additive fields: combine both, child values win on key conflicts
	result.Env = mergeEnvVars(result.Env, child.Env)
	result.Sidecars = append(result.Sidecars, child.Sidecars...)
	result.InitContainers = append(result.InitContainers, child.InitContainers...)
	result.Credentials = append(result.Credentials, child.Credentials...)
	result.Files = mergeFiles(result.Files, child.Files)

	// InitScripts: concatenated (parent first, child after)
	result.InitScripts = append(result.InitScripts, child.InitScripts...)

	return result
}

// mergeHarness deep-merges harness specs. Child fields override parent fields.
func mergeHarness(parent, child *agenttierv1alpha1.HarnessSpec) *agenttierv1alpha1.HarnessSpec {
	result := *parent

	if len(child.Command) > 0 {
		result.Command = child.Command
	}
	if len(child.Args) > 0 {
		result.Args = child.Args
	}
	if child.WorkingDir != "" {
		result.WorkingDir = child.WorkingDir
	}
	if child.Shell != "" {
		result.Shell = child.Shell
	}
	if child.SystemPrompt != nil {
		result.SystemPrompt = child.SystemPrompt
	}
	if child.Hooks != nil {
		result.Hooks = child.Hooks
	}
	if child.Constraints != nil {
		result.Constraints = child.Constraints
	}
	if child.Agent != nil {
		if result.Agent == nil {
			result.Agent = child.Agent
		} else {
			result.Agent = mergeAgent(result.Agent, child.Agent)
		}
	}

	// Tools and skills are additive
	result.Tools = append(result.Tools, child.Tools...)
	result.Skills = append(result.Skills, child.Skills...)

	return &result
}

// mergeAgent deep-merges two AgentSpec values. Child fields override parent;
// Env is additive (mergeEnvVars handles key conflicts the same way as the
// rest of the template merge logic).
func mergeAgent(parent, child *agenttierv1alpha1.AgentSpec) *agenttierv1alpha1.AgentSpec {
	result := *parent
	if len(child.Entrypoint) > 0 {
		result.Entrypoint = child.Entrypoint
	}
	if len(child.InstallCommand) > 0 {
		result.InstallCommand = child.InstallCommand
	}
	if child.WorkingDir != "" {
		result.WorkingDir = child.WorkingDir
	}
	if child.MaxConcurrentInvokes != nil {
		result.MaxConcurrentInvokes = child.MaxConcurrentInvokes
	}
	if child.DefaultInvokeTimeout != nil {
		result.DefaultInvokeTimeout = child.DefaultInvokeTimeout
	}
	result.Env = mergeEnvVars(result.Env, child.Env)
	return &result
}

// mergeEnvVars merges environment variables. Later values win on key conflicts.
func mergeEnvVars(parent, child []corev1.EnvVar) []corev1.EnvVar {
	if len(child) == 0 {
		return parent
	}
	if len(parent) == 0 {
		return child
	}

	// Build map from parent
	envMap := make(map[string]corev1.EnvVar, len(parent)+len(child))
	order := make([]string, 0, len(parent)+len(child))

	for _, e := range parent {
		if _, exists := envMap[e.Name]; !exists {
			order = append(order, e.Name)
		}
		envMap[e.Name] = e
	}

	// Child overrides parent on conflict, adds new keys
	for _, e := range child {
		if _, exists := envMap[e.Name]; !exists {
			order = append(order, e.Name)
		}
		envMap[e.Name] = e
	}

	// Rebuild ordered slice
	result := make([]corev1.EnvVar, 0, len(order))
	for _, name := range order {
		result = append(result, envMap[name])
	}
	return result
}

// mergeFiles merges file specs. Later paths win on conflict.
func mergeFiles(parent, child []agenttierv1alpha1.FileSpec) []agenttierv1alpha1.FileSpec {
	if len(child) == 0 {
		return parent
	}
	if len(parent) == 0 {
		return child
	}

	fileMap := make(map[string]agenttierv1alpha1.FileSpec, len(parent)+len(child))
	order := make([]string, 0, len(parent)+len(child))

	for _, f := range parent {
		if _, exists := fileMap[f.Path]; !exists {
			order = append(order, f.Path)
		}
		fileMap[f.Path] = f
	}

	for _, f := range child {
		if _, exists := fileMap[f.Path]; !exists {
			order = append(order, f.Path)
		}
		fileMap[f.Path] = f
	}

	result := make([]agenttierv1alpha1.FileSpec, 0, len(order))
	for _, path := range order {
		result = append(result, fileMap[path])
	}
	return result
}

// MergeSandboxWithTemplate merges a sandbox spec over a resolved template spec,
// then fills remaining gaps with controller defaults.
func MergeSandboxWithTemplate(sandbox *agenttierv1alpha1.SandboxSpec, template *agenttierv1alpha1.SandboxTemplateSpec, defaults *ControllerDefaults) *MergedPodConfig {
	config := &MergedPodConfig{
		MountPath: defaultMountPath,
		Shell:     defaultShell,
	}

	// Image: sandbox > template > default
	if sandbox.Image != nil {
		config.Image = sandbox.Image.Repository
		config.ImagePullPolicy = sandbox.Image.PullPolicy
		config.ImagePullSecret = sandbox.Image.PullSecret
	} else if template != nil && template.Image != nil {
		config.Image = template.Image.Repository
		config.ImagePullPolicy = template.Image.PullPolicy
		config.ImagePullSecret = template.Image.PullSecret
	} else if defaults != nil {
		config.Image = defaults.Image
	}

	// Resources: sandbox > template > default
	if sandbox.Resources != nil {
		config.Resources = sandbox.Resources
	} else if template != nil && template.Resources != nil {
		config.Resources = template.Resources
	} else if defaults != nil {
		config.Resources = defaults.Resources
	}

	// Storage mount path: sandbox > template > default
	if sandbox.Storage != nil && sandbox.Storage.MountPath != "" {
		config.MountPath = sandbox.Storage.MountPath
	} else if template != nil && template.Storage != nil && template.Storage.MountPath != "" {
		config.MountPath = template.Storage.MountPath
	}

	// RuntimeClass: sandbox > template
	if sandbox.RuntimeClass != nil {
		config.RuntimeClass = sandbox.RuntimeClass
	} else if template != nil && template.RuntimeClass != nil {
		config.RuntimeClass = template.RuntimeClass
	}

	// Security: sandbox > template
	if sandbox.Security != nil {
		config.Privileged = sandbox.Security.Privileged
	} else if template != nil && template.Security != nil {
		config.Privileged = template.Security.Privileged
	}

	// Harness fields
	if template != nil && template.Harness != nil {
		h := template.Harness
		config.Command = h.Command
		config.Args = h.Args
		if h.WorkingDir != "" {
			config.WorkingDir = h.WorkingDir
		}
		if h.Shell != "" {
			config.Shell = h.Shell
		}
	}

	// Env vars: merge template + sandbox (sandbox wins on conflict)
	var templateEnv []corev1.EnvVar
	if template != nil {
		templateEnv = template.Env
	}
	config.Env = mergeEnvVars(templateEnv, sandbox.Env)

	// Sidecars: template + sandbox (additive)
	if template != nil {
		config.Sidecars = append(config.Sidecars, template.Sidecars...)
	}
	config.Sidecars = append(config.Sidecars, sandbox.Sidecars...)

	// InitContainers: template + sandbox (additive)
	if template != nil {
		config.InitContainers = append(config.InitContainers, template.InitContainers...)
	}
	config.InitContainers = append(config.InitContainers, sandbox.InitContainers...)

	// Credentials: template + sandbox (additive)
	if template != nil {
		config.Credentials = append(config.Credentials, template.Credentials...)
	}
	config.Credentials = append(config.Credentials, sandbox.Credentials...)

	// Template-only fields
	if template != nil {
		config.InitScripts = template.InitScripts
		config.Files = template.Files
	}

	return config
}

// ControllerDefaults holds the controller-level default configuration.
type ControllerDefaults struct {
	Image     string
	Resources *corev1.ResourceRequirements
	Storage   string
	MountPath string
}
