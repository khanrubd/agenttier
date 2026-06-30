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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func newTestSandbox(name string) *agenttierv1alpha1.Sandbox {
	return &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
	}
}

func TestNetworkPolicyBuilder_DefaultDenyAll(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("test-sandbox")

	np := builder.Build(sandbox, nil, NetworkPolicyOptions{})

	// Should have exactly 1 egress rule (DNS only)
	if len(np.Spec.Egress) != 1 {
		t.Fatalf("expected 1 egress rule (DNS), got %d", len(np.Spec.Egress))
	}

	// Verify DNS rule has UDP and TCP port 53
	dnsRule := np.Spec.Egress[0]
	if len(dnsRule.Ports) != 2 {
		t.Fatalf("expected 2 DNS ports (UDP+TCP), got %d", len(dnsRule.Ports))
	}

	hasUDP := false
	hasTCP := false
	for _, port := range dnsRule.Ports {
		if *port.Protocol == corev1.ProtocolUDP && port.Port.IntValue() == 53 {
			hasUDP = true
		}
		if *port.Protocol == corev1.ProtocolTCP && port.Port.IntValue() == 53 {
			hasTCP = true
		}
	}
	if !hasUDP {
		t.Error("expected UDP port 53 in DNS rule")
	}
	if !hasTCP {
		t.Error("expected TCP port 53 in DNS rule")
	}

	// Should have no ingress rules
	if len(np.Spec.Ingress) != 0 {
		t.Errorf("expected 0 ingress rules, got %d", len(np.Spec.Ingress))
	}
}

func TestNetworkPolicyBuilder_DNSAlwaysAllowed(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("test-sandbox")

	// Even with custom egress rules, DNS must still be allowed
	networkSpec := &agenttierv1alpha1.NetworkSpec{
		EgressRules: []agenttierv1alpha1.NetworkRule{
			{CIDR: "10.0.0.0/8"},
		},
	}

	np := builder.Build(sandbox, networkSpec, NetworkPolicyOptions{})

	// First rule should always be DNS
	if len(np.Spec.Egress) < 1 {
		t.Fatal("expected at least 1 egress rule")
	}

	dnsRule := np.Spec.Egress[0]
	if len(dnsRule.Ports) != 2 {
		t.Fatalf("expected DNS rule as first egress rule with 2 ports, got %d ports", len(dnsRule.Ports))
	}
}

func TestNetworkPolicyBuilder_AllowInternet(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("test-sandbox")

	networkSpec := &agenttierv1alpha1.NetworkSpec{
		AllowInternet: true,
	}

	np := builder.Build(sandbox, networkSpec, NetworkPolicyOptions{})

	// Should have DNS rule + allow-all rule
	if len(np.Spec.Egress) != 2 {
		t.Fatalf("expected 2 egress rules (DNS + allow-all), got %d", len(np.Spec.Egress))
	}

	// Second rule should be empty (allow all)
	allowAll := np.Spec.Egress[1]
	if len(allowAll.To) != 0 && len(allowAll.Ports) != 0 {
		t.Error("expected empty egress rule (allow all)")
	}
}

func TestNetworkPolicyBuilder_CIDRRule(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("test-sandbox")

	networkSpec := &agenttierv1alpha1.NetworkSpec{
		EgressRules: []agenttierv1alpha1.NetworkRule{
			{
				CIDR: "10.0.0.0/8",
				Ports: []agenttierv1alpha1.NetworkPort{
					{Protocol: corev1.ProtocolTCP, Port: 443},
				},
			},
		},
	}

	np := builder.Build(sandbox, networkSpec, NetworkPolicyOptions{})

	// DNS + CIDR rule
	if len(np.Spec.Egress) != 2 {
		t.Fatalf("expected 2 egress rules, got %d", len(np.Spec.Egress))
	}

	cidrRule := np.Spec.Egress[1]
	if len(cidrRule.To) != 1 || cidrRule.To[0].IPBlock == nil {
		t.Fatal("expected CIDR-based peer")
	}
	if cidrRule.To[0].IPBlock.CIDR != "10.0.0.0/8" {
		t.Errorf("expected CIDR 10.0.0.0/8, got %s", cidrRule.To[0].IPBlock.CIDR)
	}
	if len(cidrRule.Ports) != 1 || cidrRule.Ports[0].Port.IntValue() != 443 {
		t.Error("expected port 443")
	}
}

func TestNetworkPolicyBuilder_PeerSandboxes(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("test-sandbox")

	networkSpec := &agenttierv1alpha1.NetworkSpec{
		AllowPeerSandboxes: true,
	}

	np := builder.Build(sandbox, networkSpec, NetworkPolicyOptions{})

	// DNS + peer egress
	if len(np.Spec.Egress) != 2 {
		t.Fatalf("expected 2 egress rules (DNS + peer), got %d", len(np.Spec.Egress))
	}

	// Should have peer ingress
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule (peer), got %d", len(np.Spec.Ingress))
	}

	// Verify peer selector uses managed label
	peerIngress := np.Spec.Ingress[0]
	if len(peerIngress.From) != 1 || peerIngress.From[0].PodSelector == nil {
		t.Fatal("expected pod selector in peer ingress")
	}
	labels := peerIngress.From[0].PodSelector.MatchLabels
	if labels["agenttier.io/managed"] != "true" {
		t.Error("expected agenttier.io/managed=true label selector")
	}
}

func TestNetworkPolicyBuilder_PeerSandboxesCustomSelector(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("test-sandbox")

	// A custom selector restricts peering to sandboxes carrying a team label
	// instead of the default "all managed sandboxes in the namespace".
	networkSpec := &agenttierv1alpha1.NetworkSpec{
		AllowPeerSandboxes: true,
		PeerSandboxSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"team": "blue"},
		},
	}

	np := builder.Build(sandbox, networkSpec, NetworkPolicyOptions{})

	if len(np.Spec.Egress) != 2 || len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected DNS+peer egress (2) and peer ingress (1), got egress=%d ingress=%d",
			len(np.Spec.Egress), len(np.Spec.Ingress))
	}
	for _, dir := range []struct {
		name string
		sel  *metav1.LabelSelector
	}{
		{"ingress", np.Spec.Ingress[0].From[0].PodSelector},
		{"egress", np.Spec.Egress[1].To[0].PodSelector},
	} {
		if dir.sel == nil {
			t.Fatalf("%s: expected a pod selector", dir.name)
		}
		if dir.sel.MatchLabels["team"] != "blue" {
			t.Errorf("%s: expected custom team=blue selector, got %v", dir.name, dir.sel.MatchLabels)
		}
		if _, hasManaged := dir.sel.MatchLabels["agenttier.io/managed"]; hasManaged {
			t.Errorf("%s: custom selector must replace, not merge with, the default managed selector", dir.name)
		}
	}
}

func TestNetworkPolicyBuilder_PodSelector(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("my-sandbox")

	np := builder.Build(sandbox, nil, NetworkPolicyOptions{})

	// Verify pod selector targets this specific sandbox
	labels := np.Spec.PodSelector.MatchLabels
	if labels["agenttier.io/sandbox"] != "my-sandbox" {
		t.Errorf("expected pod selector for my-sandbox, got %v", labels)
	}
}

func TestNetworkPolicyBuilder_IngressRules(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("test-sandbox")

	networkSpec := &agenttierv1alpha1.NetworkSpec{
		IngressRules: []agenttierv1alpha1.NetworkRule{
			{
				CIDR: "192.168.1.0/24",
				Ports: []agenttierv1alpha1.NetworkPort{
					{Protocol: corev1.ProtocolTCP, Port: 8080},
				},
			},
		},
	}

	np := builder.Build(sandbox, networkSpec, NetworkPolicyOptions{})

	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(np.Spec.Ingress))
	}

	ingressRule := np.Spec.Ingress[0]
	if len(ingressRule.From) != 1 || ingressRule.From[0].IPBlock.CIDR != "192.168.1.0/24" {
		t.Error("expected CIDR 192.168.1.0/24 in ingress")
	}
}

// TestNetworkPolicyBuilder_HTTPExecIngressRule verifies that when
// NetworkPolicyOptions.AllowRouterIngressOn9000 is true, the resulting
// policy includes an ingress rule scoped to:
//   - the Router namespace via kubernetes.io/metadata.name
//   - the Router pod via app.kubernetes.io/component=router
//   - TCP port 9000 only
//
// This is what gates the in-pod runtime — without it, NetworkPolicy
// blocks the Router from reaching :9000 and HTTP-exec dies with a
// connection-refused before the Bearer token even matters.
func TestNetworkPolicyBuilder_HTTPExecIngressRule(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("sbx-1")

	np := builder.Build(sandbox, nil, NetworkPolicyOptions{
		AllowRouterIngressOn9000: true,
		RouterNamespace:          "agenttier",
	})

	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d (rules=%+v)", len(np.Spec.Ingress), np.Spec.Ingress)
	}
	rule := np.Spec.Ingress[0]
	if len(rule.From) != 1 {
		t.Fatalf("expected 1 From peer, got %d", len(rule.From))
	}
	from := rule.From[0]
	if from.NamespaceSelector == nil || from.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "agenttier" {
		t.Errorf("namespace selector wrong: %+v", from.NamespaceSelector)
	}
	if from.PodSelector == nil || from.PodSelector.MatchLabels["app.kubernetes.io/component"] != "router" {
		t.Errorf("pod selector wrong: %+v", from.PodSelector)
	}
	if len(rule.Ports) != 1 || rule.Ports[0].Port == nil || rule.Ports[0].Port.IntValue() != 9000 {
		t.Errorf("expected port 9000, got %+v", rule.Ports)
	}
}

// TestNetworkPolicyBuilder_HTTPExecOptOutNoIngress — symmetric: when the
// flag is false, the ingress rule must NOT be present even on a sandbox
// with no other ingress rules.
func TestNetworkPolicyBuilder_HTTPExecOptOutNoIngress(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("sbx-1")

	np := builder.Build(sandbox, nil, NetworkPolicyOptions{
		AllowRouterIngressOn9000: false, // explicit opt-out
	})

	if len(np.Spec.Ingress) != 0 {
		t.Errorf("expected 0 ingress rules, got %d", len(np.Spec.Ingress))
	}
}

// TestNetworkPolicyBuilder_HTTPExecAppliesEvenWithAllowInternet covers a
// subtle bug class: the AllowInternet branch used to early-return before
// any HTTP-exec overlay could be appended. The fix uses a finalize()
// closure so the rule is added on every code path — verify that here.
func TestNetworkPolicyBuilder_HTTPExecAppliesEvenWithAllowInternet(t *testing.T) {
	builder := &NetworkPolicyBuilder{}
	sandbox := newTestSandbox("sbx-1")

	np := builder.Build(sandbox,
		&agenttierv1alpha1.NetworkSpec{AllowInternet: true},
		NetworkPolicyOptions{
			AllowRouterIngressOn9000: true,
			RouterNamespace:          "agenttier",
		},
	)

	if len(np.Spec.Ingress) != 1 {
		t.Errorf("HTTP-exec ingress rule lost on the AllowInternet branch (got %d ingress rules)", len(np.Spec.Ingress))
	}
}
