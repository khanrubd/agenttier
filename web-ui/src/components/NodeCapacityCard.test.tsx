/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import NodeCapacityCard from './NodeCapacityCard';
import * as api from '../api/client';
import type { NodeCapacityResponse } from '../api/client';

vi.mock('../api/client', () => ({
  fetchClusterNodes: vi.fn(),
}));

const sampleResponse: NodeCapacityResponse = {
  nodes: [
    {
      name: 'node-a',
      ready: true,
      instanceType: 'm5.xlarge',
      nodeGroup: 'general',
      allocatable: { cpuMillis: 4000, memBytes: 16 * 1024 * 1024 * 1024 },
      requests: { cpuMillis: 2000, memBytes: 8 * 1024 * 1024 * 1024 },
    },
    {
      name: 'node-b',
      ready: false,
      allocatable: { cpuMillis: 4000, memBytes: 16 * 1024 * 1024 * 1024 },
      requests: { cpuMillis: 0, memBytes: 0 },
    },
  ],
  summary: {
    ready: 1,
    total: 2,
    cpuSaturationPct: 25,
    memSaturationPct: 50,
    allocatable: { cpuMillis: 8000, memBytes: 32 * 1024 * 1024 * 1024 },
    requests: { cpuMillis: 2000, memBytes: 8 * 1024 * 1024 * 1024 },
  },
};

describe('NodeCapacityCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders nothing while the initial fetch is pending', () => {
    vi.mocked(api.fetchClusterNodes).mockReturnValue(new Promise(() => {}));
    const { container } = render(<NodeCapacityCard />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing when the endpoint returns a 403 (non-admin)', async () => {
    vi.mocked(api.fetchClusterNodes).mockRejectedValue(new Error('request failed: 403 Forbidden'));
    const { container } = render(<NodeCapacityCard />);
    await waitFor(() => {
      expect(api.fetchClusterNodes).toHaveBeenCalled();
    });
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing when the fetch fails with a non-403 error', async () => {
    vi.mocked(api.fetchClusterNodes).mockRejectedValue(new Error('network down'));
    const { container } = render(<NodeCapacityCard />);
    await waitFor(() => {
      expect(api.fetchClusterNodes).toHaveBeenCalled();
    });
    // Card also stays hidden on non-403 errors since `data` never gets set —
    // there's no separate "error" UI state, matching the current component.
    expect(container).toBeEmptyDOMElement();
  });

  it('renders the summary cards and node group once data loads', async () => {
    vi.mocked(api.fetchClusterNodes).mockResolvedValue(sampleResponse);
    render(<NodeCapacityCard />);

    expect(await screen.findByText('Cluster capacity')).toBeInTheDocument();
    expect(screen.getByText(/node group: general/)).toBeInTheDocument();
    expect(screen.getByText('1 / 2')).toBeInTheDocument();
    expect(screen.getByText('25%')).toBeInTheDocument();
    expect(screen.getByText('50%')).toBeInTheDocument();
    expect(screen.getByText('2 / 8 cores')).toBeInTheDocument();
    expect(screen.getByText('8 / 32 Gi')).toBeInTheDocument();
  });

  it('lists per-node detail rows behind the expander', async () => {
    vi.mocked(api.fetchClusterNodes).mockResolvedValue(sampleResponse);
    render(<NodeCapacityCard />);

    await screen.findByText('Cluster capacity');
    expect(screen.getByText('Per-node detail (2)')).toBeInTheDocument();
    expect(screen.getByText('node-a')).toBeInTheDocument();
    expect(screen.getByText('node-b')).toBeInTheDocument();
    expect(screen.getByText('m5.xlarge')).toBeInTheDocument();
    // node-b has no instanceType — falls back to the em-dash placeholder.
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
    // "Ready" (exact) is the per-row status cell; "Ready nodes" (the summary
    // label) contains the same substring, so match on the exact cell text.
    expect(screen.getByText('Ready', { selector: 'td' })).toBeInTheDocument();
    expect(screen.getByText('NotReady')).toBeInTheDocument();
  });
});
