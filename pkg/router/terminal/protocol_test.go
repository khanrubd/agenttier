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
	"encoding/json"
	"testing"
)

func TestMarshalInput(t *testing.T) {
	data, err := MarshalInput("ls -la\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if msg.Type != MessageTypeInput {
		t.Errorf("expected type=input, got %s", msg.Type)
	}
	if msg.Data != "ls -la\n" {
		t.Errorf("expected data='ls -la\\n', got %q", msg.Data)
	}
}

func TestMarshalOutput(t *testing.T) {
	data, err := MarshalOutput("stdout", "hello world\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if msg.Type != MessageTypeOutput {
		t.Errorf("expected type=output, got %s", msg.Type)
	}
	if msg.Stream != "stdout" {
		t.Errorf("expected stream=stdout, got %s", msg.Stream)
	}
	if msg.Data != "hello world\n" {
		t.Errorf("expected data='hello world\\n', got %q", msg.Data)
	}
}

func TestMarshalResize(t *testing.T) {
	data, err := MarshalResize(120, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if msg.Type != MessageTypeResize {
		t.Errorf("expected type=resize, got %s", msg.Type)
	}
	if msg.Cols != 120 {
		t.Errorf("expected cols=120, got %d", msg.Cols)
	}
	if msg.Rows != 40 {
		t.Errorf("expected rows=40, got %d", msg.Rows)
	}
}

func TestMarshalClose(t *testing.T) {
	data, err := MarshalClose(CloseReasonSandboxStopped, 4001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if msg.Type != MessageTypeClose {
		t.Errorf("expected type=close, got %s", msg.Type)
	}
	if msg.Reason != CloseReasonSandboxStopped {
		t.Errorf("expected reason=sandbox_stopped, got %s", msg.Reason)
	}
	if msg.Code != 4001 {
		t.Errorf("expected code=4001, got %d", msg.Code)
	}
}

func TestMarshalError(t *testing.T) {
	data, err := MarshalError("sandbox not running")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if msg.Type != MessageTypeError {
		t.Errorf("expected type=error, got %s", msg.Type)
	}
	if msg.Message != "sandbox not running" {
		t.Errorf("expected message='sandbox not running', got %q", msg.Message)
	}
}

func TestParseMessage_Input(t *testing.T) {
	raw := `{"type":"input","data":"echo hello\n"}`
	msg, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.Type != MessageTypeInput {
		t.Errorf("expected type=input, got %s", msg.Type)
	}
	if msg.Data != "echo hello\n" {
		t.Errorf("expected data='echo hello\\n', got %q", msg.Data)
	}
}

func TestParseMessage_Resize(t *testing.T) {
	raw := `{"type":"resize","cols":80,"rows":24}`
	msg, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.Type != MessageTypeResize {
		t.Errorf("expected type=resize, got %s", msg.Type)
	}
	if msg.Cols != 80 || msg.Rows != 24 {
		t.Errorf("expected 80x24, got %dx%d", msg.Cols, msg.Rows)
	}
}

func TestParseMessage_Invalid(t *testing.T) {
	_, err := ParseMessage([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// TestProtocolRoundTrip verifies that serializing and deserializing a message
// produces an identical struct (Property 9 from design).
func TestProtocolRoundTrip(t *testing.T) {
	messages := []WSMessage{
		{Type: MessageTypeInput, Data: "ls -la\n"},
		{Type: MessageTypeResize, Cols: 120, Rows: 40},
		{Type: MessageTypeOutput, Stream: "stdout", Data: "file1 file2\n"},
		{Type: MessageTypeError, Message: "connection lost"},
		{Type: MessageTypeClose, Reason: CloseReasonIdleTimeout, Code: 4002},
		{Type: MessageTypePing},
		{Type: MessageTypePong},
	}

	for _, original := range messages {
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal failed for %s: %v", original.Type, err)
		}

		var decoded WSMessage
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal failed for %s: %v", original.Type, err)
		}

		if decoded.Type != original.Type {
			t.Errorf("type mismatch: %s != %s", decoded.Type, original.Type)
		}
		if decoded.Data != original.Data {
			t.Errorf("data mismatch for %s: %q != %q", original.Type, decoded.Data, original.Data)
		}
		if decoded.Stream != original.Stream {
			t.Errorf("stream mismatch for %s", original.Type)
		}
		if decoded.Cols != original.Cols || decoded.Rows != original.Rows {
			t.Errorf("size mismatch for %s", original.Type)
		}
		if decoded.Reason != original.Reason {
			t.Errorf("reason mismatch for %s", original.Type)
		}
		if decoded.Code != original.Code {
			t.Errorf("code mismatch for %s", original.Type)
		}
		if decoded.Message != original.Message {
			t.Errorf("message mismatch for %s", original.Type)
		}
	}
}
