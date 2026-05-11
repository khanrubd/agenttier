/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { Sandbox } from '../types';
import StatusBadge from './StatusBadge';
import PortForwardsPanel from './PortForwardsPanel';

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
  const { id, name, status, error_message, template, created_at, last_accessed_at, created_by_email } = sandbox;

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
      {isTransitional && (
        <div data-testid="loading-spinner" style={{
          position: 'absolute', top: 12, right: 12, width: 20, height: 20,
          border: '2px solid #e5e4e7', borderTopColor: '#aa3bff', borderRadius: '50%',
          animation: 'spin 0.8s linear infinite',
        }} />
      )}

      {/* Title + badges */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '14px', flexWrap: 'wrap' }}>
        <h3 data-testid="sandbox-name" style={{ margin: 0, fontSize: '17px', fontWeight: 600, color: '#08060d' }}>{name}</h3>
        <StatusBadge status={status} />
        {template && (
          <span style={{ fontSize: '11px', padding: '2px 8px', borderRadius: '4px',
            background: '#ede9fe', color: '#6d28d9', fontWeight: 500 }}>
            📦 {template}
          </span>
        )}
      </div>

      {/* Error message */}
      {status === 'error' && error_message && (
        <p data-testid="error-message" style={{ color: '#ef4444', fontSize: '13px', margin: '0 0 10px', lineHeight: 1.4 }}>
          {error_message}
        </p>
      )}

      {/* Metadata */}
      <div style={{ fontSize: '13px', color: '#6b6375', marginBottom: '16px', display: 'flex', flexDirection: 'column', gap: '4px' }}>
        {created_by_email && <div>👤 {created_by_email}</div>}
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

      <PortForwardsPanel sandboxId={id} running={status === 'running'} />
    </div>
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
