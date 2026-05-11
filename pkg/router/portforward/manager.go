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

// Package portforward provides Router-side helpers for exposing individual
// sandbox pod ports to authenticated users.
//
// The manager creates a Kubernetes Service targeting the sandbox pod so other
// in-cluster components (including the Router's own authenticated HTTP proxy)
// can reach it. If a PreviewDomain is configured, the manager also creates an
// Ingress so the port is reachable from outside the cluster at a URL of the
// form  https://sandbox-{sandbox}-{port}.{previewDomain}/.
//
// We intentionally use the stable networking.k8s.io/v1 Ingress type rather
// than Gateway API CRDs. Ingress is universally available on every Kubernetes
// 1.27+ cluster; Gateway API requires a separately-installed CRD bundle that
// not every deployment target has. When Gateway API is the right choice for
// the operator's cluster they can switch the ingressClassName via Helm values.
package portforward

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

const (
	// LabelManagedBy marks Services and Ingresses owned by the port-forward
	// manager so they can be garbage-collected in bulk.
	LabelManagedBy = "agenttier.io/port-forward"
	// LabelSandbox records the owning sandbox on every child object.
	LabelSandbox = "agenttier.io/sandbox"
	// LabelPort records the exposed container port for easy lookup.
	LabelPort = "agenttier.io/port"
)

// Options configures the port-forward manager.
type Options struct {
	// PreviewDomain, if set, is the suffix used to build preview hostnames
	// (e.g. "preview.agenttier.company.com"). Empty means no Ingress is
	// created; the Service is still provisioned so the Router's internal
	// proxy can reach it.
	PreviewDomain string

	// IngressClassName, if set, is written to the Ingress spec. Empty lets
	// the cluster default class handle it.
	IngressClassName string

	// ControllerNamespace is where the Router runs — used to scope Ingress
	// owner references if we ever switch to cross-namespace garbage collection.
	ControllerNamespace string
}

// Manager creates, reads, and deletes port forwards for sandboxes.
type Manager struct {
	Client client.Client
	Opts   Options
}

// New returns a port-forward Manager.
func New(c client.Client, opts Options) *Manager {
	return &Manager{Client: c, Opts: opts}
}

// ForwardedPort is the JSON representation returned by the Router's REST API.
type ForwardedPort struct {
	Port       int32  `json:"port"`
	Protocol   string `json:"protocol"`
	PreviewURL string `json:"previewUrl,omitempty"`
	// InternalURL is the cluster-internal base URL for the Service. Useful
	// for debugging and for the Router's own reverse proxy.
	InternalURL string `json:"internalUrl,omitempty"`
}

// Create provisions a Service (and Ingress when a PreviewDomain is configured)
// exposing a sandbox pod port. It is idempotent: calling Create twice with the
// same arguments returns the existing resources.
func (m *Manager) Create(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, port int32, protocol string) (*ForwardedPort, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("port must be 1-65535 (got %d)", port)
	}
	if protocol == "" {
		protocol = "http"
	}

	name := serviceName(sandbox.Name, port)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				LabelManagedBy: "true",
				LabelSandbox:   sandbox.Name,
				LabelPort:      fmt.Sprintf("%d", port),
			},
			OwnerReferences: []metav1.OwnerReference{sandboxOwnerRef(sandbox)},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"agenttier.io/sandbox": sandbox.Name},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstr.FromInt32(port),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if err := upsertService(ctx, m.Client, svc); err != nil {
		return nil, fmt.Errorf("upsert service: %w", err)
	}

	result := &ForwardedPort{
		Port:        port,
		Protocol:    protocol,
		InternalURL: fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", name, sandbox.Namespace, port),
	}

	if m.Opts.PreviewDomain != "" {
		host := previewHost(sandbox.Name, port, m.Opts.PreviewDomain)
		ing := buildIngress(name, sandbox.Namespace, host, port, m.Opts.IngressClassName, sandbox)
		if err := upsertIngress(ctx, m.Client, ing); err != nil {
			return nil, fmt.Errorf("upsert ingress: %w", err)
		}
		result.PreviewURL = fmt.Sprintf("https://%s/", host)
	}

	return result, nil
}

// Delete removes the Service and Ingress for a port. Returns nil if nothing
// was there (safe to call repeatedly during cleanup).
func (m *Manager) Delete(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, port int32) error {
	name := serviceName(sandbox.Name, port)

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: sandbox.Namespace}}
	if err := m.Client.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete service: %w", err)
	}

	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: sandbox.Namespace}}
	if err := m.Client.Delete(ctx, ing); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete ingress: %w", err)
	}
	return nil
}

// List returns all forwarded ports for a sandbox by reading back from the
// cluster — the sandbox status is informative but the cluster is the source of truth.
func (m *Manager) List(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) ([]ForwardedPort, error) {
	svcList := &corev1.ServiceList{}
	if err := m.Client.List(ctx, svcList,
		client.InNamespace(sandbox.Namespace),
		client.MatchingLabels{LabelManagedBy: "true", LabelSandbox: sandbox.Name},
	); err != nil {
		return nil, fmt.Errorf("list port-forward services: %w", err)
	}
	out := make([]ForwardedPort, 0, len(svcList.Items))
	for i := range svcList.Items {
		svc := svcList.Items[i]
		if len(svc.Spec.Ports) == 0 {
			continue
		}
		port := svc.Spec.Ports[0].Port
		fp := ForwardedPort{
			Port:        port,
			Protocol:    "http",
			InternalURL: fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", svc.Name, svc.Namespace, port),
		}
		if m.Opts.PreviewDomain != "" {
			fp.PreviewURL = fmt.Sprintf("https://%s/", previewHost(sandbox.Name, port, m.Opts.PreviewDomain))
		}
		out = append(out, fp)
	}
	return out, nil
}

// --- helpers ---

func serviceName(sandbox string, port int32) string {
	// Kubernetes Service names are limited to 63 chars; the sandbox name is
	// already validated as a DNS-1123 label. "pf-" prefix makes intent clear.
	return fmt.Sprintf("pf-%s-%d", sandbox, port)
}

func previewHost(sandbox string, port int32, domain string) string {
	// Strip any leading dot/whitespace the operator supplied in Helm values.
	domain = strings.TrimSpace(strings.TrimPrefix(domain, "."))
	return fmt.Sprintf("sandbox-%s-%d.%s", sandbox, port, domain)
}

func sandboxOwnerRef(sandbox *agenttierv1alpha1.Sandbox) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{
		APIVersion: agenttierv1alpha1.GroupVersion.String(),
		Kind:       "Sandbox",
		Name:       sandbox.Name,
		UID:        sandbox.UID,
		Controller: &controller,
	}
}

func upsertService(ctx context.Context, c client.Client, desired *corev1.Service) error {
	existing := &corev1.Service{}
	key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}
	err := c.Get(ctx, key, existing)
	if errors.IsNotFound(err) {
		return c.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	// Carry over immutable fields; update only spec + labels.
	desired.ResourceVersion = existing.ResourceVersion
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	desired.Spec.ClusterIPs = existing.Spec.ClusterIPs
	return c.Update(ctx, desired)
}

func buildIngress(name, namespace, host string, port int32, className string, sandbox *agenttierv1alpha1.Sandbox) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				LabelManagedBy: "true",
				LabelSandbox:   sandbox.Name,
				LabelPort:      fmt.Sprintf("%d", port),
			},
			OwnerReferences: []metav1.OwnerReference{sandboxOwnerRef(sandbox)},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: name,
									Port: networkingv1.ServiceBackendPort{Number: port},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if className != "" {
		ing.Spec.IngressClassName = &className
	}
	return ing
}

func upsertIngress(ctx context.Context, c client.Client, desired *networkingv1.Ingress) error {
	existing := &networkingv1.Ingress{}
	key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}
	err := c.Get(ctx, key, existing)
	if errors.IsNotFound(err) {
		return c.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	desired.ResourceVersion = existing.ResourceVersion
	return c.Update(ctx, desired)
}
