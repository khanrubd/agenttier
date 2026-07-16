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
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/util/exec"
)

func TestNewBridge_ConstructsWithFields(t *testing.T) {
	cs := workingClientsetForTest(t)
	restConfig := brokenExecutorRestConfig()
	b := NewBridge(cs, restConfig, testLogger())

	if b.clientset != cs {
		t.Error("expected clientset to be stored")
	}
	if b.restConfig != restConfig {
		t.Error("expected restConfig to be stored")
	}
}

// The exec-related Bridge methods all fail at the same choke point when the
// rest.Config can't build a SPDY executor (e.g. unreadable TLS cert/key
// files). Constructing a working clientset separately from a broken
// executor config isolates that failure path without a live apiserver.

func TestBridge_ExecCommand_ExecutorCreationFailure(t *testing.T) {
	cs := workingClientsetForTest(t)
	b := NewBridge(cs, brokenExecutorRestConfig(), testLogger())

	res, err := b.ExecCommand(context.Background(), "default", "pod-1", "sandbox", []string{"echo", "hi"}, 5)
	if err == nil {
		t.Fatal("expected error when executor creation fails")
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
	if !strings.Contains(err.Error(), "failed to create executor") {
		t.Errorf("expected 'failed to create executor' in error, got: %v", err)
	}
}

func TestBridge_ExecCommandStream_ExecutorCreationFailure(t *testing.T) {
	cs := workingClientsetForTest(t)
	b := NewBridge(cs, brokenExecutorRestConfig(), testLogger())

	var stdout, stderr bytes.Buffer
	code, err := b.ExecCommandStream(context.Background(), "default", "pod-1", "sandbox", []string{"echo"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when executor creation fails")
	}
	if code != -1 {
		t.Errorf("expected exit code -1 on executor creation failure, got %d", code)
	}
}

func TestBridge_ExecCommandStreamWithStdin_ExecutorCreationFailure(t *testing.T) {
	cs := workingClientsetForTest(t)
	b := NewBridge(cs, brokenExecutorRestConfig(), testLogger())

	stdin := strings.NewReader("payload")
	var stdout, stderr bytes.Buffer
	code, err := b.ExecCommandStreamWithStdin(context.Background(), "default", "pod-1", "sandbox", []string{"cat"}, stdin, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when executor creation fails")
	}
	if code != -1 {
		t.Errorf("expected exit code -1, got %d", code)
	}
}

func TestBridge_ExecCommandStreamWithStdin_NilStdinBehavesLikeExecCommandStream(t *testing.T) {
	cs := workingClientsetForTest(t)
	b := NewBridge(cs, brokenExecutorRestConfig(), testLogger())

	var stdout, stderr bytes.Buffer
	code, err := b.ExecCommandStreamWithStdin(context.Background(), "default", "pod-1", "sandbox", []string{"echo"}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when executor creation fails")
	}
	if code != -1 {
		t.Errorf("expected exit code -1, got %d", code)
	}
}

func TestBridge_ExecCommandStreamWithStdin_ContextCanceledBeforeCall(t *testing.T) {
	cs := workingClientsetForTest(t)
	b := NewBridge(cs, brokenExecutorRestConfig(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout, stderr bytes.Buffer
	// Even with a canceled context, executor construction happens first and
	// fails before ctx.Err() is consulted — so this still surfaces the
	// executor-creation error rather than ctx.Canceled. This documents the
	// actual ordering in ExecCommandStreamWithStdin.
	_, err := b.ExecCommandStreamWithStdin(ctx, "default", "pod-1", "sandbox", []string{"echo"}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestBridge_Connect_ExecutorCreationFailure(t *testing.T) {
	cs := workingClientsetForTest(t)
	b := NewBridge(cs, brokenExecutorRestConfig(), testLogger())

	_, client, cleanup := newWSPair(t)
	defer cleanup()
	session := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", client, false)

	err := b.Connect(context.Background(), session)
	if err == nil {
		t.Fatal("expected error when executor creation fails")
	}
	if !strings.Contains(err.Error(), "failed to create SPDY executor") {
		t.Errorf("expected 'failed to create SPDY executor' in error, got: %v", err)
	}
}

func TestExtractExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil error", nil, 0},
		{"code exit error", exec.CodeExitError{Err: errors.New("boom"), Code: 42}, 42},
		{"other error", errors.New("network blip"), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractExitCode(tc.err)
			if got != tc.want {
				t.Errorf("extractExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestLimitedBuffer_WriteAndString(t *testing.T) {
	b := &limitedBuffer{maxSize: 10}

	n, err := b.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected n=5, got %d", n)
	}
	if b.String() != "hello" {
		t.Errorf("expected 'hello', got %q", b.String())
	}
}

func TestLimitedBuffer_TruncatesAtMaxSize(t *testing.T) {
	b := &limitedBuffer{maxSize: 5}

	n, err := b.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Write reports the full input length was "consumed" (silently
	// discarding excess) even though internal storage is capped.
	if n != len("hello world") {
		t.Errorf("expected n=%d (full input length), got %d", len("hello world"), n)
	}
	if b.String() != "hello" {
		t.Errorf("expected buffer capped at maxSize 'hello', got %q", b.String())
	}
}

func TestLimitedBuffer_DiscardsAllOnceFull(t *testing.T) {
	b := &limitedBuffer{maxSize: 3}
	_, _ = b.Write([]byte("abc"))

	n, err := b.Write([]byte("more data"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len("more data") {
		t.Errorf("expected write to report full length even when discarded, got %d", n)
	}
	if b.String() != "abc" {
		t.Errorf("expected buffer unchanged once full, got %q", b.String())
	}
}
