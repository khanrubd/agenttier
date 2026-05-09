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

package mongodb

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// EventType defines the type of audit event.
type EventType string

const (
	EventSandboxCreated     EventType = "sandbox.created"
	EventSandboxStopped     EventType = "sandbox.stopped"
	EventSandboxResumed     EventType = "sandbox.resumed"
	EventSandboxDeleted     EventType = "sandbox.deleted"
	EventSandboxError       EventType = "sandbox.error"
	EventSandboxIdleTimeout EventType = "sandbox.idle_timeout"
	EventSandboxMaxRuntime  EventType = "sandbox.max_runtime"
	EventTerminalOpened     EventType = "terminal.opened"
	EventTerminalClosed     EventType = "terminal.closed"
	EventCredentialInjected EventType = "credential.injected"
	EventSandboxShared      EventType = "sandbox.shared"
	EventSandboxCloned      EventType = "sandbox.cloned"
	EventPortForwarded      EventType = "port.forwarded"
	EventSandboxRenamed     EventType = "sandbox.renamed"
)

// AuditEvent represents an immutable audit log entry.
type AuditEvent struct {
	Timestamp   time.Time         `bson:"timestamp" json:"timestamp"`
	EventType   EventType         `bson:"eventType" json:"eventType"`
	UserID      string            `bson:"userId" json:"userId"`
	UserEmail   string            `bson:"userEmail" json:"userEmail"`
	SandboxID   string            `bson:"sandboxId" json:"sandboxId"`
	SandboxName string            `bson:"sandboxName" json:"sandboxName"`
	Namespace   string            `bson:"namespace" json:"namespace"`
	TemplateRef string            `bson:"templateRef,omitempty" json:"templateRef,omitempty"`
	Details     map[string]string `bson:"details,omitempty" json:"details,omitempty"`
	TraceID     string            `bson:"traceId,omitempty" json:"traceId,omitempty"`
}

// AuditEventFilter defines query parameters for listing audit events.
type AuditEventFilter struct {
	EventType string
	UserID    string
	SandboxID string
	Namespace string
	From      *time.Time
	To        *time.Time
	Limit     int64
	Offset    int64
}

// RecordEvent inserts an immutable audit event into the audit_events collection.
func (c *Client) RecordEvent(ctx context.Context, event *AuditEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	_, err := c.Collection(CollAuditEvents).InsertOne(ctx, event)
	if err != nil {
		c.logger.Error("failed to record audit event", "eventType", event.EventType, "error", err)
		return err
	}

	c.logger.Info("audit event recorded",
		"eventType", event.EventType,
		"userId", event.UserID,
		"sandboxId", event.SandboxID,
	)
	return nil
}

// ListAuditEvents queries audit events with filtering and pagination.
func (c *Client) ListAuditEvents(ctx context.Context, filter *AuditEventFilter) ([]AuditEvent, int64, error) {
	coll := c.Collection(CollAuditEvents)

	// Build filter
	query := bson.D{}
	if filter.EventType != "" {
		query = append(query, bson.E{Key: "eventType", Value: filter.EventType})
	}
	if filter.UserID != "" {
		query = append(query, bson.E{Key: "userId", Value: filter.UserID})
	}
	if filter.SandboxID != "" {
		query = append(query, bson.E{Key: "sandboxId", Value: filter.SandboxID})
	}
	if filter.Namespace != "" {
		query = append(query, bson.E{Key: "namespace", Value: filter.Namespace})
	}
	if filter.From != nil || filter.To != nil {
		timeFilter := bson.D{}
		if filter.From != nil {
			timeFilter = append(timeFilter, bson.E{Key: "$gte", Value: *filter.From})
		}
		if filter.To != nil {
			timeFilter = append(timeFilter, bson.E{Key: "$lte", Value: *filter.To})
		}
		query = append(query, bson.E{Key: "timestamp", Value: timeFilter})
	}

	// Count total
	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	// Query with pagination
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}}).
		SetLimit(limit).
		SetSkip(filter.Offset)

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var events []AuditEvent
	if err := cursor.All(ctx, &events); err != nil {
		return nil, 0, err
	}

	return events, total, nil
}
