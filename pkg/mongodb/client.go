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

// Package mongodb implements the persistent datastore for AgentTier.
package mongodb

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	// DatabaseName is the MongoDB database used by AgentTier.
	DatabaseName = "agenttier"

	// Collection names
	CollSandboxes         = "sandboxes"
	CollTemplates         = "templates"
	CollAuditEvents       = "audit_events"
	CollGovernancePolicies = "governance_policies"
	CollSessions          = "sessions"
	CollUserPreferences   = "user_preferences"
	CollCostRecords       = "cost_records"

	// DefaultRetentionDays is the default TTL for audit events and cost records.
	DefaultRetentionDays = 90

	// ConnectTimeout is the maximum time to wait for initial connection.
	ConnectTimeout = 30 * time.Second
)

// Client wraps the MongoDB driver client with AgentTier-specific operations.
type Client struct {
	client *mongo.Client
	db     *mongo.Database
	logger *slog.Logger
}

// Config holds MongoDB connection configuration.
type Config struct {
	URI            string
	Database       string
	RetentionDays  int
	ConnectTimeout time.Duration
}

// NewClient creates a new MongoDB client and verifies connectivity.
func NewClient(ctx context.Context, cfg *Config, logger *slog.Logger) (*Client, error) {
	if cfg.Database == "" {
		cfg.Database = DatabaseName
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = DefaultRetentionDays
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = ConnectTimeout
	}

	opts := options.Client().ApplyURI(cfg.URI)

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	client, err := mongo.Connect(connectCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Verify connectivity
	if err := client.Ping(connectCtx, nil); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	db := client.Database(cfg.Database)
	logger.Info("connected to MongoDB", "database", cfg.Database)

	c := &Client{
		client: client,
		db:     db,
		logger: logger,
	}

	return c, nil
}

// Close disconnects from MongoDB.
func (c *Client) Close(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}

// Collection returns a handle to the named collection.
func (c *Client) Collection(name string) *mongo.Collection {
	return c.db.Collection(name)
}

// HealthCheck verifies MongoDB connectivity.
func (c *Client) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.client.Ping(ctx, nil)
}

// EnsureIndexes creates all required indexes idempotently.
// This is called on controller startup and is safe to run multiple times.
func (c *Client) EnsureIndexes(ctx context.Context, retentionDays int) error {
	c.logger.Info("ensuring MongoDB indexes")

	ttlSeconds := int32(retentionDays * 24 * 60 * 60)

	indexes := map[string][]mongo.IndexModel{
		CollSandboxes: {
			{Keys: bson.D{{Key: "namespace", Value: 1}, {Key: "sandboxId", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "createdBy.sub", Value: 1}, {Key: "namespace", Value: 1}}},
			{Keys: bson.D{{Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "templateRef", Value: 1}}},
			{Keys: bson.D{{Key: "lastActivityAt", Value: 1}}},
			{Keys: bson.D{{Key: "sharing.users.identity", Value: 1}}},
		},
		CollTemplates: {
			{Keys: bson.D{{Key: "name", Value: 1}, {Key: "namespace", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "allowedNamespaces", Value: 1}}},
		},
		CollAuditEvents: {
			{Keys: bson.D{{Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "sandboxId", Value: 1}, {Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "eventType", Value: 1}, {Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "namespace", Value: 1}, {Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "timestamp", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(ttlSeconds)},
		},
		CollGovernancePolicies: {
			{Keys: bson.D{{Key: "scope", Value: 1}, {Key: "namespace", Value: 1}, {Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		CollSessions: {
			{Keys: bson.D{{Key: "sessionId", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "sandboxId", Value: 1}, {Key: "startedAt", Value: -1}}},
			{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "startedAt", Value: -1}}},
			{Keys: bson.D{{Key: "endedAt", Value: 1}}},
		},
		CollUserPreferences: {
			{Keys: bson.D{{Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "apiKeys.keyHash", Value: 1}}},
		},
		CollCostRecords: {
			{Keys: bson.D{{Key: "sandboxId", Value: 1}, {Key: "period", Value: -1}}},
			{Keys: bson.D{{Key: "namespace", Value: 1}, {Key: "period", Value: -1}}},
			{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "period", Value: -1}}},
			{Keys: bson.D{{Key: "period", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(ttlSeconds)},
		},
	}

	for collName, models := range indexes {
		coll := c.db.Collection(collName)
		_, err := coll.Indexes().CreateMany(ctx, models)
		if err != nil {
			return fmt.Errorf("failed to create indexes for %s: %w", collName, err)
		}
		c.logger.Info("indexes ensured", "collection", collName, "count", len(models))
	}

	return nil
}
