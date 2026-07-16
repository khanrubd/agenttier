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
	"io"
	"log/slog"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordingChannel captures the notifications it receives for assertions.
type recordingChannel struct {
	name string
	got  chan *Notification
}

func (c *recordingChannel) Name() string { return c.name }
func (c *recordingChannel) Send(_ context.Context, n *Notification) error {
	c.got <- n
	return nil
}

func TestNotifier_FansOutToNamedChannels(t *testing.T) {
	a := &recordingChannel{name: "a", got: make(chan *Notification, 1)}
	b := &recordingChannel{name: "b", got: make(chan *Notification, 1)}
	n := NewNotifier(discardLogger())
	n.RegisterChannel(a)
	n.RegisterChannel(b)

	n.Send(context.Background(), &Notification{Type: NotifyError, Message: "boom"}, []string{"a", "b", "missing"})

	for _, ch := range []*recordingChannel{a, b} {
		select {
		case got := <-ch.got:
			if got.Message != "boom" {
				t.Errorf("channel %s got %q, want boom", ch.name, got.Message)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("channel %s never received the notification", ch.name)
		}
	}
}

func TestNotifier_StampsTimestamp(t *testing.T) {
	a := &recordingChannel{name: "a", got: make(chan *Notification, 1)}
	n := NewNotifier(discardLogger())
	n.RegisterChannel(a)
	n.Send(context.Background(), &Notification{Type: NotifyError, Message: "x"}, []string{"a"})
	select {
	case got := <-a.got:
		if got.Timestamp.IsZero() {
			t.Error("expected Send to stamp a timestamp")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification received")
	}
}

func TestEmailChannel_FormatMessage(t *testing.T) {
	e := NewEmailChannel("smtp.example.com", 587, "user", "pass", "agenttier@example.com")
	msg := string(e.formatMessage(&Notification{
		Type:        NotifyIdleWarning,
		SandboxName: "sb-1",
		UserEmail:   "owner@example.com",
		Message:     "Your sandbox will stop soon.",
		Timestamp:   time.Unix(1700000000, 0).UTC(),
		Details:     map[string]string{"idleFor": "30m"},
	}))
	for _, want := range []string{
		"From: agenttier@example.com",
		"To: owner@example.com",
		"Subject: [AgentTier] Sandbox idle warning",
		"Content-Type: text/plain; charset=UTF-8",
		"Your sandbox will stop soon.",
		"Sandbox: sb-1",
		"idleFor: 30m",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n---\n%s", want, msg)
		}
	}
}

func TestEmailChannel_Send(t *testing.T) {
	var gotTo []string
	var gotMsg []byte
	e := NewEmailChannel("smtp.example.com", 25, "", "", "from@example.com")
	e.sendMail = func(_ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
		gotTo, gotMsg = to, msg
		return nil
	}
	if err := e.Send(context.Background(), &Notification{Type: NotifyError, UserEmail: "to@example.com", Message: "m"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(gotTo) != 1 || gotTo[0] != "to@example.com" {
		t.Errorf("recipient = %v, want [to@example.com]", gotTo)
	}
	if !strings.Contains(string(gotMsg), "Subject: [AgentTier] Sandbox error") {
		t.Errorf("missing subject in %s", gotMsg)
	}

	// No recipient → error, no send attempt.
	if err := e.Send(context.Background(), &Notification{Type: NotifyError, Message: "m"}); err == nil {
		t.Error("expected an error when UserEmail is empty")
	}
}

func TestEmailChannel_AuthOnlyWhenCredentialsGiven(t *testing.T) {
	if NewEmailChannel("h", 25, "", "", "f@x").auth != nil {
		t.Error("expected nil auth when no username is configured")
	}
	if NewEmailChannel("h", 587, "u", "p", "f@x").auth == nil {
		t.Error("expected PlainAuth when a username is configured")
	}
}
