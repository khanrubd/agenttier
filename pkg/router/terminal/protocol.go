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

// Package terminal implements the WebSocket terminal with PTY bridging.
package terminal

import "encoding/json"

// MessageType defines the type of WebSocket message.
type MessageType string

const (
	// Client → Server messages
	MessageTypeInput  MessageType = "input"
	MessageTypeResize MessageType = "resize"
	MessageTypePing   MessageType = "ping"

	// Server → Client messages
	MessageTypeOutput MessageType = "output"
	MessageTypeError  MessageType = "error"
	MessageTypeClose  MessageType = "close"
	MessageTypePong   MessageType = "pong"
)

// CloseReason defines why a terminal session was closed.
type CloseReason string

const (
	CloseReasonSandboxStopped CloseReason = "sandbox_stopped"
	CloseReasonSandboxDeleted CloseReason = "sandbox_deleted"
	CloseReasonIdleTimeout    CloseReason = "idle_timeout"
	CloseReasonSessionExpired CloseReason = "session_expired"
	CloseReasonRevoked        CloseReason = "access_revoked"
	CloseReasonError          CloseReason = "error"
)

// WSMessage is the JSON message format for the terminal WebSocket protocol.
type WSMessage struct {
	Type    MessageType `json:"type"`
	Data    string      `json:"data,omitempty"`
	Stream  string      `json:"stream,omitempty"` // "stdout" or "stderr"
	Cols    int         `json:"cols,omitempty"`
	Rows    int         `json:"rows,omitempty"`
	Reason  CloseReason `json:"reason,omitempty"`
	Code    int         `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

// MarshalInput creates an input message.
func MarshalInput(data string) ([]byte, error) {
	return json.Marshal(WSMessage{Type: MessageTypeInput, Data: data})
}

// MarshalOutput creates an output message.
func MarshalOutput(stream, data string) ([]byte, error) {
	return json.Marshal(WSMessage{Type: MessageTypeOutput, Stream: stream, Data: data})
}

// MarshalResize creates a resize message.
func MarshalResize(cols, rows int) ([]byte, error) {
	return json.Marshal(WSMessage{Type: MessageTypeResize, Cols: cols, Rows: rows})
}

// MarshalError creates an error message.
func MarshalError(message string) ([]byte, error) {
	return json.Marshal(WSMessage{Type: MessageTypeError, Message: message})
}

// MarshalClose creates a close message.
func MarshalClose(reason CloseReason, code int) ([]byte, error) {
	return json.Marshal(WSMessage{Type: MessageTypeClose, Reason: reason, Code: code})
}

// ParseMessage parses a raw WebSocket message into a WSMessage.
func ParseMessage(data []byte) (*WSMessage, error) {
	msg := &WSMessage{}
	if err := json.Unmarshal(data, msg); err != nil {
		return nil, err
	}
	return msg, nil
}
