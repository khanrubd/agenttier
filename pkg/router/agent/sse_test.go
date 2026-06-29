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
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestSSEWriter_ConcurrentStreamsShareLock is the regression guard for the
// data race where withStream() gave each derived writer its OWN mutex while
// sharing the underlying ResponseWriter. stdout, stderr, and the keepalive
// writer all hammer the same ResponseWriter concurrently; with a per-writer
// mutex this is a data race (run with -race) and produces interleaved,
// corrupted SSE frames. With the shared mutex it is clean.
func TestSSEWriter_ConcurrentStreamsShareLock(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, ok := newSSEWriter(rec)
	if !ok {
		t.Fatal("recorder should support flushing")
	}

	stdoutW := sw.withStream("stdout")
	stderrW := sw.withStream("stderr")

	const iters = 300
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _ = stdoutW.Write([]byte("stdout-line\n"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _ = stderrW.Write([]byte("stderr-line\n"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			sw.WriteRaw(": keepalive\n\n")
		}
	}()
	wg.Wait()

	body := rec.Body.String()
	if !strings.Contains(body, "event: log") {
		t.Fatal("expected log events in the SSE output")
	}
	// Every SSE data frame must be well-formed: a "data: " line is always
	// followed by the blank-line terminator. A torn frame from interleaved
	// writes would leave a "data:" without its trailing "\n\n".
	for _, frame := range strings.Split(body, "event: log\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		if strings.HasPrefix(frame, "data: ") && !strings.Contains(frame, "\"stream\"") {
			t.Fatalf("torn SSE frame detected: %q", frame)
		}
	}
}
