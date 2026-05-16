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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// NetworkPolicyBuilder constructs Kubernetes NetworkPolicies for sandboxes.
type NetworkPolicyBuilder struct{}

// Build creates a NetworkPolicy for the given sandbox with the specified network configuration.
// Invariants:
// Build returns a NetworkPolicy for the sandbox. The user's NetworkSpec
// drives the rules; controller-side overlays (HTTP-exec ingress, future
// sandbox-runtime additions) are passed through `opts`.
//
// Spec semantics:
//   - Default deny-all egress (base policy)
//   - DNS (UDP+TCP port 53) is ALWAYS allowed regardless of other rules
//   - If allowInternet=true, all egress is permitted
//   - Inter-sandbox communication requires explicit opt-in
//   - When opts.AllowRouterIngressOn9000 is true, an ingress rule is
//     appended permitting any pod labeled
//     `app.kubernetes.io/component=router` in opts.RouterNamespace to
//     reach :9000 on the sandbox. Required for HTTP-exec routing.
func (b *NetworkPolicyBuilder) Build(sandbox *agenttierv1alpha1.Sandbox, networkSpec *agenttierv1alpha1.NetworkSpec, opts NetworkPolicyOptions) *networkingv1.NetworkPolicy {
	npName := fmt.Sprintf("%s-netpol", sandbox.Name)

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      npName,
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				"agenttier.io/sandbox": sandbox.Name,
				"agenttier.io/managed": "true",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"agenttier.io/sandbox": sandbox.Name,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
				networkingv1.PolicyTypeIngress,
			},
			Egress:  []networkingv1.NetworkPolicyEgressRule{},
			Ingress: []networkingv1.NetworkPolicyIngressRule{},
		},
	}

	// INVARIANT: Always allow DNS resolution (CoreDNS) — UDP and TCP port 53
	dnsRule := b.buildDNSRule()
	np.Spec.Egress = append(np.Spec.Egress, dnsRule)

	// Apply user-defined NetworkSpec rules. The early returns below
	// short-circuit *user-rule* logic but must NOT skip the controller-
	// managed overlays at the bottom (HTTP-exec ingress, future runtime
	// rules). Use a helper closure so all paths fall through to the
	// post-append step below.
	finalize := func() *networkingv1.NetworkPolicy {
		// HTTP-exec opt-in: append after user rules so it's always
		// present when requested, even if the user's NetworkSpec is
		// otherwise "default deny." Keyed on the standard component
		// label so the rule survives Router rollouts.
		if opts.AllowRouterIngressOn9000 {
			np.Spec.Ingress = append(np.Spec.Ingress, b.buildRouterIngressRule(opts.RouterNamespace))
		}
		return np
	}

	if networkSpec == nil {
		// No network spec — default deny-all (only DNS allowed).
		return finalize()
	}

	// If allowInternet is true, permit all egress.
	if networkSpec.AllowInternet {
		np.Spec.Egress = append(np.Spec.Egress, networkingv1.NetworkPolicyEgressRule{})
		return finalize()
	}

	// Add specific egress rules
	for _, rule := range networkSpec.EgressRules {
		egressRule := b.buildEgressRule(rule)
		np.Spec.Egress = append(np.Spec.Egress, egressRule)
	}

	// Add ingress rules
	for _, rule := range networkSpec.IngressRules {
		ingressRule := b.buildIngressRule(rule)
		np.Spec.Ingress = append(np.Spec.Ingress, ingressRule)
	}

	// Inter-sandbox communication
	if networkSpec.AllowPeerSandboxes {
		peerEgress, peerIngress := b.buildPeerRules(networkSpec)
		np.Spec.Egress = append(np.Spec.Egress, peerEgress)
		np.Spec.Ingress = append(np.Spec.Ingress, peerIngress)
	}

	return finalize()
}

// buildRouterIngressRule emits an ingress rule allowing any Router pod
// (matched by its standard `app.kubernetes.io/component=router` label) in
// the given namespace to dial :9000 on the sandbox. Used by the
// HTTP-exec opt-in path.
func (b *NetworkPolicyBuilder) buildRouterIngressRule(routerNamespace string) networkingv1.NetworkPolicyIngressRule {
	port := intstr.FromInt(9000)
	tcp := corev1.ProtocolTCP
	return networkingv1.NetworkPolicyIngressRule{
		From: []networkingv1.NetworkPolicyPeer{
			{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						// Every Kubernetes namespace carries this label
						// from K8s 1.21+; matching on it scopes the rule
						// to the install namespace cleanly.
						"kubernetes.io/metadata.name": routerNamespace,
					},
				},
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app.kubernetes.io/component": "router",
					},
				},
			},
		},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &tcp, Port: &port},
		},
	}
}

// buildDNSRule creates an egress rule that always allows DNS resolution.
func (b *NetworkPolicyBuilder) buildDNSRule() networkingv1.NetworkPolicyEgressRule {
	udp := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP
	dnsPort := intstr.FromInt(53)

	return networkingv1.NetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &udp, Port: &dnsPort},
			{Protocol: &tcp, Port: &dnsPort},
		},
	}
}

// buildEgressRule converts an AgentTier NetworkRule to a K8s NetworkPolicyEgressRule.
func (b *NetworkPolicyBuilder) buildEgressRule(rule agenttierv1alpha1.NetworkRule) networkingv1.NetworkPolicyEgressRule {
	egressRule := networkingv1.NetworkPolicyEgressRule{}

	// Add CIDR-based peer
	if rule.CIDR != "" {
		egressRule.To = append(egressRule.To, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: rule.CIDR},
		})
	}

	// Add service reference peer
	if rule.ServiceRef != nil {
		// Service references are resolved to pod selectors
		// This is a simplification — in production, you'd resolve the service endpoints
		egressRule.To = append(egressRule.To, networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{},
		})
	}

	// Add port restrictions
	for _, port := range rule.Ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		portVal := intstr.FromInt(int(port.Port))
		egressRule.Ports = append(egressRule.Ports, networkingv1.NetworkPolicyPort{
			Protocol: &protocol,
			Port:     &portVal,
		})
	}

	return egressRule
}

// buildIngressRule converts an AgentTier NetworkRule to a K8s NetworkPolicyIngressRule.
func (b *NetworkPolicyBuilder) buildIngressRule(rule agenttierv1alpha1.NetworkRule) networkingv1.NetworkPolicyIngressRule {
	ingressRule := networkingv1.NetworkPolicyIngressRule{}

	if rule.CIDR != "" {
		ingressRule.From = append(ingressRule.From, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: rule.CIDR},
		})
	}

	for _, port := range rule.Ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		portVal := intstr.FromInt(int(port.Port))
		ingressRule.Ports = append(ingressRule.Ports, networkingv1.NetworkPolicyPort{
			Protocol: &protocol,
			Port:     &portVal,
		})
	}

	return ingressRule
}

// buildPeerRules creates egress and ingress rules for inter-sandbox communication.
func (b *NetworkPolicyBuilder) buildPeerRules(networkSpec *agenttierv1alpha1.NetworkSpec) (networkingv1.NetworkPolicyEgressRule, networkingv1.NetworkPolicyIngressRule) {
	var peerSelector metav1.LabelSelector

	if networkSpec.PeerSandboxSelector != nil {
		peerSelector = *networkSpec.PeerSandboxSelector
	} else {
		// Default: allow all managed sandboxes in the namespace
		peerSelector = metav1.LabelSelector{
			MatchLabels: map[string]string{
				"agenttier.io/managed": "true",
			},
		}
	}

	egressRule := networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{
			{PodSelector: &peerSelector},
		},
	}

	ingressRule := networkingv1.NetworkPolicyIngressRule{
		From: []networkingv1.NetworkPolicyPeer{
			{PodSelector: &peerSelector},
		},
	}

	return egressRule, ingressRule
}

// ensureNetworkPolicy creates or updates the NetworkPolicy for a sandbox.
//
// Beyond the user's NetworkSpec rules, the controller appends an ingress
// rule allowing the Router pod to reach :9000 on the sandbox when
// HTTP-exec is opted in. The rule is keyed on the Router's
// well-known label (`app.kubernetes.io/component=router` set by the
// Helm chart) so it doesn't depend on a specific Router pod IP.
func (r *SandboxReconciler) ensureNetworkPolicy(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, networkSpec *agenttierv1alpha1.NetworkSpec, opts NetworkPolicyOptions) error {
	logger := log.FromContext(ctx)

	builder := &NetworkPolicyBuilder{}
	desired := builder.Build(sandbox, networkSpec, opts)

	// Set owner reference
	if err := controllerutil.SetControllerReference(sandbox, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on NetworkPolicy: %w", err)
	}

	// Check if NetworkPolicy already exists
	existing := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err == nil {
		// Update existing NetworkPolicy (mutable network rules)
		existing.Spec = desired.Spec
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update NetworkPolicy: %w", err)
		}
		logger.V(1).Info("updated NetworkPolicy", "name", existing.Name)
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing NetworkPolicy: %w", err)
	}

	// Create NetworkPolicy
	logger.Info("creating NetworkPolicy", "name", desired.Name)
	if err := r.Create(ctx, desired); err != nil {
		return fmt.Errorf("failed to create NetworkPolicy: %w", err)
	}

	return nil
}

// NetworkPolicyOptions are controller-side overlays applied on top of the
// user's NetworkSpec when building the NetworkPolicy. Today only the
// HTTP-exec ingress rule is here; future controller-managed rules go in
// the same struct so the call signature stays stable.
type NetworkPolicyOptions struct {
	// AllowRouterIngressOn9000, when true, permits the Router pod to
	// dial :9000 on the sandbox — required for HTTP-exec routing.
	AllowRouterIngressOn9000 bool
	// RouterNamespace is where the Router runs (same as InstallNamespace
	// on the reconciler). Used as the namespace selector for the ingress
	// rule above so the policy is portable across multi-namespace
	// installs.
	RouterNamespace string
}
