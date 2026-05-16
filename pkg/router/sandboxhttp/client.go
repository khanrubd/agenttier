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

// Package sandboxhttp is the Router-side HTTP client that talks to the
// in-pod sandbox-runtime server (pkg/sandboxruntime). When a Sandbox is
// configured with HarnessSpec.UseHTTPExec=true, the Router proxies /exec
// requests through this client instead of going through the legacy SPDY
// exec path.
//
// Phase 3 ships only the client + types. No Router handler dispatches
// through it yet — that's phase 4. Defining the surface now in its own
// package keeps phase 4's diff focused on routing logic.
package sandboxhttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultDialTimeout caps connect-establish time. Pod IPs change on every
// restart and a stale Endpoints entry would manifest as a connect timeout
// — short timeout means the Router falls back to SPDY (or returns an
// error to the caller) quickly rather than queuing.
const DefaultDialTimeout = 5 * time.Second

// DefaultRequestTimeout caps total wall-clock per /exec call. Longer than
// the runtime's MaxExecTimeout (30 min) doesn't add value because the
// runtime gives up first. We pad slightly so a runtime-side timeout is
// the diagnosable error rather than a client-side one.
const DefaultRequestTimeout = 31 * time.Minute

// ExecRequest mirrors sandboxruntime.ExecRequest. Defined here so the
// Router doesn't import the in-pod runtime package directly — that would
// pull os/exec into the Router binary for no reason.
type ExecRequest struct {
	Command        []string          `json:"command"`
	Stdin          string            `json:"stdin,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	WorkingDir     string            `json:"workingDir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

// ExecResponse mirrors sandboxruntime.ExecResponse.
type ExecResponse struct {
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"durationMs"`
	TimedOut   bool   `json:"timedOut"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// Client is the Router-side HTTP client for one sandbox. Cheap to
// construct (no connection state) — handlers create one per request.
type Client struct {
	// BaseURL is the full URL the runtime listens on, typically
	// `http://<pod-ip>:9000`. The Router resolves the pod IP from the
	// Sandbox.status.podName + the Pod's Status.PodIP.
	BaseURL string

	// Token is the bearer token the runtime expects (matches
	// AGENTTIER_RUNTIME_TOKEN inside the pod). Read from the per-sandbox
	// Secret via controller.ReadRuntimeToken.
	Token string

	// HTTPClient lets callers inject a custom http.Client (mainly for
	// tests). Nil falls back to a sensible default.
	HTTPClient *http.Client
}

// New returns a Client with sane defaults — connect timeout, total
// request timeout, no per-host pooling (each call gets its own
// connection, fine for the relatively low /exec QPS).
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: DefaultRequestTimeout,
			// Default Transport is fine — no client-cert auth and the
			// per-pod IP changes on every restart so connection reuse
			// across requests has limited value.
			Transport: &http.Transport{
				ResponseHeaderTimeout: DefaultDialTimeout,
				DisableKeepAlives:     false,
				IdleConnTimeout:       30 * time.Second,
			},
		},
	}
}

// Healthz dials the runtime's unauthenticated /healthz endpoint. Useful
// for the Router to verify the runtime is up before swapping a /exec call
// from SPDY to HTTP — if the in-pod server isn't reachable we want to
// fall back, not 502 the user.
//
// Returns nil when the runtime responds 200 OK with the expected body.
// Any other outcome (network error, non-200, malformed body) returns a
// descriptive error so callers can log + decide.
func (c *Client) Healthz(ctx context.Context) error {
	if c.BaseURL == "" {
		return fmt.Errorf("sandboxhttp.Client: BaseURL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("healthz returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// Exec runs a command on the sandbox via POST /exec. Returns the parsed
// ExecResponse on success; returns an error for transport / decode
// failures. A non-zero exit code is NOT an error — it's reported in the
// returned response just like the SPDY path does today.
func (c *Client) Exec(ctx context.Context, req ExecRequest) (*ExecResponse, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("sandboxhttp.Client: BaseURL is empty")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal exec request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/exec", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("exec returned %d: %s", resp.StatusCode, raw)
	}

	var out ExecResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode exec response: %w", err)
	}
	return &out, nil
}

// do attaches the bearer token and dispatches via the configured HTTP
// client. Pulled out so Healthz and Exec share auth + transport setup.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultRequestTimeout}
	}
	return httpClient.Do(req)
}


// InvokeRequest mirrors sandboxruntime.InvokeRequest. Defined here so the
// Router doesn't import the in-pod runtime package directly.
type InvokeRequest struct {
	Command        []string          `json:"command"`
	Stdin          string            `json:"stdin,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	WorkingDir     string            `json:"workingDir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	InvokeID       string            `json:"invokeId,omitempty"`
}

// InvokeEvent is one SSE event from the runtime's /invoke stream.
// EventType is one of "start", "log", "exit". Data is the JSON payload
// the runtime emitted under `data: ...` for that event.
type InvokeEvent struct {
	EventType string
	Data      []byte
}

// InvokeStream calls /invoke and streams events back via the supplied
// callback. Returns when the runtime closes the stream (after the
// "exit" event), the context cancels, or the underlying HTTP transport
// errors.
//
// The callback runs synchronously on the receive goroutine — long-
// running work in onEvent backpressures the runtime via TCP. That's
// usually what you want for a pass-through proxy.
func (c *Client) InvokeStream(ctx context.Context, req InvokeRequest, onEvent func(InvokeEvent) error) error {
	if c.BaseURL == "" {
		return fmt.Errorf("sandboxhttp.Client: BaseURL is empty")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal invoke request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/invoke", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// Streaming responses need an http.Client with no Timeout (the
	// caller's ctx provides the bound). Reuse the client's Transport
	// so connection pooling still helps but unset Timeout for the
	// streamed call.
	streamClient := *c.streamingClient()
	streamClient.Timeout = 0

	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("invoke returned %d: %s", resp.StatusCode, raw)
	}

	return parseSSE(resp.Body, onEvent)
}

// InvokeCancel addresses /invoke/cancel/<invokeID>. Returns nil on 204,
// a sentinel-equivalent error on 404 (caller can use errors.Is /
// strings.Contains "no in-flight invoke" to detect already-completed),
// and a transport error otherwise.
func (c *Client) InvokeCancel(ctx context.Context, invokeID string) error {
	if c.BaseURL == "" {
		return fmt.Errorf("sandboxhttp.Client: BaseURL is empty")
	}
	if invokeID == "" {
		return fmt.Errorf("invokeID is required")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/invoke/cancel/"+invokeID, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("invoke cancel returned %d: %s", resp.StatusCode, raw)
}

// streamingClient returns the configured HTTPClient or a fresh default
// when none is set. Used by InvokeStream which needs to clone-and-
// modify the timeout before issuing a streamed request.
func (c *Client) streamingClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{
		Timeout: DefaultRequestTimeout,
		Transport: &http.Transport{
			ResponseHeaderTimeout: DefaultDialTimeout,
		},
	}
}

// parseSSE consumes the runtime's SSE response and dispatches each event
// to onEvent. Returns nil on graceful end-of-stream (server closed),
// the underlying error otherwise.
//
// The runtime's wire format is the bare minimum SSE: `event: NAME\ndata:
// JSON\n\n`. We don't support `id:`, `retry:`, or multi-line data
// because the runtime never emits them. Keep the parser tight to
// reduce surface area.
func parseSSE(body io.Reader, onEvent func(InvokeEvent) error) error {
	scanner := bufio.NewScanner(body)
	// SSE events are unbounded in theory; in practice the runtime
	// emits 4 KiB log chunks. Allocate 1 MiB max line so a chatty
	// stdout chunk can't error out the parser.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var current InvokeEvent
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			// Blank line = event boundary.
			if current.EventType != "" || len(current.Data) > 0 {
				if err := onEvent(current); err != nil {
					return err
				}
			}
			current = InvokeEvent{}
		case strings.HasPrefix(line, "event:"):
			current.EventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			current.Data = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.HasPrefix(line, ":"):
			// Comment (keepalive). Ignore.
		}
	}
	return scanner.Err()
}
