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

// SessionRecord represents a terminal session stored in MongoDB.
type SessionRecord struct {
	SessionID      string     `bson:"sessionId" json:"sessionId"`
	SandboxID      string     `bson:"sandboxId" json:"sandboxId"`
	Namespace      string     `bson:"namespace" json:"namespace"`
	UserID         string     `bson:"userId" json:"userId"`
	UserEmail      string     `bson:"userEmail" json:"userEmail"`
	Type           string     `bson:"type" json:"type"` // "terminal" or "command"
	StartedAt      time.Time  `bson:"startedAt" json:"startedAt"`
	EndedAt        *time.Time `bson:"endedAt,omitempty" json:"endedAt,omitempty"`
	Duration       int        `bson:"duration,omitempty" json:"duration,omitempty"` // seconds
	BytesIn        int64      `bson:"bytesIn,omitempty" json:"bytesIn,omitempty"`
	BytesOut       int64      `bson:"bytesOut,omitempty" json:"bytesOut,omitempty"`
	RouterInstance string     `bson:"routerInstance,omitempty" json:"routerInstance,omitempty"`
	ReconnectCount int        `bson:"reconnectCount,omitempty" json:"reconnectCount,omitempty"`
}

// CreateSession records a new terminal session.
func (c *Client) CreateSession(ctx context.Context, session *SessionRecord) error {
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now()
	}
	_, err := c.Collection(CollSessions).InsertOne(ctx, session)
	return err
}

// EndSession updates a session with end time and stats.
func (c *Client) EndSession(ctx context.Context, sessionID string, bytesIn, bytesOut int64) error {
	now := time.Now()
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "endedAt", Value: now},
			{Key: "bytesIn", Value: bytesIn},
			{Key: "bytesOut", Value: bytesOut},
		}},
	}

	// Calculate duration from startedAt
	var session SessionRecord
	err := c.Collection(CollSessions).FindOne(ctx, bson.D{{Key: "sessionId", Value: sessionID}}).Decode(&session)
	if err == nil {
		duration := int(now.Sub(session.StartedAt).Seconds())
		update = bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "endedAt", Value: now},
				{Key: "duration", Value: duration},
				{Key: "bytesIn", Value: bytesIn},
				{Key: "bytesOut", Value: bytesOut},
			}},
		}
	}

	_, err = c.Collection(CollSessions).UpdateOne(ctx, bson.D{{Key: "sessionId", Value: sessionID}}, update)
	return err
}

// ListSessionsBySandbox returns sessions for a specific sandbox.
func (c *Client) ListSessionsBySandbox(ctx context.Context, sandboxID string, limit int64) ([]SessionRecord, error) {
	if limit == 0 {
		limit = 20
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "startedAt", Value: -1}}).
		SetLimit(limit)

	cursor, err := c.Collection(CollSessions).Find(ctx, bson.D{{Key: "sandboxId", Value: sandboxID}}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var sessions []SessionRecord
	if err := cursor.All(ctx, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// GetActiveSessions returns all sessions that haven't ended yet.
func (c *Client) GetActiveSessions(ctx context.Context) ([]SessionRecord, error) {
	cursor, err := c.Collection(CollSessions).Find(ctx, bson.D{{Key: "endedAt", Value: nil}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var sessions []SessionRecord
	if err := cursor.All(ctx, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}
