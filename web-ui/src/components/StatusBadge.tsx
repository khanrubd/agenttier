/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import type { SandboxStatus } from '../types';

const statusConfig: Record<SandboxStatus, { bg: string; color: string; label: string }> = {
  creating: { bg: '#dbeafe', color: '#1d4ed8', label: 'Creating' },
  running: { bg: '#dcfce7', color: '#166534', label: 'Running' },
  stopped: { bg: '#f3f4f6', color: '#6b7280', label: 'Stopped' },
  error: { bg: '#fee2e2', color: '#dc2626', label: 'Error' },
  deleting: { bg: '#fef3c7', color: '#92400e', label: 'Deleting' },
};

export default function StatusBadge({ status }: { status: SandboxStatus }) {
  const config = statusConfig[status] || statusConfig.creating;
  return (
    <span data-testid="status-badge" style={{
      fontSize: '12px', padding: '3px 10px', borderRadius: '12px',
      background: config.bg, color: config.color, fontWeight: 600,
    }}>
      {config.label}
    </span>
  );
}
