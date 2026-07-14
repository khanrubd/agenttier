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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// testLogger returns a slog.Logger that discards output — tests care about
// behavior, not log lines, and a nil logger would panic the code under test.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newWSPair spins up a local httptest server that upgrades one connection,
// dials it from the client side, and returns both ends plus a cleanup func.
// `server` is the conn a real Session would hold (the Router's side of the
// upgrade); `client` is the test's stand-in for the browser, used to feed
// input and observe output.
func newWSPair(t *testing.T) (server, client *websocket.Conn, cleanup func()) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	connCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connCh <- c
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial test websocket: %v", err)
	}

	serverConn := <-connCh

	return serverConn, clientConn, func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		srv.Close()
	}
}

// workingClientsetForTest builds a real *kubernetes.Clientset from a valid
// (but unreachable) rest.Config, so CoreV1().RESTClient() is non-nil and
// Bridge methods don't panic before reaching the code path under test.
func workingClientsetForTest(t *testing.T) kubernetes.Interface {
	t.Helper()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: "https://127.0.0.1:6443"})
	if err != nil {
		t.Fatalf("build test clientset: %v", err)
	}
	return cs
}

// brokenExecutorRestConfig returns a rest.Config that fails inside
// spdy.RoundTripperFor (via rest.TLSConfigFor) because the referenced cert/key
// files don't exist — used to exercise Bridge's "failed to create executor"
// error branches without a live apiserver.
func brokenExecutorRestConfig() *rest.Config {
	return &rest.Config{
		Host: "https://127.0.0.1:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CertFile: "/nonexistent/cert.pem",
			KeyFile:  "/nonexistent/key.pem",
		},
	}
}
