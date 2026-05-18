/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useRef, useState } from 'react';
import {
  fetchTemplates,
  fetchWarmPoolStatus,
  setWarmPoolPools,
  type PoolConfig,
  type PoolStatus,
} from '../api/client';
import type { Template } from '../types';

// WarmPoolEditor lets operators run multiple per-template warm pools
// independently. Add a row -> picks a template + count. Remove a row ->
// next save deletes that pool from the cluster (the controller's
// reconciler scales the orphan down on the next reconcile).
//
// The page already renders a per-pool status panel above this editor
// so the operator can see counts (ready / pending / target) per
// template alongside the form. We keep the editor a controlled-form
// component — typing in any input doesn't fire a save until the user
// clicks Save.

const POLL_INTERVAL_MS = 5000;

interface Row {
  // Stable client-side key for React, since templates can be renamed.
  // Generated once on row create; never persisted.
  id: string;
  template: string;
  desiredCount: number;
}

function uid(): string {
  return Math.random().toString(36).slice(2, 10);
}

export default function WarmPoolEditor() {
  const [rows, setRows] = useState<Row[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [statuses, setStatuses] = useState<PoolStatus[] | null>(null);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  // Tracks whether the rows have been initialised from the server so
  // the polling refresh doesn't clobber unsaved edits. Once the user
  // adds, removes, or changes a row we stop seeding rows from polls.
  const initialisedRef = useRef(false);

  useEffect(() => {
    fetchTemplates().then(setTemplates).catch(() => {});

    let cancelled = false;
    const refresh = async () => {
      try {
        const s = await fetchWarmPoolStatus();
        if (cancelled) return;
        const pools: PoolStatus[] = s.pools && s.pools.length > 0
          ? s.pools
          : s.template
            // Legacy single-template cluster — promote into a one-entry array
            // so the rest of the UI works the same way.
            ? [{ template: s.template, desiredCount: s.desiredCount, readyCount: s.readyCount, pendingCount: s.pendingCount }]
            : [];
        setStatuses(pools);
        if (!initialisedRef.current) {
          setRows(pools.map((p) => ({ id: uid(), template: p.template, desiredCount: p.desiredCount })));
          initialisedRef.current = true;
        }
      } catch {
        // Polling is best-effort; UI keeps last-known values.
      }
    };
    refresh();
    const interval = setInterval(refresh, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, []);

  const addRow = () => {
    // Pick the first template that isn't already in a row, or any
    // template if everything's already used. Better than forcing the
    // operator to type a name from memory.
    const used = new Set(rows.map((r) => r.template));
    const available = templates.find((t) => !used.has(t.name));
    setRows((prev) => [...prev, { id: uid(), template: available?.name || (templates[0]?.name ?? ''), desiredCount: 1 }]);
  };

  const removeRow = (id: string) => {
    setRows((prev) => prev.filter((r) => r.id !== id));
  };

  const updateRow = (id: string, patch: Partial<Row>) => {
    setRows((prev) => prev.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  };

  const handleSave = async () => {
    // De-duplicate by template name (last write wins) and drop empty
    // rows. Backend rejects desiredCount > 10 so we don't bother
    // pre-validating beyond the input min/max.
    const seen = new Map<string, PoolConfig>();
    for (const r of rows) {
      if (!r.template) continue;
      seen.set(r.template, { template: r.template, desiredCount: r.desiredCount });
    }
    const pools = Array.from(seen.values());

    setSaving(true);
    setMessage(null);
    try {
      await setWarmPoolPools(pools);
      setMessage('Saved. Pools converge within ~10 seconds.');
      setTimeout(async () => {
        const s = await fetchWarmPoolStatus();
        const next: PoolStatus[] = s.pools && s.pools.length > 0
          ? s.pools
          : s.template
            ? [{ template: s.template, desiredCount: s.desiredCount, readyCount: s.readyCount, pendingCount: s.pendingCount }]
            : [];
        setStatuses(next);
      }, 3000);
    } catch (e: any) {
      setMessage('Error: ' + (e.message || 'Failed to save'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div data-testid="warm-pool-editor">
      <p style={{ fontSize: '13px', color: '#6b6375', marginTop: 0, marginBottom: '16px' }}>
        Pre-create idle sandbox pods so new sandboxes start instantly (&lt;2s) instead of waiting for provisioning (~10s).
        Run multiple pools side-by-side — one per template. Removing a row scales that pool to zero on the next save.
      </p>

      {/* Live status panel (per-pool counts). Shown above the editor so
          changes the user is about to save are easy to compare against
          what's currently provisioned. */}
      {statuses && statuses.length > 0 && (
        <div data-testid="warm-pool-status" style={{ padding: '12px 16px', background: '#f8f8fa', borderRadius: '8px', marginBottom: '16px', fontSize: '13px' }}>
          <strong>Current status:</strong>
          <ul style={{ margin: '8px 0 0', paddingLeft: '18px' }}>
            {statuses.map((p) => (
              <li key={p.template} data-testid={`pool-status-${p.template}`}>
                {p.template}: {p.readyCount} ready, {p.pendingCount} pending (target {p.desiredCount})
              </li>
            ))}
          </ul>
        </div>
      )}
      {statuses && statuses.length === 0 && (
        <div data-testid="warm-pool-empty" style={{ padding: '12px 16px', background: '#f8f8fa', borderRadius: '8px', marginBottom: '16px', fontSize: '13px', color: '#6b6375' }}>
          No warm pools configured yet. Add a row below to start.
        </div>
      )}

      {/* Editable rows */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', marginBottom: '12px' }}>
        {rows.map((r) => (
          <div key={r.id} data-testid={`pool-row-${r.id}`} style={{ display: 'flex', gap: '12px', alignItems: 'flex-end' }}>
            <div>
              <label style={{ display: 'block', fontSize: '12px', fontWeight: 500, marginBottom: '4px', color: '#6b6375' }}>
                Pool size
              </label>
              <input
                data-testid="pool-row-count"
                type="number" min={0} max={10} value={r.desiredCount}
                onChange={(e) => updateRow(r.id, { desiredCount: Number(e.target.value) })}
                style={{ width: '70px', padding: '8px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '15px' }}
              />
            </div>
            <div style={{ flex: 1 }}>
              <label style={{ display: 'block', fontSize: '12px', fontWeight: 500, marginBottom: '4px', color: '#6b6375' }}>
                Template
              </label>
              <select
                data-testid="pool-row-template"
                value={r.template}
                onChange={(e) => updateRow(r.id, { template: e.target.value })}
                style={{ width: '100%', padding: '8px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '14px' }}
              >
                {templates.map((t) => (
                  <option key={t.name} value={t.name}>{t.name}</option>
                ))}
              </select>
            </div>
            <button
              data-testid="pool-row-remove"
              type="button"
              onClick={() => removeRow(r.id)}
              title="Remove this pool"
              style={{
                height: '38px', padding: '0 12px', borderRadius: '6px',
                border: '1px solid #fecaca', background: '#fef2f2', color: '#dc2626',
                fontSize: '13px', fontWeight: 500, cursor: 'pointer',
              }}
            >
              Remove
            </button>
          </div>
        ))}
      </div>

      {/* Add row + Save buttons */}
      <div style={{ display: 'flex', gap: '12px', marginTop: '8px' }}>
        <button
          data-testid="pool-add-row"
          type="button"
          onClick={addRow}
          style={{
            padding: '8px 16px', borderRadius: '8px',
            border: '1px solid #d4d4d8', background: '#fff', color: '#08060d',
            fontSize: '13px', fontWeight: 500, cursor: 'pointer',
          }}
        >
          + Add template
        </button>
        <button
          data-testid="pool-save"
          type="button"
          onClick={handleSave}
          disabled={saving}
          style={{
            padding: '8px 20px', borderRadius: '8px', border: 'none',
            background: saving ? '#d1d5db' : '#aa3bff', color: '#fff',
            fontSize: '14px', fontWeight: 600,
            cursor: saving ? 'not-allowed' : 'pointer',
          }}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>

      {message && (
        <div
          data-testid="warm-pool-message"
          style={{
            marginTop: '12px',
            padding: '8px 12px', borderRadius: '6px', fontSize: '13px',
            background: message.startsWith('Error') ? '#fef2f2' : '#f0fdf4',
            color: message.startsWith('Error') ? '#dc2626' : '#16a34a',
            border: `1px solid ${message.startsWith('Error') ? '#fecaca' : '#bbf7d0'}`,
          }}
        >
          {message}
        </div>
      )}
    </div>
  );
}
