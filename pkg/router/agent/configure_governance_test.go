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

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/governance"
)

// buildHandlerWithPolicy mirrors buildHandler from handler_test.go but
// wires a static PolicyResolver in so we can exercise the governance
// gate. Lives here (rather than handler_test.go) to keep the
// governance-specific tests self-contained.
func buildHandlerWithPolicy(t *testing.T, sandbox *agenttierv1alpha1.Sandbox, policy governance.Policy) *Handler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1 scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sandbox).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()

	return New(Options{
		K8sClient: c,
		Bridge:    &stubBridge{},
		ClaimsLookup: func(r *http.Request) *Claims {
			return &Claims{Sub: "dev-user", Email: "dev@agenttier.local", IsAdmin: true}
		},
		SandboxOf: func(ctx context.Context, sandboxID string, claims *Claims) (*agenttierv1alpha1.Sandbox, error) {
			sb := &agenttierv1alpha1.Sandbox{}
			if err := c.Get(ctx, client.ObjectKey{Name: sandboxID, Namespace: "default"}, sb); err != nil {
				return nil, err
			}
			return sb, nil
		},
		PolicyOf: func(ctx context.Context, namespace string) (governance.Policy, error) {
			return policy, nil
		},
	})
}

// dispatchConfigure runs a /configure POST through the handler's mux.
// Returns the recorder so callers can assert on status + body.
func dispatchConfigure(h *Handler, body any) *httptest.ResponseRecorder {
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sbx-agent/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r := mux.NewRouter()
	api := r.PathPrefix("/api/v1").Subrouter()
	h.RegisterRoutes(api)
	r.ServeHTTP(rec, req)
	return rec
}

// TestConfigure_GovernanceTemplateAllowlist verifies a request is
// denied when the sandbox's resolved template isn't in the policy's
// AllowedTemplates list.
func TestConfigure_GovernanceTemplateAllowlist(t *testing.T) {
	sandbox := newAgentSandbox()
	sandbox.Status.ResolvedTemplate = "blocked-template"

	h := buildHandlerWithPolicy(t, sandbox, governance.Policy{
		AllowedTemplates: []string{"only-this-template"},
	})

	rec := dispatchConfigure(h, ConfigureRequest{
		Entrypoint: []string{"python", "/workspace/agent.py"},
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 from AllowedTemplates denial, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AllowedTemplates") {
		t.Errorf("expected error to reference AllowedTemplates, got %q", rec.Body.String())
	}
}

// TestConfigure_GovernanceImageAllowlist verifies the AllowedAgentImages
// gate denies a configure when the sandbox's spec.image doesn't match
// any approved registry prefix.
func TestConfigure_GovernanceImageAllowlist(t *testing.T) {
	sandbox := newAgentSandbox()
	sandbox.Spec.Image = &agenttierv1alpha1.ImageSpec{
		Repository: "docker.io/sketchy/random-agent:latest",
	}

	h := buildHandlerWithPolicy(t, sandbox, governance.Policy{
		AllowedAgentImages: []string{"ghcr.io/agenttier/", "582483581248.dkr.ecr.us-east-1.amazonaws.com/agentloft/"},
	})

	rec := dispatchConfigure(h, ConfigureRequest{
		Entrypoint: []string{"python", "/workspace/agent.py"},
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 from AllowedAgentImages denial, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AllowedAgentImages") {
		t.Errorf("expected error to reference AllowedAgentImages, got %q", rec.Body.String())
	}
}

// TestConfigure_GovernanceImageAllowlistMatches verifies a configure
// is allowed when the image matches a prefix.
func TestConfigure_GovernanceImageAllowlistMatches(t *testing.T) {
	sandbox := newAgentSandbox()
	sandbox.Spec.Image = &agenttierv1alpha1.ImageSpec{
		Repository: "ghcr.io/agenttier/sandbox-langgraph:v0.4.1",
	}

	h := buildHandlerWithPolicy(t, sandbox, governance.Policy{
		AllowedAgentImages: []string{"ghcr.io/agenttier/"},
	})

	rec := dispatchConfigure(h, ConfigureRequest{
		Entrypoint: []string{"python", "/workspace/agent.py"},
	})

	// Empty policy/IsEmpty=false plus matching image must not 403.
	// We expect either 200 or a non-policy error from the SSE stream
	// short-circuiting on the stub. The important assertion is "not 403".
	if rec.Code == http.StatusForbidden {
		t.Fatalf("expected pass-through, got 403: %s", rec.Body.String())
	}
}

// TestConfigure_AggregateFileSizeCap rejects a request whose total
// upload exceeds configureFileTotalLimitBytes (sized below the per-file
// cap to avoid eating real memory in CI).
func TestConfigure_AggregateFileSizeCap(t *testing.T) {
	sandbox := newAgentSandbox()
	h := buildHandlerWithPolicy(t, sandbox, governance.Policy{})

	// 5 files × (1/4 of total cap) = 5 × 32 MiB = 160 MiB > 128 MiB cap.
	chunk := strings.Repeat("a", configureFileLimitBytes/2) // 16 MiB each
	files := make([]ConfigureFile, 0, 9)
	for i := 0; i < 9; i++ { // 9 × 16 MiB = 144 MiB > 128 MiB
		files = append(files, ConfigureFile{
			Path:    "/workspace/file" + string(rune('a'+i)) + ".bin",
			Content: chunk,
		})
	}

	rec := dispatchConfigure(h, ConfigureRequest{
		Files:      files,
		Entrypoint: []string{"python", "/workspace/agent.py"},
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 from aggregate size cap, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "aggregate file size") {
		t.Errorf("expected error to mention aggregate file size, got %q", rec.Body.String())
	}
}

// TestConfigure_TooManyFiles enforces configureFileMaxCount.
func TestConfigure_TooManyFiles(t *testing.T) {
	sandbox := newAgentSandbox()
	h := buildHandlerWithPolicy(t, sandbox, governance.Policy{})

	files := make([]ConfigureFile, configureFileMaxCount+1)
	for i := range files {
		files[i] = ConfigureFile{
			Path:    "/workspace/f" + string(rune('a'+(i%26))) + string(rune('a'+(i/26))) + ".txt",
			Content: "x",
		}
	}

	rec := dispatchConfigure(h, ConfigureRequest{
		Files:      files,
		Entrypoint: []string{"python", "/workspace/agent.py"},
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 from too-many-files cap, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too many files") {
		t.Errorf("expected error to mention too many files, got %q", rec.Body.String())
	}
}

// TestConfigure_NoPolicyResolverSkipsGovernance verifies that when
// PolicyOf is nil, the governance gate doesn't block anything (the
// caller is in the "no governance configured" deployment posture).
func TestConfigure_NoPolicyResolverSkipsGovernance(t *testing.T) {
	sandbox := newAgentSandbox()
	sandbox.Status.ResolvedTemplate = "anything"

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1 scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sandbox).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()

	h := New(Options{
		K8sClient: c,
		Bridge:    &stubBridge{},
		ClaimsLookup: func(r *http.Request) *Claims {
			return &Claims{Sub: "dev-user", Email: "dev@agenttier.local", IsAdmin: true}
		},
		SandboxOf: func(ctx context.Context, sandboxID string, claims *Claims) (*agenttierv1alpha1.Sandbox, error) {
			sb := &agenttierv1alpha1.Sandbox{}
			if err := c.Get(ctx, client.ObjectKey{Name: sandboxID, Namespace: "default"}, sb); err != nil {
				return nil, err
			}
			return sb, nil
		},
		// PolicyOf is intentionally nil.
	})

	rec := dispatchConfigure(h, ConfigureRequest{
		Entrypoint: []string{"python", "/workspace/agent.py"},
	})

	if rec.Code == http.StatusForbidden {
		t.Fatalf("expected pass-through with no policy resolver, got 403: %s", rec.Body.String())
	}
}
