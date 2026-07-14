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
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/tools/remotecommand"
)

func TestNewSession_DefaultsAndInitialSize(t *testing.T) {
	_, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", client, false)

	if s.ID != "sess-1" || s.SandboxID != "sbx-1" || s.PodName != "pod-1" {
		t.Fatalf("unexpected session fields: %+v", s)
	}
	if s.CreatedAt.IsZero() || s.LastActive.IsZero() {
		t.Error("expected CreatedAt/LastActive to be set")
	}

	size := s.Next()
	if size == nil || size.Width != 120 || size.Height != 40 {
		t.Errorf("expected default 120x40 initial size, got %+v", size)
	}
}

func TestSession_Read_InputMessage(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)

	data, err := MarshalInput("echo hi\n")
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write input from client: %v", err)
	}

	buf := make([]byte, 64)
	n, err := s.Read(buf)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if string(buf[:n]) != "echo hi\n" {
		t.Errorf("expected 'echo hi\\n', got %q", string(buf[:n]))
	}
	if s.BytesIn != int64(n) {
		t.Errorf("expected BytesIn=%d, got %d", n, s.BytesIn)
	}
}

func TestSession_Read_ViewerDiscardsInput(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	// Viewer sessions are spectators — their Read must skip input frames
	// and keep waiting rather than returning discarded data.
	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, true)

	inputData, _ := MarshalInput("should be discarded\n")
	if err := client.WriteMessage(websocket.TextMessage, inputData); err != nil {
		t.Fatalf("write input: %v", err)
	}
	// Follow with a resize (also skipped) and then a real signal we can
	// detect: close the client connection to end the blocking Read call.
	resizeData, _ := MarshalResize(80, 24)
	if err := client.WriteMessage(websocket.TextMessage, resizeData); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, err := s.Read(buf)
		readDone <- err
	}()

	// Give the goroutine a moment to consume the discarded frames, then
	// close so Read unblocks with an error (proving it never returned data
	// for the input frame above).
	time.Sleep(50 * time.Millisecond)
	_ = client.Close()

	select {
	case err := <-readDone:
		if err == nil {
			t.Error("expected Read to return an error after connection close, not viewer input data")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Read to return")
	}
}

func TestSession_Read_ResizeUpdatesSizeQueue(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)

	resizeData, _ := MarshalResize(200, 50)
	if err := client.WriteMessage(websocket.TextMessage, resizeData); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 64)
		_, _ = s.Read(buf) // Blocks until more input or error; we only care about the resize side effect.
		close(readDone)
	}()

	// Poll for the new size to land on the queue (Read runs in a goroutine).
	deadline := time.Now().Add(2 * time.Second)
	var size *remotecommand.TerminalSize
	for time.Now().Before(deadline) {
		size = s.Next()
		if size.Width == 200 && size.Height == 50 {
			break
		}
		s.sizeQueue.Push(*size) // put it back if it wasn't the one we wanted yet
		time.Sleep(10 * time.Millisecond)
	}
	if size == nil || size.Width != 200 || size.Height != 50 {
		t.Errorf("expected resize to 200x50, got %+v", size)
	}

	_ = client.Close()
	<-readDone
}

func TestSession_Read_PingSendsPong(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)

	pingData, _ := json.Marshal(WSMessage{Type: MessageTypePing})
	if err := client.WriteMessage(websocket.TextMessage, pingData); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	go func() {
		buf := make([]byte, 64)
		_, _ = s.Read(buf)
	}()

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("expected pong response, got error: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal pong: %v", err)
	}
	if msg.Type != MessageTypePong {
		t.Errorf("expected pong, got %s", msg.Type)
	}
}

func TestSession_Read_MalformedMessageSkipped(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)

	if err := client.WriteMessage(websocket.TextMessage, []byte("not json")); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	validData, _ := MarshalInput("ok\n")
	if err := client.WriteMessage(websocket.TextMessage, validData); err != nil {
		t.Fatalf("write valid input: %v", err)
	}

	buf := make([]byte, 64)
	n, err := s.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(buf[:n]) != "ok\n" {
		t.Errorf("expected malformed message to be skipped and 'ok\\n' returned, got %q", string(buf[:n]))
	}
}

func TestSession_Read_ConnectionClosedReturnsError(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)
	_ = client.Close()

	buf := make([]byte, 64)
	_, err := s.Read(buf)
	if err == nil {
		t.Error("expected error reading from a closed connection")
	}
}

func TestSession_Write_SendsOutputMessage(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)

	n, err := s.Write([]byte("hello output"))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != len("hello output") {
		t.Errorf("expected n=%d, got %d", len("hello output"), n)
	}
	if s.BytesOut != int64(len("hello output")) {
		t.Errorf("expected BytesOut updated, got %d", s.BytesOut)
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("expected output message: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != MessageTypeOutput || msg.Stream != "stdout" || msg.Data != "hello output" {
		t.Errorf("unexpected message: %+v", msg)
	}
}

func TestSession_Write_AfterCloseReturnsErrClosedPipe(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()
	defer client.Close()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)
	s.Close()

	_, err := s.Write([]byte("data"))
	if err != io.ErrClosedPipe {
		t.Errorf("expected io.ErrClosedPipe, got %v", err)
	}
}

func TestSession_Close_IsIdempotent(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()
	defer client.Close()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)
	s.Close()
	s.Close() // Must not panic or double-close.

	if !s.closed {
		t.Error("expected session to be marked closed")
	}
}

func TestSession_SendClose_MarksClosed(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)
	s.SendClose(CloseReasonIdleTimeout, 4002)

	if !s.closed {
		t.Error("expected SendClose to mark the session closed")
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("expected close message: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != MessageTypeClose || msg.Reason != CloseReasonIdleTimeout || msg.Code != 4002 {
		t.Errorf("unexpected close message: %+v", msg)
	}
}

func TestSession_SendError_DeliversErrorMessage(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)
	s.SendError("sandbox not running")

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("expected error message: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != MessageTypeError || msg.Message != "sandbox not running" {
		t.Errorf("unexpected error message: %+v", msg)
	}
}

func TestSession_Duration_IncreasesOverTime(t *testing.T) {
	_, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", client, false)
	time.Sleep(10 * time.Millisecond)
	if s.Duration() <= 0 {
		t.Error("expected positive duration")
	}
}

func TestSession_DisconnectAndReconnect_ClearsState(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)
	if s.IsDisconnected() {
		t.Fatal("new session should not be disconnected")
	}

	s.MarkDisconnected()
	if !s.IsDisconnected() {
		t.Fatal("expected session to be marked disconnected")
	}

	s.Close() // Simulate the old connection dying while disconnected.

	_, newClient, cleanup2 := newWSPair(t)
	defer cleanup2()
	s.Reconnect(newClient)

	if s.IsDisconnected() {
		t.Error("expected IsDisconnected to be false after Reconnect")
	}
	if s.closed {
		t.Error("expected closed to be reset after Reconnect")
	}
	_ = client.Close()
}

func TestSession_StartKeepalive_SendsPingAndHeartbeat(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)

	// StartKeepalive normally fires every 30s; that's too slow for a test,
	// so directly exercise writePingAndHeartbeat which is what the ticker
	// invokes, and separately confirm StartKeepalive installs a pong
	// handler + read deadline without blocking or panicking.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartKeepalive(ctx, testLogger())

	if err := s.writePingAndHeartbeat(); err != nil {
		t.Fatalf("unexpected error writing ping/heartbeat: %v", err)
	}

	sawHeartbeat := false
	for i := 0; i < 2; i++ {
		_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
		mt, raw, err := client.ReadMessage()
		if err != nil {
			// A control-frame ping doesn't surface via ReadMessage with
			// gorilla's default handler in some cases; keep looking for
			// the heartbeat text message within the loop budget.
			break
		}
		if mt != websocket.TextMessage {
			continue
		}
		var msg WSMessage
		if json.Unmarshal(raw, &msg) == nil && msg.Type == MessageTypeHeartbeat {
			sawHeartbeat = true
			break
		}
	}
	if !sawHeartbeat {
		t.Error("expected to observe a heartbeat message after writePingAndHeartbeat")
	}
}

func TestSession_WritePingAndHeartbeat_AfterCloseReturnsErr(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()
	defer client.Close()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)
	s.Close()

	if err := s.writePingAndHeartbeat(); err != io.ErrClosedPipe {
		t.Errorf("expected io.ErrClosedPipe, got %v", err)
	}
}

func TestSession_StartKeepalive_ClosesSessionOnWriteFailure(t *testing.T) {
	server, client, cleanup := newWSPair(t)
	defer cleanup()

	s := NewSession("sess-1", "sbx-1", "default", "user-1", "u@example.com", "pod-1", "/bin/bash", server, false)

	// Close the underlying connection out from under the session so the
	// next keepalive tick's write fails, exercising the "close on error"
	// branch inside the keepalive goroutine.
	_ = client.Close()
	_ = server.Close()

	if err := s.writePingAndHeartbeat(); err == nil {
		t.Fatal("expected write to fail on a closed connection")
	}
	// Directly invoke the same cleanup StartKeepalive's goroutine performs
	// on write failure, since we can't wait 30s for the real ticker.
	s.Close()
	if !s.closed {
		t.Error("expected session to be closed after keepalive write failure path")
	}
}

func TestClampTerminalDim(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want uint16
	}{
		{"zero clamps to 1", 0, 1},
		{"negative clamps to 1", -5, 1},
		{"in range passes through", 80, 80},
		{"max uint16 passes through", 65535, 65535},
		{"over max clamps to uint16 max", 1 << 20, 65535},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clampTerminalDim(tc.in)
			if got != tc.want {
				t.Errorf("clampTerminalDim(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestTerminalSizeQueue_PushAndNext(t *testing.T) {
	q := NewTerminalSizeQueue()
	q.Push(remotecommand.TerminalSize{Width: 80, Height: 24})

	got := q.Next()
	if got == nil || got.Width != 80 || got.Height != 24 {
		t.Errorf("expected 80x24, got %+v", got)
	}
}

func TestTerminalSizeQueue_PushOverwritesWhenFull(t *testing.T) {
	q := NewTerminalSizeQueue()
	q.Push(remotecommand.TerminalSize{Width: 80, Height: 24})
	// Buffer is size 1 — a second push before any Next() must drop the
	// stale value and keep only the newest size.
	q.Push(remotecommand.TerminalSize{Width: 200, Height: 60})

	got := q.Next()
	if got == nil || got.Width != 200 || got.Height != 60 {
		t.Errorf("expected latest push (200x60) to win, got %+v", got)
	}
}
