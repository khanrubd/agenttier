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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// stubBridge records exec calls and replies with canned stdout / stderr.
type stubBridge struct {
	mu     sync.Mutex
	calls  []stubCall
	stdout []byte
	stderr []byte
	exit   int
}

type stubCall struct {
	command []string
}

func (b *stubBridge) ExecCommandStream(ctx context.Context, _, _, _ string, command []string, stdout, stderr io.Writer) (int, error) {
	b.mu.Lock()
	b.calls = append(b.calls, stubCall{command: append([]string(nil), command...)})
	b.mu.Unlock()
	if len(b.stdout) > 0 {
		_, _ = stdout.Write(b.stdout)
	}
	if len(b.stderr) > 0 {
		_, _ = stderr.Write(b.stderr)
	}
	return b.exit, nil
}

// ExecCommandStreamWithStdin proxies to ExecCommandStream after draining
// any stdin reader so we can verify input bytes via the recorded call.
func (b *stubBridge) ExecCommandStreamWithStdin(ctx context.Context, ns, pod, container string, command []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin)
	}
	return b.ExecCommandStream(ctx, ns, pod, container, command, stdout, stderr)
}

func buildHandler(t *testing.T, sandbox *agenttierv1alpha1.Sandbox, bridge ExecBridge) (*Handler, client.Client) {
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

	h := New(Options{
		K8sClient: c,
		Bridge:    bridge,
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
	})
	return h, c
}

func newAgentSandbox() *agenttierv1alpha1.Sandbox {
	return &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-agent", Namespace: "default", UID: "u1"},
		Spec: agenttierv1alpha1.SandboxSpec{
			Mode:      agenttierv1alpha1.SandboxModeAgent,
			CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "dev-user"},
		},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: "sbx-agent-pod",
		},
	}
}

// doConfigure builds a /configure POST and dispatches it through a real
// gorilla/mux router so the {id} variable resolves correctly.
func doConfigure(h *Handler, body any) *httptest.ResponseRecorder {
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

func TestConfigure_RejectsCodeMode(t *testing.T) {
	sandbox := newAgentSandbox()
	sandbox.Spec.Mode = agenttierv1alpha1.SandboxModeCode

	h, _ := buildHandler(t, sandbox, &stubBridge{})
	rec := doConfigure(h, ConfigureRequest{Entrypoint: []string{"echo"}})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for code-mode sandbox, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mode: agent") {
		t.Errorf("expected error to reference mode: agent, got %q", rec.Body.String())
	}
}

func TestConfigure_FilesAndInstallPersistsStatus(t *testing.T) {
	sandbox := newAgentSandbox()
	bridge := &stubBridge{stdout: []byte("Successfully installed mem0\n"), exit: 0}

	h, c := buildHandler(t, sandbox, bridge)
	rec := doConfigure(h, ConfigureRequest{
		Files: []ConfigureFile{
			{Path: "/workspace/agent.py", Content: "print('hi')\n"},
			{Path: "/workspace/requirements.txt", Content: "mem0\n"},
		},
		InstallCommand: []string{"pip", "install", "-r", "/workspace/requirements.txt"},
		Entrypoint:     []string{"python", "/workspace/agent.py"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK SSE, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	// Every file write + the install becomes a separate exec call.
	bridge.mu.Lock()
	calls := bridge.calls
	bridge.mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("expected 3 exec calls (2 files + 1 install), got %d", len(calls))
	}
	// The last call should be the install command.
	if got := calls[2].command; len(got) < 4 || got[0] != "pip" {
		t.Errorf("expected last call to be pip install, got %v", got)
	}

	// SSE body should contain the install output.
	if !strings.Contains(body, "Successfully installed mem0") {
		t.Errorf("expected install stdout in SSE body, got %q", body)
	}
	// And a final result event.
	if !strings.Contains(body, "event: result") {
		t.Errorf("expected result event in SSE body, got %q", body)
	}

	// Status should now reflect the configure.
	sb := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sbx-agent", Namespace: "default"}, sb); err != nil {
		t.Fatalf("re-fetch sandbox: %v", err)
	}
	if sb.Status.AgentConfigure == nil {
		t.Fatal("expected status.agentConfigure populated")
	}
	if sb.Status.AgentConfigure.InstallExitCode != 0 {
		t.Errorf("expected exit 0, got %d", sb.Status.AgentConfigure.InstallExitCode)
	}
	if got := sb.Status.AgentConfigure.Entrypoint; len(got) != 2 || got[0] != "python" {
		t.Errorf("expected entrypoint persisted, got %v", got)
	}
	if !strings.Contains(sb.Status.AgentConfigure.InstallLog, "mem0") {
		t.Errorf("expected install log tail to contain stdout, got %q", sb.Status.AgentConfigure.InstallLog)
	}
}

func TestConfigure_IdempotentRerun(t *testing.T) {
	sandbox := newAgentSandbox()
	bridge := &stubBridge{exit: 0}
	h, _ := buildHandler(t, sandbox, bridge)

	body := ConfigureRequest{
		Files:          []ConfigureFile{{Path: "/workspace/agent.py", Content: "print('hi')\n"}},
		InstallCommand: []string{"pip", "install", "agentlib"},
		Entrypoint:     []string{"python", "/workspace/agent.py"},
	}

	rec1 := doConfigure(h, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first configure: expected 200, got %d", rec1.Code)
	}

	bridge.mu.Lock()
	firstRunCalls := len(bridge.calls)
	bridge.mu.Unlock()
	if firstRunCalls != 2 {
		t.Fatalf("first configure: expected 2 exec calls (1 file + 1 install), got %d", firstRunCalls)
	}

	rec2 := doConfigure(h, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second configure: expected 200, got %d", rec2.Code)
	}
	bridge.mu.Lock()
	secondRunCalls := len(bridge.calls)
	bridge.mu.Unlock()
	// Same files + command hash → no new exec calls.
	if secondRunCalls != firstRunCalls {
		t.Errorf("idempotent re-configure should skip install (no new exec calls); had %d, now %d",
			firstRunCalls, secondRunCalls)
	}
	if !strings.Contains(rec2.Body.String(), `"skipped":true`) {
		t.Errorf("expected skipped:true in second response, got %q", rec2.Body.String())
	}
}

func TestConfigure_ValidatesAbsolutePath(t *testing.T) {
	sandbox := newAgentSandbox()
	h, _ := buildHandler(t, sandbox, &stubBridge{})

	rec := doConfigure(h, ConfigureRequest{
		Files: []ConfigureFile{{Path: "agent.py", Content: "x"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative path, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "absolute") {
		t.Errorf("expected error to mention absolute path, got %q", rec.Body.String())
	}
}
