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

// Package storage provides an optional persistence backend for AgentTier's
// historical records — sandbox lifecycle events, audit events, and cost
// snapshots. AgentTier's source of truth stays Kubernetes (etcd + Events +
// ConfigMaps); this backend is an OPT-IN sink for long-term retention and
// reporting that outlives a Sandbox object's lifetime (Kubernetes Events are
// GC'd after ~1h by default).
//
// The default is NoopBackend, so an install with no SQL configured behaves
// exactly as before. A SQLBackend (SQLite / Postgres / MySQL) can be wired in
// via Helm. All recording is best-effort: a backend error is logged by the
// caller and never blocks the operation it describes (graceful degradation).
package storage

import (
	"context"
	"time"
)

// SandboxEvent is a recorded sandbox lifecycle transition.
type SandboxEvent struct {
	Time        time.Time
	SandboxID   string
	SandboxName string
	Namespace   string
	Phase       string
	Reason      string
	Message     string
}

// AuditEvent is a recorded user/operator action.
type AuditEvent struct {
	Time      time.Time
	Actor     string // OIDC sub or API-key id (or a hash thereof)
	Action    string // e.g. "create", "stop", "share", "exec"
	Resource  string // e.g. "sandbox", "template"
	SandboxID string
	Details   string // free-form JSON or text
}

// CostSnapshot is a point-in-time cost/usage sample for a sandbox.
type CostSnapshot struct {
	Time         time.Time
	SandboxID    string
	Namespace    string
	CPUHours     float64
	MemGBHours   float64
	EstimatedUSD float64
}

// AuditFilter narrows ListAuditEvents. Zero values mean "no constraint".
type AuditFilter struct {
	Actor     string
	SandboxID string
	Since     time.Time
	Limit     int
}

// Backend persists AgentTier historical records. Implementations must be safe
// for concurrent use. Record* methods are best-effort sinks; callers log
// errors and continue.
type Backend interface {
	RecordSandboxEvent(ctx context.Context, e SandboxEvent) error
	RecordAuditEvent(ctx context.Context, e AuditEvent) error
	RecordCostSnapshot(ctx context.Context, s CostSnapshot) error
	ListAuditEvents(ctx context.Context, f AuditFilter) ([]AuditEvent, error)
	Close() error
}

// NoopBackend is the default: it persists nothing and returns no rows. It lets
// every call site use a non-nil Backend without nil checks, and is what runs
// when no SQL backend is configured.
type NoopBackend struct{}

// NewNoopBackend returns a Backend that discards all records.
func NewNoopBackend() *NoopBackend { return &NoopBackend{} }

func (*NoopBackend) RecordSandboxEvent(context.Context, SandboxEvent) error { return nil }
func (*NoopBackend) RecordAuditEvent(context.Context, AuditEvent) error     { return nil }
func (*NoopBackend) RecordCostSnapshot(context.Context, CostSnapshot) error { return nil }
func (*NoopBackend) ListAuditEvents(context.Context, AuditFilter) ([]AuditEvent, error) {
	return nil, nil
}
func (*NoopBackend) Close() error { return nil }
