/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { Sandbox } from '../types';
import StatusBadge from './StatusBadge';

// SandboxCard is the top-level entry on the Dashboard for one sandbox.
// Layout (top to bottom):
//
//   ┌────────────────────────────────────────────────┐
//   │ name  [status]  [mode]                  ⚙️     │  ← title row + gear
//   │ error message (only on status === 'error')     │
//   │ 👤 created_by_email                            │
//   │ 📦 template (plain, above date)                │
//   │ 📅 Created: …                                  │
//   │ 🕐 Last accessed: …                            │
//   │                                                │
//   │ [Open Terminal] [Stop|Resume] [Delete]         │  ← actions
//   └────────────────────────────────────────────────┘
//
// The previous card had an inline "Advanced — ports, files, agent"
// expandable panel below the action buttons. That panel has been moved
// to a dedicated full-page settings route at /sandbox/:id/settings,
// reachable from the gear icon. The motivation: per-sandbox settings
// are growing (governance, network, env vars, mode-specific knobs) and
// stuffing all of them into a card-internal accordion was pushing the
// list view into ergonomic territory better served by a dedicated page.

interface SandboxCardProps {
  sandbox: Sandbox;
  busy?: boolean;
  onStop: (id: string) => void;
  onResume: (id: string) => void;
  onDelete: (id: string) => void;
}

function formatDate(iso: string | null): string {
  if (!iso) return '—';
  return new Date(iso).toLocaleString();
}

export default function SandboxCard({ sandbox, busy = false, onStop, onResume, onDelete }: SandboxCardProps) {
  const navigate = useNavigate();
  const [hovered, setHovered] = useState(false);
  const { id, name, status, mode, error_message, template, created_at, last_accessed_at, created_by_email } = sandbox;

  const isTransitional = status === 'creating' || status === 'deleting' || busy;
  const canOpenTerminal = status === 'running';
  const canStop = status === 'running';
  const showResume = status === 'stopped';
  const canDelete = status === 'running' || status === 'stopped' || status === 'error';

  return (
    <div
      data-testid="sandbox-card"
      data-sandbox-id={id}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        background: hovered ? '#faf9fc' : '#fff',
        border: hovered ? '1px solid #d4d0e0' : '1px solid #e8e6ed',
        borderRadius: '12px',
        padding: '20px',
        boxShadow: hovered ? '0 4px 12px rgba(170,59,255,0.08)' : '0 1px 3px rgba(0,0,0,0.04)',
        position: 'relative',
        transition: 'all 0.2s ease',
      }}
    >
      {/* Spinner sits in the same top-right area as the gear, but is only
          visible while transitional. Both are absolute-positioned so they
          can't fight for layout space; while transitional we hide the
          gear so the spinner is the unambiguous visual. */}
      {isTransitional && (
        <div data-testid="loading-spinner" style={{
          position: 'absolute', top: 16, right: 16, width: 20, height: 20,
          border: '2px solid #e5e4e7', borderTopColor: '#aa3bff', borderRadius: '50%',
          animation: 'spin 0.8s linear infinite',
        }} />
      )}

      {/* Gear icon: opens the per-sandbox settings page in a new tab.
          New tab on purpose — operators usually keep the dashboard open
          while inspecting a single sandbox's settings. The path
          `/sandbox/:id/settings` is wired in App.tsx as a sibling of
          the full-screen Terminal route. */}
      {!isTransitional && (
        <button
          data-testid="btn-settings"
          aria-label="Sandbox settings"
          onClick={(e) => {
            e.stopPropagation();
            window.open(`/sandbox/${id}/settings`, '_blank', 'noopener');
          }}
          style={{
            position: 'absolute',
            top: 14,
            right: 14,
            width: 28,
            height: 28,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: 'transparent',
            border: '1px solid transparent',
            borderRadius: '6px',
            color: '#6b6375',
            fontSize: '16px',
            cursor: 'pointer',
            transition: 'all 0.15s',
          }}
          onMouseEnter={(e) => {
            (e.currentTarget as HTMLButtonElement).style.background = '#f3f0fa';
            (e.currentTarget as HTMLButtonElement).style.borderColor = '#e5e4e7';
            (e.currentTarget as HTMLButtonElement).style.color = '#aa3bff';
          }}
          onMouseLeave={(e) => {
            (e.currentTarget as HTMLButtonElement).style.background = 'transparent';
            (e.currentTarget as HTMLButtonElement).style.borderColor = 'transparent';
            (e.currentTarget as HTMLButtonElement).style.color = '#6b6375';
          }}
          title="Open sandbox settings"
        >
          ⚙
        </button>
      )}

      {/* Title row: name, status badge, mode badge.
          Mode badge sits next to status on purpose — operators care
          most about "is it up?" and "what kind?" at a glance. The
          previous template chip moves out of this row to the metadata
          block below the error so the row stays compact. */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: '8px',
        marginBottom: '14px', flexWrap: 'wrap',
        // Reserve space on the right so the gear / spinner doesn't
        // overlap a long sandbox name.
        paddingRight: '36px',
      }}>
        <h3 data-testid="sandbox-name" style={{ margin: 0, fontSize: '17px', fontWeight: 600, color: '#08060d' }}>{name}</h3>
        <StatusBadge status={status} />
        <ModeBadge mode={mode} />
      </div>

      {/* Error message */}
      {status === 'error' && error_message && (
        <p data-testid="error-message" style={{ color: '#ef4444', fontSize: '13px', margin: '0 0 10px', lineHeight: 1.4 }}>
          {error_message}
        </p>
      )}

      {/* Metadata block. Template now sits as a plain line above the
          Created date in the same color/weight as the date — the prior
          highlighted purple chip in the title row drew attention away
          from the status + mode badges, which are the scan-target. */}
      <div style={{ fontSize: '13px', color: '#6b6375', marginBottom: '16px', display: 'flex', flexDirection: 'column', gap: '4px' }}>
        {created_by_email && <div>👤 {created_by_email}</div>}
        {template && <div data-testid="template">📦 Template: {template}</div>}
        <div data-testid="created-at">📅 Created: {formatDate(created_at)}</div>
        <div data-testid="last-accessed">🕐 Last accessed: {formatDate(last_accessed_at)}</div>
      </div>

      {/* Action buttons */}
      <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
        <button data-testid="btn-open-terminal" disabled={!canOpenTerminal}
          onClick={() => navigate(`/sandbox/${id}/terminal`)} style={btnStyle(canOpenTerminal, '#aa3bff')}>
          Open Terminal
        </button>
        {!showResume && (
          <button data-testid="btn-stop" disabled={!canStop}
            onClick={() => onStop(id)} style={btnStyle(canStop, '#eab308')}>Stop</button>
        )}
        {showResume && (
          <button data-testid="btn-resume"
            onClick={() => onResume(id)} style={btnStyle(true, '#22c55e')}>Resume</button>
        )}
        <button data-testid="btn-delete" disabled={!canDelete}
          onClick={() => onDelete(id)} style={btnStyle(canDelete, '#ef4444')}>Delete</button>
      </div>
    </div>
  );
}

// ModeBadge renders a compact "Code" or "Agent" pill next to the status
// badge. Using purple-tinted "Code" and a different green-tinted "Agent"
// so the two states are distinguishable at a glance even before reading
// the text. Lighter weight than StatusBadge on purpose — mode is
// orthogonal to lifecycle, not a phase.
function ModeBadge({ mode }: { mode: 'code' | 'agent' }) {
  const isAgent = mode === 'agent';
  const label = isAgent ? 'Agent' : 'Code';
  // Agent uses a teal-green palette; Code uses neutral purple-tinted.
  // Both stay visually subordinate to the StatusBadge.
  const bg = isAgent ? '#dcfce7' : '#eef2ff';
  const fg = isAgent ? '#15803d' : '#4338ca';
  return (
    <span
      data-testid="sandbox-mode"
      data-mode={mode}
      style={{
        fontSize: '11px',
        fontWeight: 600,
        padding: '3px 9px',
        borderRadius: '999px',
        background: bg,
        color: fg,
        textTransform: 'uppercase',
        letterSpacing: '0.04em',
      }}
    >
      {label}
    </span>
  );
}

function btnStyle(enabled: boolean, color: string): React.CSSProperties {
  return {
    padding: '6px 14px', borderRadius: '6px',
    border: `1px solid ${enabled ? color + '60' : '#d1d5db'}`,
    background: enabled ? color + '14' : '#f3f4f6',
    color: enabled ? color : '#9ca3af',
    fontSize: '13px', fontWeight: 500,
    cursor: enabled ? 'pointer' : 'not-allowed',
    opacity: enabled ? 1 : 0.6, transition: 'all 0.15s',
  };
}
