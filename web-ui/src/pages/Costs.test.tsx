/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import Costs from './Costs';
import * as api from '../api/client';

vi.mock('../api/client', () => ({
  fetchCosts: vi.fn(),
}));

const costData = {
  running_sandboxes: 3,
  stopped_sandboxes: 1,
  total_hourly_compute: 0.24,
  total_hourly_storage: 0.05,
  total_estimated_monthly: 175.5,
  per_template: [
    { template: 'python-3.12', hourly_cost: 0.12, count: 2 },
    { template: 'node-20', hourly_cost: 0.06, count: 1 },
  ],
};

describe('Costs', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows a loading state before the fetch resolves', () => {
    vi.mocked(api.fetchCosts).mockReturnValue(new Promise(() => {}));
    render(<Costs />);
    expect(screen.getByText('Loading cost data...')).toBeInTheDocument();
  });

  it('shows a failure message when data never arrives', async () => {
    vi.mocked(api.fetchCosts).mockRejectedValue(new Error('cost API down'));
    render(<Costs />);
    expect(await screen.findByText('Failed to load cost data.')).toBeInTheDocument();
  });

  it('renders the summary cards with formatted currency', async () => {
    vi.mocked(api.fetchCosts).mockResolvedValue(costData as any);
    render(<Costs />);

    expect(await screen.findByText('$175.50')).toBeInTheDocument();
    expect(screen.getByText('$0.24')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('Monthly Estimate')).toBeInTheDocument();
    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('Stopped')).toBeInTheDocument();
  });

  it('renders a per-template cost breakdown when present', async () => {
    vi.mocked(api.fetchCosts).mockResolvedValue(costData as any);
    render(<Costs />);

    expect(await screen.findByText('Cost by Template')).toBeInTheDocument();
    expect(screen.getByText('python-3.12')).toBeInTheDocument();
    expect(screen.getByText('2 running · $0.12/hr')).toBeInTheDocument();
    expect(screen.getByText('node-20')).toBeInTheDocument();
    expect(screen.getByText('1 running · $0.06/hr')).toBeInTheDocument();
  });

  it('omits the per-template section when there is no breakdown', async () => {
    vi.mocked(api.fetchCosts).mockResolvedValue({ ...costData, per_template: [] } as any);
    render(<Costs />);

    await waitFor(() => {
      expect(screen.queryByText('Loading cost data...')).not.toBeInTheDocument();
    });
    expect(screen.queryByText('Cost by Template')).not.toBeInTheDocument();
  });
});
