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
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// sseWriter is a minimal Server-Sent Events writer. Each WriteEvent call
// emits one event with the given name + JSON-encoded payload and flushes the
// HTTP response writer so the client receives bytes immediately.
//
// It is also an io.Writer — Write(p []byte) emits each non-empty line as a
// "stdout" event. /invoke uses this to stream entrypoint output line-by-line
// without buffering through the SDK.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	stream  string // "stdout" or "stderr" — set per-stream by withStream()
	mu      sync.Mutex
	pending []byte // partial line buffer, only flushed on newline
}

// newSSEWriter wires up SSE response headers and verifies the http.ResponseWriter
// supports flushing. Returns nil + an HTTP 500 if not (callers must check).
func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable nginx proxy buffering so events arrive at the client live.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return &sseWriter{w: w, flusher: flusher}, true
}

// withStream returns a copy of the writer with stream=name set so subsequent
// Write calls emit events tagged for stdout vs stderr. The underlying
// flusher and response writer are shared (and serialized via mu).
func (s *sseWriter) withStream(name string) *sseWriter {
	return &sseWriter{w: s.w, flusher: s.flusher, stream: name, mu: sync.Mutex{}}
}

// WriteEvent emits a single SSE event with the given name + payload encoded
// as JSON. Errors writing to the response are silently dropped — the most
// likely cause is a client disconnect, which we handle through ctx cancel.
func (s *sseWriter) WriteEvent(name string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode SSE payload: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// SSE event format: "event: name\ndata: <json>\n\n"
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, body); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// WriteRaw emits a comment line — used for keepalives during long idle
// periods so middleboxes don't terminate the connection.
func (s *sseWriter) WriteRaw(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprint(s.w, line)
	s.flusher.Flush()
}

// Write satisfies io.Writer. Each newline-delimited chunk becomes one SSE
// event tagged with the current stream name (stdout/stderr). Partial lines
// are buffered until a newline arrives so we never split a line across
// multiple events.
func (s *sseWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, p...)
	for {
		i := indexNewline(s.pending)
		if i < 0 {
			break
		}
		line := string(s.pending[:i])
		s.pending = s.pending[i+1:]
		stream := s.stream
		if stream == "" {
			stream = "stdout"
		}
		body, _ := json.Marshal(map[string]string{"stream": stream, "data": line})
		_, _ = fmt.Fprintf(s.w, "event: log\ndata: %s\n\n", body)
		s.flusher.Flush()
	}
	return len(p), nil
}

// flushPending emits any buffered partial line as a final event. Callers must
// invoke this after the underlying command completes so a trailing line that
// lacked a newline still reaches the client.
func (s *sseWriter) flushPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return
	}
	stream := s.stream
	if stream == "" {
		stream = "stdout"
	}
	body, _ := json.Marshal(map[string]string{"stream": stream, "data": string(s.pending)})
	_, _ = fmt.Fprintf(s.w, "event: log\ndata: %s\n\n", body)
	s.flusher.Flush()
	s.pending = nil
}

func indexNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}
