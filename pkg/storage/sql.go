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

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	// modernc.org/sqlite is a pure-Go (CGO-free) SQLite driver, so the SQLite
	// backend works on every platform and in tests without a C toolchain.
	_ "modernc.org/sqlite"
)

// Dialect selects the SQL placeholder style and is set from the configured
// driver. SQLite and MySQL use "?"; Postgres uses "$1, $2, …".
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
)

// SQLBackend persists records to any database/sql-supported store. It uses
// only the standard library plus the pure-Go SQLite driver; Postgres/MySQL are
// supported by passing an already-open *sql.DB from the operator's driver.
type SQLBackend struct {
	db      *sql.DB
	dialect Dialect
}

// OpenSQLite opens (creating if needed) a SQLite database at path and returns a
// ready SQLBackend with its schema initialised. Use ":memory:" for tests.
func OpenSQLite(ctx context.Context, path string) (*SQLBackend, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	return NewSQLBackend(ctx, db, DialectSQLite)
}

// NewSQLBackend wraps an already-open *sql.DB (e.g. a Postgres or MySQL
// connection the operator constructed with their driver) and initialises the
// schema. dialect controls placeholder rebinding.
func NewSQLBackend(ctx context.Context, db *sql.DB, dialect Dialect) (*SQLBackend, error) {
	b := &SQLBackend{db: db, dialect: dialect}
	if err := b.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return b, nil
}

// rebind converts "?"-style placeholders to the dialect's style. Postgres
// needs $1,$2,…; SQLite and MySQL keep "?".
func (b *SQLBackend) rebind(query string) string {
	if b.dialect != DialectPostgres {
		return query
	}
	var sb strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(n))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// initSchema creates the tables if they don't exist. Column types are chosen
// to be portable across SQLite/Postgres/MySQL (TEXT timestamps in RFC3339,
// DOUBLE PRECISION rendered as the portable REAL/DOUBLE per dialect).
func (b *SQLBackend) initSchema(ctx context.Context) error {
	realType := "REAL"
	if b.dialect != DialectSQLite {
		realType = "DOUBLE PRECISION"
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sandbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			sandbox_id TEXT,
			sandbox_name TEXT,
			namespace TEXT,
			phase TEXT,
			reason TEXT,
			message TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			actor TEXT,
			action TEXT,
			resource TEXT,
			sandbox_id TEXT,
			details TEXT
		)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS cost_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			sandbox_id TEXT,
			namespace TEXT,
			cpu_hours %s,
			mem_gb_hours %s,
			estimated_usd %s
		)`, realType, realType, realType),
	}
	// Postgres/MySQL don't accept SQLite's AUTOINCREMENT spelling; swap it.
	for i, s := range stmts {
		switch b.dialect {
		case DialectPostgres:
			stmts[i] = strings.Replace(s, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY", 1)
		case DialectMySQL:
			stmts[i] = strings.Replace(s, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGINT AUTO_INCREMENT PRIMARY KEY", 1)
		}
	}
	for _, s := range stmts {
		if _, err := b.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func tsOrNow(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// RecordSandboxEvent inserts a sandbox lifecycle event.
func (b *SQLBackend) RecordSandboxEvent(ctx context.Context, e SandboxEvent) error {
	_, err := b.db.ExecContext(ctx, b.rebind(
		`INSERT INTO sandbox_events (ts, sandbox_id, sandbox_name, namespace, phase, reason, message)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`),
		tsOrNow(e.Time), e.SandboxID, e.SandboxName, e.Namespace, e.Phase, e.Reason, e.Message)
	return err
}

// RecordAuditEvent inserts an audit event.
func (b *SQLBackend) RecordAuditEvent(ctx context.Context, e AuditEvent) error {
	_, err := b.db.ExecContext(ctx, b.rebind(
		`INSERT INTO audit_events (ts, actor, action, resource, sandbox_id, details)
		 VALUES (?, ?, ?, ?, ?, ?)`),
		tsOrNow(e.Time), e.Actor, e.Action, e.Resource, e.SandboxID, e.Details)
	return err
}

// RecordCostSnapshot inserts a cost/usage sample.
func (b *SQLBackend) RecordCostSnapshot(ctx context.Context, s CostSnapshot) error {
	_, err := b.db.ExecContext(ctx, b.rebind(
		`INSERT INTO cost_snapshots (ts, sandbox_id, namespace, cpu_hours, mem_gb_hours, estimated_usd)
		 VALUES (?, ?, ?, ?, ?, ?)`),
		tsOrNow(s.Time), s.SandboxID, s.Namespace, s.CPUHours, s.MemGBHours, s.EstimatedUSD)
	return err
}

// ListAuditEvents returns audit events matching the filter, newest first.
func (b *SQLBackend) ListAuditEvents(ctx context.Context, f AuditFilter) ([]AuditEvent, error) {
	q := `SELECT ts, actor, action, resource, sandbox_id, details FROM audit_events WHERE 1=1`
	var args []interface{}
	if f.Actor != "" {
		q += ` AND actor = ?`
		args = append(args, f.Actor)
	}
	if f.SandboxID != "" {
		q += ` AND sandbox_id = ?`
		args = append(args, f.SandboxID)
	}
	if !f.Since.IsZero() {
		q += ` AND ts >= ?`
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	q += ` ORDER BY ts DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := b.db.QueryContext(ctx, b.rebind(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var ts string
		if err := rows.Scan(&ts, &e.Actor, &e.Action, &e.Resource, &e.SandboxID, &e.Details); err != nil {
			return nil, err
		}
		e.Time, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Close closes the underlying database handle.
func (b *SQLBackend) Close() error { return b.db.Close() }
