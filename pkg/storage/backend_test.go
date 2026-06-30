/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package storage

import (
	"context"
	"testing"
	"time"
)

func TestNoopBackend(t *testing.T) {
	b := NewNoopBackend()
	ctx := context.Background()
	if err := b.RecordSandboxEvent(ctx, SandboxEvent{SandboxID: "x"}); err != nil {
		t.Errorf("noop sandbox event: %v", err)
	}
	if err := b.RecordAuditEvent(ctx, AuditEvent{Actor: "a"}); err != nil {
		t.Errorf("noop audit event: %v", err)
	}
	if err := b.RecordCostSnapshot(ctx, CostSnapshot{SandboxID: "x"}); err != nil {
		t.Errorf("noop cost: %v", err)
	}
	got, err := b.ListAuditEvents(ctx, AuditFilter{})
	if err != nil || got != nil {
		t.Errorf("noop list = (%v, %v), want (nil, nil)", got, err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("noop close: %v", err)
	}
}

func TestSQLBackend_SQLite_RoundTrip(t *testing.T) {
	ctx := context.Background()
	b, err := OpenSQLite(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer b.Close()

	if err := b.RecordSandboxEvent(ctx, SandboxEvent{
		SandboxID: "sb-1", SandboxName: "sb-1", Namespace: "agenttier",
		Phase: "Running", Reason: "Started", Message: "pod ready",
	}); err != nil {
		t.Fatalf("record sandbox event: %v", err)
	}
	if err := b.RecordCostSnapshot(ctx, CostSnapshot{
		SandboxID: "sb-1", Namespace: "agenttier", CPUHours: 0.5, MemGBHours: 1.0, EstimatedUSD: 0.02,
	}); err != nil {
		t.Fatalf("record cost: %v", err)
	}

	// Two audit events for different actors, then filter.
	now := time.Now()
	_ = b.RecordAuditEvent(ctx, AuditEvent{Time: now.Add(-2 * time.Hour), Actor: "alice", Action: "create", Resource: "sandbox", SandboxID: "sb-1"})
	_ = b.RecordAuditEvent(ctx, AuditEvent{Time: now.Add(-1 * time.Hour), Actor: "bob", Action: "stop", Resource: "sandbox", SandboxID: "sb-1"})
	_ = b.RecordAuditEvent(ctx, AuditEvent{Time: now, Actor: "alice", Action: "delete", Resource: "sandbox", SandboxID: "sb-2"})

	all, err := b.ListAuditEvents(ctx, AuditFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 audit events, got %d", len(all))
	}
	// Newest first.
	if all[0].Action != "delete" {
		t.Errorf("expected newest-first ordering, got first action %q", all[0].Action)
	}

	byActor, err := b.ListAuditEvents(ctx, AuditFilter{Actor: "alice"})
	if err != nil {
		t.Fatalf("list by actor: %v", err)
	}
	if len(byActor) != 2 {
		t.Errorf("expected 2 events for alice, got %d", len(byActor))
	}

	bySandbox, err := b.ListAuditEvents(ctx, AuditFilter{SandboxID: "sb-1", Limit: 1})
	if err != nil {
		t.Fatalf("list by sandbox+limit: %v", err)
	}
	if len(bySandbox) != 1 {
		t.Errorf("expected limit=1 to return 1 row, got %d", len(bySandbox))
	}
}

func TestSQLBackend_RebindPostgres(t *testing.T) {
	b := &SQLBackend{dialect: DialectPostgres}
	got := b.rebind("INSERT INTO t (a,b) VALUES (?, ?)")
	want := "INSERT INTO t (a,b) VALUES ($1, $2)"
	if got != want {
		t.Errorf("rebind = %q, want %q", got, want)
	}
	// SQLite/MySQL keep ?.
	sb := &SQLBackend{dialect: DialectSQLite}
	if sb.rebind("VALUES (?, ?)") != "VALUES (?, ?)" {
		t.Error("sqlite rebind should leave ? untouched")
	}
}
