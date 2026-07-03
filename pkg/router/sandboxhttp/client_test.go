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

package sandboxhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_HealthzHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("expected /healthz, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if err := c.Healthz(context.Background()); err != nil {
		t.Errorf("Healthz returned %v, want nil", err)
	}
}

func TestClient_Healthz503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready: K8s API unreachable"))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	err := c.Healthz(context.Background())
	if err == nil {
		t.Fatal("Healthz should error on 503")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error didn't mention 503: %v", err)
	}
}

func TestClient_ExecAttachesBearerToken(t *testing.T) {
	gotAuth := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(ExecResponse{ExitCode: 0, Stdout: "ok\n"})
	}))
	defer srv.Close()

	c := New(srv.URL, "secret-runtime-token")
	_, err := c.Exec(context.Background(), ExecRequest{Command: []string{"echo", "ok"}})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if want := "Bearer secret-runtime-token"; gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}

func TestClient_ExecReturnsResponse(t *testing.T) {
	want := ExecResponse{
		ExitCode:   42,
		Stdout:     "hello\n",
		Stderr:     "warn\n",
		DurationMs: 123,
		Truncated:  true,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		var req ExecRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Command) == 0 || req.Command[0] != "echo" {
			t.Errorf("server didn't get expected command: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	got, err := c.Exec(context.Background(), ExecRequest{
		Command: []string{"echo", "hello"},
		Stdin:   "world",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got.ExitCode != want.ExitCode || got.Stdout != want.Stdout ||
		got.Stderr != want.Stderr || got.DurationMs != want.DurationMs ||
		got.Truncated != want.Truncated {
		t.Errorf("ExecResponse mismatch:\n got %+v\n want %+v", got, want)
	}
}

func TestClient_ExecPropagatesContextCancellation(t *testing.T) {
	// If the caller cancels their context the client should abort the
	// in-flight request rather than waiting for the runtime's full
	// timeout. Verifies http.NewRequestWithContext is wired correctly.
	blocker := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker // never fires — client cancellation should kill the request
	}))
	defer func() {
		close(blocker)
		srv.Close()
	}()

	c := New(srv.URL, "t")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Exec(ctx, ExecRequest{Command: []string{"sleep", "100"}})
	if err == nil {
		t.Fatal("expected error when context cancelled, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
		// httptest sometimes wraps the context error — accept either spelling.
		t.Errorf("expected deadline-related error, got %v", err)
	}
}

func TestClient_ExecHandlesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"runtime crashed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	_, err := c.Exec(context.Background(), ExecRequest{Command: []string{"true"}})
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error didn't mention 500: %v", err)
	}
}

func TestClient_RejectsEmptyBaseURL(t *testing.T) {
	c := &Client{} // zero value — BaseURL deliberately unset

	if err := c.Healthz(context.Background()); err == nil {
		t.Error("Healthz with empty BaseURL should error")
	}
	if _, err := c.Exec(context.Background(), ExecRequest{Command: []string{"true"}}); err == nil {
		t.Error("Exec with empty BaseURL should error")
	}
}

func TestClient_NoTokenWhenEmpty(t *testing.T) {
	gotAuth := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(ExecResponse{ExitCode: 0})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.Exec(context.Background(), ExecRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization sent despite empty token: %q", gotAuth)
	}
}

func TestClient_InvokeStreamForwardsEvents(t *testing.T) {
	// Simulate the in-pod runtime emitting start + log + exit. Verify
	// the client's parser dispatches each event to the callback in
	// order with correct EventType + Data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		// Three events back-to-back with the keepalive comment shape.
		_, _ = w.Write([]byte("event: start\ndata: {\"invokeId\":\"x\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte(": keepalive\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("event: log\ndata: {\"stream\":\"stdout\",\"data\":\"hi\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("event: exit\ndata: {\"exitCode\":0,\"reason\":\"completed\"}\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	var events []InvokeEvent
	err := c.InvokeStream(context.Background(), InvokeRequest{Command: []string{"echo", "hi"}}, func(ev InvokeEvent) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	wantTypes := []string{"start", "log", "exit"}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d (events=%+v)", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].EventType != want {
			t.Errorf("events[%d].EventType = %q, want %q", i, events[i].EventType, want)
		}
	}
}

func TestClient_InvokeCancelHappyPath(t *testing.T) {
	gotPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	if err := c.InvokeCancel(context.Background(), "inv-abc"); err != nil {
		t.Fatalf("InvokeCancel: %v", err)
	}
	if gotPath != "/invoke/cancel/inv-abc" {
		t.Errorf("path = %q, want /invoke/cancel/inv-abc", gotPath)
	}
}

func TestClient_InvokeCancelMissingID(t *testing.T) {
	c := New("http://example.com", "")
	if err := c.InvokeCancel(context.Background(), ""); err == nil {
		t.Error("InvokeCancel with empty ID should error")
	}
}

// DialPTY URL construction tests. These don't actually establish a
// WebSocket — that requires the server side to consent to an upgrade,
// which httptest.Server doesn't do by default — but they do verify the
// pre-dial URL gets built right and that schemes / params translate
// the way we expect.

func TestDialPTY_RequiresShell(t *testing.T) {
	c := New("http://10.0.0.1:9000", "tok")
	_, err := c.DialPTY(context.Background(), PTYOptions{})
	if err == nil || !strings.Contains(err.Error(), "Shell is required") {
		t.Errorf("expected 'Shell is required' error, got %v", err)
	}
}

func TestDialPTY_RequiresHTTPBaseURL(t *testing.T) {
	c := New("ftp://nope", "tok")
	_, err := c.DialPTY(context.Background(), PTYOptions{Shell: "/bin/bash"})
	if err == nil || !strings.Contains(err.Error(), "must start with http") {
		t.Errorf("expected scheme-rejection error, got %v", err)
	}
}

func TestDialPTY_EmptyBaseURL(t *testing.T) {
	c := &Client{}
	_, err := c.DialPTY(context.Background(), PTYOptions{Shell: "/bin/bash"})
	if err == nil || !strings.Contains(err.Error(), "BaseURL is empty") {
		t.Errorf("expected 'BaseURL is empty' error, got %v", err)
	}
}

// urlQueryEscape is exported only via the unexported helper, but we want
// to confirm a couple of key cases — in particular that paths with
// slashes and spaces survive a round trip via url.QueryEscape (they do).
func TestURLQueryEscape_KeyCases(t *testing.T) {
	cases := map[string]string{
		"/bin/bash":       "%2Fbin%2Fbash",
		"/usr/bin/zsh":    "%2Fusr%2Fbin%2Fzsh",
		"path with space": "path+with+space",
		"weird&value":     "weird%26value",
	}
	for in, want := range cases {
		got := urlQueryEscape(in)
		if got != want {
			t.Errorf("urlQueryEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
