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

package router

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/controller"
	"github.com/agenttier/agenttier/pkg/router/agent"
	"github.com/agenttier/agenttier/pkg/router/sandboxhttp"
	"github.com/agenttier/agenttier/pkg/router/terminal"
)

// runtimePortForTest is the override for the in-pod runtime's listen
// port. Production always uses 9000 (matches sandboxruntime.Default-
// ListenAddr); tests inject a different port to point the dispatcher
// at an httptest.Server on localhost. Constant in production code
// paths to keep the wire contract obvious.
var runtimePortForTest = "9000"

// runtimeHealthzTimeout is how long the dispatcher waits for the in-pod
// runtime's /healthz response before falling back to SPDY. A
// fast-failing probe is the right call: when the runtime is up, /healthz
// returns in single-digit ms; when it isn't, we don't want to keep the
// caller waiting on a doomed dial. Three seconds is enough margin for
// kubernetes Service IP resolution + a TCP handshake on a healthy
// network and short enough that fallback is timely.
const runtimeHealthzTimeout = 3 * time.Second

// executor is the contract the /exec handler uses to run a command in a
// sandbox. Each implementation (SPDY-based, HTTP-based) is exchanged
// transparently via dispatchExec, which picks the right one based on
// HTTP-exec opt-in state and runtime reachability.
type executor interface {
	Execute(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, command []string, timeoutSeconds int) (*terminal.CommandResult, error)
}

// spdyExecutor wraps the legacy SPDY path. Today's default — used when
// the sandbox isn't opted into HTTP-exec or when the runtime is
// unreachable. Behavior is byte-identical to what handleExecCommand
// has done since v0.1.
type spdyExecutor struct {
	bridge *terminal.Bridge
}

func (e *spdyExecutor) Execute(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, command []string, timeoutSeconds int) (*terminal.CommandResult, error) {
	return e.bridge.ExecCommand(ctx, sandbox.Namespace, sandbox.Status.PodName, "sandbox", command, timeoutSeconds)
}

// httpExecutor proxies to the in-pod sandbox-runtime HTTP server. Used
// when the sandbox was created with HarnessSpec.UseHTTPExec=true and
// the runtime is reachable + healthy.
type httpExecutor struct {
	client *sandboxhttp.Client
}

func (e *httpExecutor) Execute(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, command []string, timeoutSeconds int) (*terminal.CommandResult, error) {
	resp, err := e.client.Exec(ctx, sandboxhttp.ExecRequest{
		Command:        command,
		TimeoutSeconds: timeoutSeconds,
	})
	if err != nil {
		return nil, err
	}
	// Map the runtime's response shape onto terminal.CommandResult so
	// the handler doesn't need to know which path produced the result.
	// The runtime may include extra fields (TimedOut, Truncated,
	// DurationMs); none change the user-visible contract today.
	return &terminal.CommandResult{
		Stdout:   resp.Stdout,
		Stderr:   resp.Stderr,
		ExitCode: resp.ExitCode,
	}, nil
}

// dispatchExec picks the right executor for a sandbox and runs the
// command. The decision tree:
//
//  1. The sandbox has no runtime-token Secret → SPDY. This is the
//     common case today (UseHTTPExec defaults to false). Reading the
//     Secret first avoids any controller-state coupling — the Secret's
//     existence is the canonical signal.
//
//  2. The sandbox has a token but the pod has no IP yet → SPDY.
//     Pod.Status.PodIP is empty during the brief window between Pod
//     scheduling and kubelet finalizing the network setup; falling
//     back avoids a spurious 502 in that window.
//
//  3. /healthz on the runtime fails → SPDY with a structured warning
//     log. This is the soft-fail path for "runtime crashed inside the
//     pod" — the user shouldn't see anything different.
//
//  4. Healthy runtime → HTTP. Returns the result directly.
//
// The whole point is that operators flip on HarnessSpec.UseHTTPExec
// and watch traffic shift over without having to babysit the cutover.
// Anything that would otherwise produce a 502 quietly falls back.
func (s *Server) dispatchExec(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, command []string, timeoutSeconds int) (*terminal.CommandResult, error) {
	exec, fallbackReason := s.pickExecutor(ctx, sandbox)
	if fallbackReason != "" {
		s.logger.Info("HTTP-exec fallback to SPDY",
			"sandbox", sandbox.Name,
			"namespace", sandbox.Namespace,
			"reason", fallbackReason,
		)
	}
	return exec.Execute(ctx, sandbox, command, timeoutSeconds)
}

// pickExecutor decides which executor to use and returns a fallback-reason
// string when the SPDY path is selected for any reason other than "the
// sandbox isn't opted in." Empty fallback reason on the happy
// HTTP-exec path or when SPDY is the deliberate choice (no token).
func (s *Server) pickExecutor(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (executor, string) {
	spdy := &spdyExecutor{bridge: s.bridge}

	// No client wired? SPDY only.
	if s.k8sClient == nil {
		return spdy, ""
	}

	token, err := controller.ReadRuntimeToken(ctx, s.k8sClient, sandbox.Name, sandbox.Namespace)
	if err != nil {
		// Read failure is rare (apiserver hiccup) and not worth
		// failing the request over — SPDY is always available.
		return spdy, "runtime-token Secret read failed: " + err.Error()
	}
	if token == "" {
		// The deliberate, non-opted-in path. No fallback warning.
		return spdy, ""
	}

	podIP := s.lookupPodIP(ctx, sandbox)
	if podIP == "" {
		return spdy, "pod IP not yet assigned"
	}

	client := sandboxhttp.New("http://"+podIP+":"+runtimePortForTest, token)

	// Probe the runtime before swapping. If /healthz times out or
	// returns non-200 we fall back. The small extra round trip is
	// worth it because a doomed dispatch would surface as a 5xx to
	// the user; a probe lets us be invisible.
	probeCtx, cancel := context.WithTimeout(ctx, runtimeHealthzTimeout)
	defer cancel()
	if err := client.Healthz(probeCtx); err != nil {
		return spdy, "runtime healthz failed: " + err.Error()
	}

	return &httpExecutor{client: client}, ""
}

// lookupPodIP returns the running sandbox pod's IP, or empty string when
// the pod isn't found / has no IP yet. The status field is updated by
// kubelet as soon as the network namespace is wired, so anything past
// SandboxPhaseRunning should have it — but we tolerate the brief gap
// between phase-transition and IP-assignment by returning empty.
func (s *Server) lookupPodIP(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) string {
	if sandbox.Status.PodName == "" {
		return ""
	}
	pod := &corev1.Pod{}
	if err := s.k8sClient.Get(ctx, client.ObjectKey{
		Name:      sandbox.Status.PodName,
		Namespace: sandbox.Namespace,
	}, pod); err != nil {
		return ""
	}
	return pod.Status.PodIP
}

// agentHTTPExec implements the agent package's HTTPExecResolver. Returns
// (dispatcher, true) when the sandbox is opted into HTTP-exec and the
// in-pod runtime is reachable; (nil, false) otherwise. Reuses the same
// decision tree pickExecutor uses for /exec — token Secret + PodIP +
// healthy /healthz — so the two surfaces stay consistent.
func (s *Server) agentHTTPExec(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (agent.HTTPExecDispatcher, bool) {
	if s.k8sClient == nil {
		return nil, false
	}
	token, err := controller.ReadRuntimeToken(ctx, s.k8sClient, sandbox.Name, sandbox.Namespace)
	if err != nil || token == "" {
		return nil, false
	}
	podIP := s.lookupPodIP(ctx, sandbox)
	if podIP == "" {
		return nil, false
	}
	client := sandboxhttp.New("http://"+podIP+":"+runtimePortForTest, token)
	probeCtx, cancel := context.WithTimeout(ctx, runtimeHealthzTimeout)
	defer cancel()
	if err := client.Healthz(probeCtx); err != nil {
		return nil, false
	}
	return &agentHTTPDispatcher{client: client}, true
}

// agentHTTPDispatcher adapts a sandboxhttp.Client to the agent package's
// HTTPExecDispatcher interface. The two methods just translate request
// shapes between the two packages — neither owns the streaming logic.
type agentHTTPDispatcher struct {
	client *sandboxhttp.Client
}

func (d *agentHTTPDispatcher) InvokeStream(ctx context.Context, req agent.HTTPInvokeRequest, onEvent func(eventType string, data []byte) error) error {
	return d.client.InvokeStream(ctx, sandboxhttp.InvokeRequest{
		Command:        req.Command,
		Stdin:          req.Stdin,
		TimeoutSeconds: req.TimeoutSeconds,
		WorkingDir:     req.WorkingDir,
		Env:            req.Env,
		InvokeID:       req.InvokeID,
	}, func(ev sandboxhttp.InvokeEvent) error {
		return onEvent(ev.EventType, ev.Data)
	})
}

func (d *agentHTTPDispatcher) InvokeCancel(ctx context.Context, invokeID string) error {
	return d.client.InvokeCancel(ctx, invokeID)
}
