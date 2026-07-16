/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import ActivityLog from './ActivityLog';
import * as api from '../api/client';
import type { ActivityEntry } from '../types';

vi.mock('../api/client', () => ({
  fetchActivity: vi.fn(),
}));

const entries: ActivityEntry[] = [
  {
    timestamp: '2026-01-01T12:00:00Z',
    user_email: 'alice@example.com',
    action: 'sandbox.create',
    sandbox_id: 'sb-1',
    sandbox_name: 'my-sandbox',
    details: '',
  },
  {
    timestamp: '2026-01-02T09:30:00Z',
    user_email: 'bob@example.com',
    action: 'sandbox_stop',
    sandbox_id: 'sb-2',
    sandbox_name: '',
    details: '',
  },
];

describe('ActivityLog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows a loading state before the fetch resolves', () => {
    vi.mocked(api.fetchActivity).mockReturnValue(new Promise(() => {}));
    render(<ActivityLog />);
    expect(screen.getByText('Loading...')).toBeInTheDocument();
  });

  it('shows an empty state when there is no activity', async () => {
    vi.mocked(api.fetchActivity).mockResolvedValue([]);
    render(<ActivityLog />);
    expect(await screen.findByText('No activity yet.')).toBeInTheDocument();
  });

  it('renders each activity entry, formatting the action and preferring sandbox_name', async () => {
    vi.mocked(api.fetchActivity).mockResolvedValue(entries);
    render(<ActivityLog />);

    expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
    expect(screen.getByText('sandbox → create')).toBeInTheDocument();
    expect(screen.getByText('my-sandbox')).toBeInTheDocument();

    // sandbox_name is empty for the second entry, so it falls back to
    // sandbox_id; underscores in the action are rendered as spaces.
    expect(screen.getByText('sandbox stop')).toBeInTheDocument();
    expect(screen.getByText('sb-2')).toBeInTheDocument();
    expect(screen.getByText('bob@example.com')).toBeInTheDocument();
  });

  it('falls back to the empty state when the fetch fails', async () => {
    vi.mocked(api.fetchActivity).mockRejectedValue(new Error('network error'));
    render(<ActivityLog />);

    await waitFor(() => {
      expect(screen.queryByText('Loading...')).not.toBeInTheDocument();
    });
    expect(screen.getByText('No activity yet.')).toBeInTheDocument();
  });
});
