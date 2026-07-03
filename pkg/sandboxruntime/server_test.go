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

package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHealthz_NoAuthRequired(t *testing.T) {
	s := New(Config{AuthToken: "should-not-be-checked"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.handleHealthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !bytes.Contains(body, []byte(`"status":"ok"`)) {
		t.Errorf("body = %q, want status:ok", body)
	}
}

func TestRequireAuth_RejectsMissingToken(t *testing.T) {
	s := New(Config{AuthToken: "real-token"})
	called := false
	wrapped := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("handler was called despite missing token")
	}
}

func TestRequireAuth_RejectsWrongToken(t *testing.T) {
	s := New(Config{AuthToken: "real-token"})
	wrapped := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_AcceptsCorrectToken(t *testing.T) {
	s := New(Config{AuthToken: "real-token"})
	wrapped := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Bearer real-token")
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRequireAuth_DisabledWhenTokenEmpty(t *testing.T) {
	// Empty AuthToken is the test-mode escape hatch — production deploys
	// always set a token. Verify the empty-token branch is genuinely
	// permissive (no 401) so test fixtures don't have to wire auth.
	s := New(Config{AuthToken: ""})
	wrapped := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("empty-token mode returned %d, want 200", rec.Code)
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	// End-to-end: start the server on a real port, hit /healthz, then
	// cancel the context and verify Start returns nil within the
	// shutdown window.
	addr := freePort(t)
	s := New(Config{
		ListenAddr:      addr,
		ShutdownTimeout: 1 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	// Poll /healthz until the server is up (< 100ms in practice).
	if err := pollHealthz("http://"+addr+"/healthz", 2*time.Second); err != nil {
		cancel()
		<-done
		t.Fatalf("server didn't come up: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned unexpected error on shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start didn't return within shutdown window")
	}
}

func TestExec_HandlerHappyPath(t *testing.T) {
	// Inject a stub executor so the test doesn't shell out — keeps
	// cross-platform behavior predictable and avoids racing the real
	// /bin/sh.
	stub := &stubExecutor{out: ExecResponse{ExitCode: 0, Stdout: "hello\n", DurationMs: 5}}
	s := New(Config{AuthToken: "t"})
	s.SetExecutor(stub)

	body, _ := json.Marshal(ExecRequest{Command: []string{"echo", "hello"}})
	req := httptest.NewRequest(http.MethodPost, "/exec", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	s.requireAuth(s.handleExec)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp ExecResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExitCode != 0 || resp.Stdout != "hello\n" {
		t.Errorf("response = %+v", resp)
	}
	if !stub.called {
		t.Error("executor not invoked")
	}
}

func TestExec_RejectsNonPOST(t *testing.T) {
	s := New(Config{})
	rec := httptest.NewRecorder()
	s.handleExec(rec, httptest.NewRequest(http.MethodGet, "/exec", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestExec_RejectsEmptyCommand(t *testing.T) {
	s := New(Config{})
	body, _ := json.Marshal(ExecRequest{}) // no Command
	req := httptest.NewRequest(http.MethodPost, "/exec", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleExec(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestExec_RejectsUnknownFields(t *testing.T) {
	// DisallowUnknownFields means a typo'd field is a 400 instead of
	// silently ignored. Keeps the wire contract tight.
	s := New(Config{})
	req := httptest.NewRequest(http.MethodPost, "/exec",
		strings.NewReader(`{"command":["echo"],"unknownField":true}`))
	rec := httptest.NewRecorder()
	s.handleExec(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDefaultExecutor_Echo(t *testing.T) {
	// Real shell-out — verifies the production code path against /bin/sh
	// or /bin/echo. Skipped on non-Unix because Windows lacks /bin/echo
	// (and the runtime ships only on Linux containers anyway).
	if !hasUnixShell() {
		t.Skip("requires unix shell")
	}
	resp := defaultExecutor{}.Execute(context.Background(), ExecRequest{
		Command: []string{"/bin/echo", "ok"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%q)", resp.ExitCode, resp.Stderr)
	}
	if !strings.Contains(resp.Stdout, "ok") {
		t.Errorf("stdout = %q, want it to contain 'ok'", resp.Stdout)
	}
	if resp.DurationMs < 0 {
		t.Errorf("duration_ms = %d, want >= 0", resp.DurationMs)
	}
}

func TestDefaultExecutor_NonZeroExit(t *testing.T) {
	if !hasUnixShell() {
		t.Skip("requires unix shell")
	}
	resp := defaultExecutor{}.Execute(context.Background(), ExecRequest{
		Command: []string{"/bin/sh", "-c", "exit 7"},
	})
	if resp.ExitCode != 7 {
		t.Errorf("exitCode = %d, want 7", resp.ExitCode)
	}
}

func TestDefaultExecutor_ExplicitTimeout(t *testing.T) {
	if !hasUnixShell() {
		t.Skip("requires unix shell")
	}
	start := time.Now()
	resp := defaultExecutor{}.Execute(context.Background(), ExecRequest{
		Command:        []string{"/bin/sh", "-c", "sleep 10"},
		TimeoutSeconds: 1,
	})
	elapsed := time.Since(start)
	if !resp.TimedOut {
		t.Errorf("TimedOut = false, want true (resp=%+v)", resp)
	}
	if elapsed > 3*time.Second {
		t.Errorf("timeout took %v, want < 3s", elapsed)
	}
}

func TestCappedBuffer_TruncatesPastMax(t *testing.T) {
	b := &cappedBuffer{max: 10}
	n, err := b.Write([]byte("0123456789ABCDEF"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 16 {
		t.Errorf("wrote %d, want 16 (writer must consume even after truncation to avoid blocking the producer)", n)
	}
	if !b.truncated {
		t.Error("truncated flag not set")
	}
	got := b.String()
	if !strings.HasSuffix(got, "[truncated]") {
		t.Errorf("expected truncation marker, got %q", got)
	}
	if !strings.HasPrefix(got, "0123456789") {
		t.Errorf("expected first 10 bytes intact, got %q", got)
	}
}

func TestCappedBuffer_BelowMaxIsExact(t *testing.T) {
	b := &cappedBuffer{max: 100}
	_, _ = b.Write([]byte("hello"))
	if b.truncated {
		t.Error("truncated flag set despite under-cap write")
	}
	if b.String() != "hello" {
		t.Errorf("buf = %q, want %q", b.String(), "hello")
	}
}

// stubExecutor satisfies the commandExecutor interface for handler-level
// tests without spawning real processes.
type stubExecutor struct {
	called bool
	out    ExecResponse
	last   ExecRequest
}

func (s *stubExecutor) Execute(_ context.Context, req ExecRequest) ExecResponse {
	s.called = true
	s.last = req
	return s.out
}

// freePort grabs an OS-assigned port and returns it as host:port. We close
// the listener immediately — there's a tiny race where the OS could
// reassign the port to another process, but it's fine for tests.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func pollHealthz(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	c := http.Client{Timeout: 200 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := c.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return io.ErrUnexpectedEOF
}

// hasUnixShell returns true on Linux/Darwin/etc. The sandbox runtime ships
// only on Linux containers, but tests run on every CI matrix entry.
func hasUnixShell() bool {
	if _, err := os.Stat("/bin/sh"); err == nil {
		return true
	}
	return false
}
