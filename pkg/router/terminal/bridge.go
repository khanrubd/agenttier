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

package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"
)

// Bridge connects a terminal Session (WebSocket) to a Kubernetes pod exec stream (SPDY).
type Bridge struct {
	clientset  kubernetes.Interface
	restConfig *rest.Config
	logger     *slog.Logger
}

// NewBridge creates a new PTY bridge.
func NewBridge(clientset kubernetes.Interface, restConfig *rest.Config, logger *slog.Logger) *Bridge {
	return &Bridge{
		clientset:  clientset,
		restConfig: restConfig,
		logger:     logger,
	}
}

// Connect establishes the exec stream and bridges it to the session's WebSocket.
// This blocks until the session ends (disconnect, sandbox stop, or error).
func (b *Bridge) Connect(ctx context.Context, session *Session) error {
	b.logger.Info("establishing exec stream",
		"sessionId", session.ID,
		"pod", session.PodName,
		"namespace", session.Namespace,
		"shell", session.Shell,
	)

	// Build the exec request
	req := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(session.PodName).
		Namespace(session.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "sandbox",
			Command:   []string{session.Shell},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	// Create SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	// Stream bridges the WebSocket (session) to the SPDY exec stream.
	// session implements io.Reader (reads from WebSocket → stdin)
	// session implements io.Writer (writes from stdout → WebSocket)
	// session.sizeQueue implements TerminalSizeQueue (resize events)
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             session,
		Stdout:            session,
		Stderr:            session, // stderr merged with stdout for TTY
		Tty:               true,
		TerminalSizeQueue: session,
	})

	if err != nil {
		b.logger.Error("exec stream ended with error",
			"sessionId", session.ID,
			"error", err,
		)
		return fmt.Errorf("exec stream error: %w", err)
	}

	b.logger.Info("exec stream ended normally", "sessionId", session.ID)
	return nil
}

// ExecCommand runs a non-interactive command in a pod and returns the output.
func (b *Bridge) ExecCommand(ctx context.Context, namespace, podName, container string, command []string, timeout int) (*CommandResult, error) {
	req := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	stdout := &limitedBuffer{maxSize: 1 << 20} // 1MB max
	stderr := &limitedBuffer{maxSize: 1 << 20}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})

	result := &CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}

	if err != nil {
		result.ExitCode = extractExitCode(err)
		// extractExitCode returns -1 for non-CodeExitError failures. Map
		// those to 1 so callers that just check `ExitCode != 0` keep working.
		if result.ExitCode == -1 {
			result.ExitCode = 1
		}
	}

	return result, nil
}

// CommandResult holds the output of a non-interactive command execution.
type CommandResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// ExecCommandStream runs a non-interactive command in a pod and pipes
// stdout / stderr live to the supplied writers. Used by the agent /configure
// (install logs) and /invoke (entrypoint output) endpoints to emit SSE chunks
// as the command produces them rather than buffering to completion.
//
// Closing ctx cancels the exec — the SPDY layer drops the stream, which makes
// kubelet send SIGTERM (then SIGKILL) to the in-pod process. Callers tie ctx
// to the request context so a client disconnect terminates the agent.
//
// Returns the exit code on clean termination. On context cancel returns
// ctx.Err() and the exit code is undefined (the caller already knows the
// stream was aborted).
func (b *Bridge) ExecCommandStream(ctx context.Context, namespace, podName, container string, command []string, stdout, stderr io.Writer) (int, error) {
	return b.ExecCommandStreamWithStdin(ctx, namespace, podName, container, command, nil, stdout, stderr)
}

// ExecCommandStreamWithStdin is like ExecCommandStream but also pipes the
// supplied stdin reader into the in-pod process. Pass nil for stdin to
// behave exactly like ExecCommandStream. Used by /invoke so the request
// body reaches the entrypoint without going through `printf | base64 -d`
// (which hits ARG_MAX on payloads above ~128 KB).
func (b *Bridge) ExecCommandStreamWithStdin(ctx context.Context, namespace, podName, container string, command []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	req := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", req.URL())
	if err != nil {
		return -1, fmt.Errorf("failed to create executor: %w", err)
	}

	streamOpts := remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	}
	if stdin != nil {
		streamOpts.Stdin = stdin
	}
	err = exec.StreamWithContext(ctx, streamOpts)

	// Treat context cancel as the canonical "client disconnected" signal.
	// We intentionally don't try to recover the exit code here — the caller
	// will surface the cancel reason as the SSE exit event.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, ctxErr
	}

	if err != nil {
		return extractExitCode(err), nil
	}
	return 0, nil
}

// limitedBuffer is a bytes.Buffer with a maximum size to prevent OOM.
type limitedBuffer struct {
	data    []byte
	maxSize int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.maxSize - len(b.data)
	if remaining <= 0 {
		return len(p), nil // Silently discard excess
	}
	if len(p) > remaining {
		b.data = append(b.data, p[:remaining]...)
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.data)
}

// extractExitCode pulls the exit code from a remotecommand exec error. The
// SPDY layer wraps non-zero exits in `exec.CodeExitError`. Anything else
// (network blip, auth failure, ctx cancel) is treated as exit -1 so callers
// can distinguish from clean exit 0.
func extractExitCode(err error) int {
	if err == nil {
		return 0
	}
	var codeErr exec.CodeExitError
	if errors.As(err, &codeErr) {
		return codeErr.Code
	}
	return -1
}
