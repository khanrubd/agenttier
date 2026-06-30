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

// Package notifications implements notification delivery for AgentTier.
package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// NotificationType defines the type of notification event.
type NotificationType string

const (
	NotifyIdleWarning     NotificationType = "idle_warning"
	NotifyError           NotificationType = "error"
	NotifyAutoRestart     NotificationType = "auto_restart"
	NotifySandboxStopped  NotificationType = "sandbox_stopped"
	NotifySharedChange    NotificationType = "shared_sandbox_changed"
	NotifyGovernanceLimit NotificationType = "governance_limit"
	NotifyErrorSpike      NotificationType = "error_spike"
)

// Notification represents a notification to be delivered.
type Notification struct {
	Type        NotificationType  `json:"type"`
	SandboxID   string            `json:"sandboxId,omitempty"`
	SandboxName string            `json:"sandboxName,omitempty"`
	UserID      string            `json:"userId,omitempty"`
	UserEmail   string            `json:"userEmail,omitempty"`
	Message     string            `json:"message"`
	Timestamp   time.Time         `json:"timestamp"`
	Details     map[string]string `json:"details,omitempty"`
}

// Channel defines a notification delivery channel.
type Channel interface {
	Send(ctx context.Context, notification *Notification) error
	Name() string
}

// Notifier routes notifications to configured channels based on user preferences.
type Notifier struct {
	channels map[string]Channel
	logger   *slog.Logger
}

// NewNotifier creates a new notification router.
func NewNotifier(logger *slog.Logger) *Notifier {
	return &Notifier{
		channels: make(map[string]Channel),
		logger:   logger,
	}
}

// RegisterChannel adds a delivery channel.
func (n *Notifier) RegisterChannel(channel Channel) {
	n.channels[channel.Name()] = channel
}

// Send delivers a notification to the specified channels.
func (n *Notifier) Send(ctx context.Context, notification *Notification, channelNames []string) {
	if notification.Timestamp.IsZero() {
		notification.Timestamp = time.Now()
	}

	for _, name := range channelNames {
		ch, ok := n.channels[name]
		if !ok {
			n.logger.Warn("notification channel not found", "channel", name)
			continue
		}

		go func(channel Channel) {
			if err := channel.Send(ctx, notification); err != nil {
				n.logger.Error("notification delivery failed",
					"channel", channel.Name(),
					"type", notification.Type,
					"error", err,
				)
			}
		}(ch)
	}
}

// --- Webhook Channel ---

// WebhookChannel delivers notifications via HTTP POST.
type WebhookChannel struct {
	url        string
	httpClient *http.Client
}

// NewWebhookChannel creates a webhook notification channel.
func NewWebhookChannel(url string) *WebhookChannel {
	return &WebhookChannel{
		url:        url,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *WebhookChannel) Name() string { return "webhook" }

func (w *WebhookChannel) Send(ctx context.Context, notification *Notification) error {
	body, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook delivery failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// --- Slack Channel ---

// SlackChannel delivers notifications to Slack via incoming webhook.
type SlackChannel struct {
	webhookURL string
	httpClient *http.Client
}

// NewSlackChannel creates a Slack notification channel.
func NewSlackChannel(webhookURL string) *SlackChannel {
	return &SlackChannel{
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *SlackChannel) Name() string { return "slack" }

func (s *SlackChannel) Send(ctx context.Context, notification *Notification) error {
	// Format Slack message
	payload := map[string]interface{}{
		"blocks": []map[string]interface{}{
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*AgentTier — %s*\n%s", notification.Type, notification.Message),
				},
			},
			{
				"type": "context",
				"elements": []map[string]string{
					{"type": "mrkdwn", "text": fmt.Sprintf("Sandbox: `%s` | User: %s | %s",
						notification.SandboxName, notification.UserEmail, notification.Timestamp.Format(time.RFC3339))},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack delivery failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("slack returned %d", resp.StatusCode)
	}
	return nil
}
