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
	"fmt"
	"net/smtp"
	"sort"
	"strings"
	"time"
)

// EmailChannel delivers notifications over SMTP. It's the third channel the
// docs/board promised alongside webhook + Slack. The recipient is the
// notification's UserEmail (the sandbox owner); when that's empty the message
// is dropped with an error so the Notifier logs it.
type EmailChannel struct {
	addr string    // host:port
	host string    // host alone (for PlainAuth realm)
	from string    // envelope + From-header sender
	auth smtp.Auth // nil when no credentials are configured (open relay / local MTA)
	// sendMail is indirected for testing; defaults to smtp.SendMail.
	sendMail func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

// NewEmailChannel builds an SMTP channel. username/password may be empty for
// an unauthenticated relay (e.g. a local Postfix sidecar). host+port is the
// SMTP server; from is the sender address.
func NewEmailChannel(host string, port int, username, password, from string) *EmailChannel {
	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}
	return &EmailChannel{
		addr:     fmt.Sprintf("%s:%d", host, port),
		host:     host,
		from:     from,
		auth:     auth,
		sendMail: smtp.SendMail,
	}
}

func (e *EmailChannel) Name() string { return "email" }

func (e *EmailChannel) Send(_ context.Context, n *Notification) error {
	if n.UserEmail == "" {
		return fmt.Errorf("email channel: notification has no recipient (UserEmail empty)")
	}
	msg := e.formatMessage(n)
	if err := e.sendMail(e.addr, e.auth, e.from, []string{n.UserEmail}, msg); err != nil {
		return fmt.Errorf("smtp send to %s: %w", n.UserEmail, err)
	}
	return nil
}

// formatMessage renders an RFC 5322 message. Pulled out so tests can assert
// the headers/body without a live SMTP server.
func (e *EmailChannel) formatMessage(n *Notification) []byte {
	subject := fmt.Sprintf("[AgentTier] %s", subjectFor(n.Type))
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", e.from)
	fmt.Fprintf(&b, "To: %s\r\n", n.UserEmail)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(n.Message + "\r\n")
	if n.SandboxName != "" {
		fmt.Fprintf(&b, "\r\nSandbox: %s\r\n", n.SandboxName)
	}
	// Deterministic detail ordering so the body is stable + testable.
	if len(n.Details) > 0 {
		keys := make([]string, 0, len(n.Details))
		for k := range n.Details {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\r\n", k, n.Details[k])
		}
	}
	fmt.Fprintf(&b, "\r\nTime: %s\r\n", n.Timestamp.Format(time.RFC3339))
	return []byte(b.String())
}

// subjectFor maps a NotificationType to a human subject fragment.
func subjectFor(t NotificationType) string {
	switch t {
	case NotifyIdleWarning:
		return "Sandbox idle warning"
	case NotifyError:
		return "Sandbox error"
	case NotifyAutoRestart:
		return "Sandbox auto-restarted"
	case NotifySharedChange:
		return "Sandbox shared with you"
	case NotifyGovernanceLimit:
		return "Governance limit reached"
	case NotifyErrorSpike:
		return "Error spike detected"
	default:
		return string(t)
	}
}
