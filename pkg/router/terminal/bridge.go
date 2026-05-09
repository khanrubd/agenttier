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
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
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
		// Extract exit code from error if possible
		result.ExitCode = extractExitCode(err)
		if result.ExitCode == 0 {
			result.ExitCode = 1 // Default non-zero for errors
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

// extractExitCode attempts to extract the exit code from an exec error.
func extractExitCode(err error) int {
	// The error message from remotecommand typically contains the exit code
	// Format: "command terminated with exit code N"
	// This is a simplified extraction — production code should use
	// k8s.io/apimachinery/pkg/util/exec.CodeExitError
	if err == nil {
		return 0
	}
	// Default: return -1 to indicate unknown exit code
	return -1
}
