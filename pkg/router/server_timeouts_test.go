/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package router

import (
	"testing"
	"time"
)

// TestServer_TimeoutsBalanceWebSocketsVsSlowloris confirms the http.Server
// is constructed with timeouts that allow long-lived WebSocket connections
// (ReadTimeout: 0) while still bounding the request-line + headers phase
// (ReadHeaderTimeout > 0) so a slow client can't hold a goroutine + fd open
// indefinitely just by dribbling header bytes.
//
// Regression coverage for the P1 "Router Read timeout disabled globally"
// bug: the previous code set ReadTimeout: 0 alone, leaving every endpoint
// (REST, SSE, file PUT/GET, port-preview) exposed to slowloris.
func TestServer_TimeoutsBalanceWebSocketsVsSlowloris(t *testing.T) {
	s, _ := buildTestServer(t)

	if s.httpServer == nil {
		t.Fatal("httpServer not initialized")
	}
	// WebSocket support requires this.
	if s.httpServer.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %v, want 0 (required for WebSocket)", s.httpServer.ReadTimeout)
	}
	// Slowloris protection requires this.
	if s.httpServer.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want > 0 (slowloris protection)", s.httpServer.ReadHeaderTimeout)
	}
	// Sanity-check it isn't unreasonably large — 30s is the high end of
	// "reasonable for a WAN-scale client." 5s is what we set; anything
	// over 30s deserves a closer look at why.
	if s.httpServer.ReadHeaderTimeout > 30*time.Second {
		t.Errorf("ReadHeaderTimeout = %v is too generous; legitimate clients finish headers in well under a second",
			s.httpServer.ReadHeaderTimeout)
	}
	// IdleTimeout still set so abandoned keep-alives don't pile up.
	if s.httpServer.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want > 0", s.httpServer.IdleTimeout)
	}
}
