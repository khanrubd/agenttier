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

package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookChannel_Name(t *testing.T) {
	w := NewWebhookChannel("https://example.com/hook")
	if w.Name() != "webhook" {
		t.Errorf("Name() = %q, want %q", w.Name(), "webhook")
	}
}

func TestWebhookChannel_Send_Success(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := NewWebhookChannel(srv.URL)
	err := ch.Send(context.Background(), &Notification{
		Type:      NotifyError,
		Message:   "boom",
		Timestamp: time.Unix(1700000000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody["message"] != "boom" {
		t.Errorf("webhook body message = %v, want boom", gotBody["message"])
	}
}

func TestWebhookChannel_Send_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ch := NewWebhookChannel(srv.URL)
	if err := ch.Send(context.Background(), &Notification{Type: NotifyError, Message: "x"}); err == nil {
		t.Error("expected an error on 500 response")
	}
}

func TestWebhookChannel_Send_InvalidURL(t *testing.T) {
	ch := NewWebhookChannel("://not-a-valid-url")
	if err := ch.Send(context.Background(), &Notification{Type: NotifyError, Message: "x"}); err == nil {
		t.Error("expected an error for an invalid webhook URL")
	}
}

func TestWebhookChannel_Send_ConnectionRefused(t *testing.T) {
	// Port 0 on an unroutable-in-practice address to force a dial failure
	// without depending on external network state.
	ch := NewWebhookChannel("http://127.0.0.1:1")
	if err := ch.Send(context.Background(), &Notification{Type: NotifyError, Message: "x"}); err == nil {
		t.Error("expected an error when the webhook endpoint is unreachable")
	}
}

func TestSlackChannel_Name(t *testing.T) {
	s := NewSlackChannel("https://hooks.slack.com/services/x")
	if s.Name() != "slack" {
		t.Errorf("Name() = %q, want %q", s.Name(), "slack")
	}
}

func TestSlackChannel_Send_Success(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := NewSlackChannel(srv.URL)
	err := ch.Send(context.Background(), &Notification{
		Type:        NotifyAutoRestart,
		SandboxName: "sb-1",
		UserEmail:   "owner@example.com",
		Message:     "restarted",
		Timestamp:   time.Unix(1700000000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, ok := gotBody["blocks"]; !ok {
		t.Errorf("slack payload missing 'blocks' key: %v", gotBody)
	}
}

func TestSlackChannel_Send_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Slack's channel only accepts exactly 200, unlike the webhook
		// channel's ">= 400" check — 201 must also fail here.
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	ch := NewSlackChannel(srv.URL)
	if err := ch.Send(context.Background(), &Notification{Type: NotifyError, Message: "x"}); err == nil {
		t.Error("expected an error on non-200 response")
	}
}

func TestSlackChannel_Send_InvalidURL(t *testing.T) {
	ch := NewSlackChannel("://not-a-valid-url")
	if err := ch.Send(context.Background(), &Notification{Type: NotifyError, Message: "x"}); err == nil {
		t.Error("expected an error for an invalid Slack webhook URL")
	}
}

func TestEmailChannel_Name(t *testing.T) {
	e := NewEmailChannel("smtp.example.com", 587, "u", "p", "from@example.com")
	if e.Name() != "email" {
		t.Errorf("Name() = %q, want %q", e.Name(), "email")
	}
}

func TestSubjectFor_AllKnownTypes(t *testing.T) {
	cases := map[NotificationType]string{
		NotifyIdleWarning:     "Sandbox idle warning",
		NotifyError:           "Sandbox error",
		NotifyAutoRestart:     "Sandbox auto-restarted",
		NotifySharedChange:    "Sandbox shared with you",
		NotifyGovernanceLimit: "Governance limit reached",
		NotifyErrorSpike:      "Error spike detected",
	}
	for typ, want := range cases {
		if got := subjectFor(typ); got != want {
			t.Errorf("subjectFor(%q) = %q, want %q", typ, got, want)
		}
	}
	// Unknown type falls back to the raw string value.
	if got := subjectFor(NotifySandboxStopped); got != string(NotifySandboxStopped) {
		t.Errorf("subjectFor(unmapped) = %q, want %q", got, string(NotifySandboxStopped))
	}
}
