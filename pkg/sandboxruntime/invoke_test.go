/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestInvoke_HappyPathStreamsLogsAndExit exercises the SSE /invoke
// endpoint end-to-end: send a small command, read the stream, assert
// we got start + log(stdout) + exit events with the expected payloads.
func TestInvoke_HappyPathStreamsLogsAndExit(t *testing.T) {
	if !hasUnixShell() {
		t.Skip("requires unix shell")
	}
	s := New(Config{AuthToken: "t"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/invoke":
			s.handleInvoke(w, r)
		}
	}))
	defer srv.Close()

	body, _ := json.Marshal(InvokeRequest{
		Command: []string{"/bin/sh", "-c", "echo hello"},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/invoke", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	events := readSSE(t, resp.Body)
	// First should be "start" with a non-empty invokeId.
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events (start, log, exit), got %d: %+v", len(events), events)
	}
	if events[0].EventType != "start" {
		t.Errorf("first event = %q, want start", events[0].EventType)
	}
	// Last should be "exit" with exitCode 0.
	last := events[len(events)-1]
	if last.EventType != "exit" {
		t.Errorf("last event = %q, want exit", last.EventType)
	}
	var exitData struct {
		ExitCode int    `json:"exitCode"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(last.Data, &exitData); err != nil {
		t.Fatalf("decode exit: %v", err)
	}
	if exitData.ExitCode != 0 || exitData.Reason != "completed" {
		t.Errorf("exit = %+v, want {0, completed}", exitData)
	}

	// At least one stdout log carrying "hello".
	var sawHello bool
	for _, ev := range events {
		if ev.EventType == "log" && strings.Contains(string(ev.Data), "hello") {
			sawHello = true
			break
		}
	}
	if !sawHello {
		t.Errorf("no log event contained 'hello': %+v", events)
	}
}

// TestInvoke_RegistryEnablesCancel verifies that /invoke/cancel<id>
// terminates an in-flight invoke. Spins up a long-running command
// (`sleep 60`), starts the invoke, hits cancel from a second goroutine,
// asserts the stream ends with reason=canceled.
func TestInvoke_RegistryEnablesCancel(t *testing.T) {
	if !hasUnixShell() {
		t.Skip("requires unix shell")
	}
	s := New(Config{AuthToken: "t"})
	mux := http.NewServeMux()
	mux.HandleFunc("/invoke", s.handleInvoke)
	mux.HandleFunc("/invoke/cancel/", s.handleInvokeCancel)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(InvokeRequest{
		Command:  []string{"/bin/sh", "-c", "sleep 30"},
		InvokeID: "test-cancel",
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/invoke", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	defer resp.Body.Close()

	// Consume the start event before triggering cancel so the registry
	// has the entry in place. Reading at least one event also confirms
	// streaming works.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancelReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/invoke/cancel/test-cancel", nil)
		cancelResp, _ := http.DefaultClient.Do(cancelReq)
		if cancelResp != nil {
			_ = cancelResp.Body.Close()
		}
	}()

	events := readSSE(t, resp.Body)
	last := events[len(events)-1]
	if last.EventType != "exit" {
		t.Fatalf("last event = %q, want exit", last.EventType)
	}
	var exitData struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(last.Data, &exitData)
	if exitData.Reason != "canceled" {
		t.Errorf("reason = %q, want canceled (got events=%+v)", exitData.Reason, events)
	}
}

func TestInvokeCancel_404OnUnknownID(t *testing.T) {
	s := New(Config{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handleInvokeCancel(w, r)
	}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/invoke/cancel/no-such-id", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// readSSE consumes a text/event-stream response and returns the parsed
// events. Test-only — production parsing lives in pkg/router/sandboxhttp.
type sseEvent struct {
	EventType string
	Data      []byte
}

func readSSE(t *testing.T, body interface {
	Read(p []byte) (int, error)
},
) []sseEvent {
	t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	var events []sseEvent
	for _, raw := range strings.Split(string(buf), "\n\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var ev sseEvent
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				ev.EventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				ev.Data = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if ev.EventType != "" {
			events = append(events, ev)
		}
	}
	return events
}
