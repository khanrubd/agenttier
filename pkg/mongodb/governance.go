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

// PolicyScope defines the scope level of a governance policy.
type PolicyScope string

const (
	PolicyScopeCluster   PolicyScope = "cluster"
	PolicyScopeNamespace PolicyScope = "namespace"
	PolicyScopeUser      PolicyScope = "user"
)

// GovernancePolicy defines resource limits and access controls.
type GovernancePolicy struct {
	Scope     PolicyScope `bson:"scope" json:"scope"`
	Namespace string      `bson:"namespace,omitempty" json:"namespace,omitempty"`
	UserID    string      `bson:"userId,omitempty" json:"userId,omitempty"`
	Limits    PolicyLimits `bson:"limits" json:"limits"`
	RateLimit RateLimitConfig `bson:"rateLimit,omitempty" json:"rateLimit,omitempty"`
	ApprovedRegistries []string `bson:"approvedRegistries,omitempty" json:"approvedRegistries,omitempty"`
	AllowedTemplates   []string `bson:"allowedTemplates,omitempty" json:"allowedTemplates,omitempty"`
	PortForwardingEnabled *bool  `bson:"portForwardingEnabled,omitempty" json:"portForwardingEnabled,omitempty"`
	AllowedPorts []PortRange `bson:"allowedPorts,omitempty" json:"allowedPorts,omitempty"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
	UpdatedBy string    `bson:"updatedBy,omitempty" json:"updatedBy,omitempty"`
}

// PolicyLimits defines resource limits for sandboxes.
type PolicyLimits struct {
	MaxSandboxes            int    `bson:"maxSandboxes,omitempty" json:"maxSandboxes,omitempty"`
	MaxSandboxesPerUser     int    `bson:"maxSandboxesPerUser,omitempty" json:"maxSandboxesPerUser,omitempty"`
	MaxSandboxesPerNamespace int   `bson:"maxSandboxesPerNamespace,omitempty" json:"maxSandboxesPerNamespace,omitempty"`
	MaxCPU                  string `bson:"maxCPU,omitempty" json:"maxCPU,omitempty"`
	MaxMemory               string `bson:"maxMemory,omitempty" json:"maxMemory,omitempty"`
	MaxStorage              string `bson:"maxStorage,omitempty" json:"maxStorage,omitempty"`
	MaxTimeout              string `bson:"maxTimeout,omitempty" json:"maxTimeout,omitempty"`           // e.g., "24h"
	MaxIdleTimeout          string `bson:"maxIdleTimeout,omitempty" json:"maxIdleTimeout,omitempty"`   // e.g., "4h"
	AllowInfiniteTimeout    bool   `bson:"allowInfiniteTimeout,omitempty" json:"allowInfiniteTimeout,omitempty"`
}

// RateLimitConfig defines rate limiting for sandbox creation.
type RateLimitConfig struct {
	BurstRate    int `bson:"burstRate,omitempty" json:"burstRate,omitempty"`       // per minute
	SustainedRate int `bson:"sustainedRate,omitempty" json:"sustainedRate,omitempty"` // per hour
}

// PortRange defines an allowed port range.
type PortRange struct {
	Min int `bson:"min" json:"min"`
	Max int `bson:"max" json:"max"`
}

// GetPolicy retrieves a governance policy by scope, namespace, and user.
func (c *Client) GetPolicy(ctx context.Context, scope PolicyScope, namespace, userID string) (*GovernancePolicy, error) {
	filter := bson.D{
		{Key: "scope", Value: scope},
		{Key: "namespace", Value: namespace},
		{Key: "userId", Value: userID},
	}

	var policy GovernancePolicy
	err := c.Collection(CollGovernancePolicies).FindOne(ctx, filter).Decode(&policy)
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// SetPolicy creates or updates a governance policy (upsert).
func (c *Client) SetPolicy(ctx context.Context, policy *GovernancePolicy) error {
	policy.UpdatedAt = time.Now()

	filter := bson.D{
		{Key: "scope", Value: policy.Scope},
		{Key: "namespace", Value: policy.Namespace},
		{Key: "userId", Value: policy.UserID},
	}

	opts := options.Replace().SetUpsert(true)
	_, err := c.Collection(CollGovernancePolicies).ReplaceOne(ctx, filter, policy, opts)
	return err
}

// ListPolicies returns all governance policies, optionally filtered by namespace.
func (c *Client) ListPolicies(ctx context.Context, namespace string) ([]GovernancePolicy, error) {
	filter := bson.D{}
	if namespace != "" {
		filter = append(filter, bson.E{Key: "namespace", Value: namespace})
	}

	cursor, err := c.Collection(CollGovernancePolicies).Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var policies []GovernancePolicy
	if err := cursor.All(ctx, &policies); err != nil {
		return nil, err
	}
	return policies, nil
}
