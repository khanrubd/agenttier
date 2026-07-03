/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import WarmPoolEditor from './WarmPoolEditor';
import * as api from '../api/client';
import type { Template } from '../types';
import type { WarmPoolStatus } from '../api/client';

vi.mock('../api/client', () => ({
  fetchTemplates: vi.fn(),
  fetchWarmPoolStatus: vi.fn(),
  setWarmPoolPools: vi.fn(),
}));

const sampleTemplates: Template[] = [
  { name: 'general', description: 'General coding', image: 'agenttier/sandbox-general' },
  { name: 'claude-code', description: 'Claude Code', image: 'agenttier/sandbox-claude-code' },
];

const emptyStatus: WarmPoolStatus = {
  pools: [],
  desiredCount: 0,
  readyCount: 0,
  pendingCount: 0,
  template: '',
};

const onePoolStatus: WarmPoolStatus = {
  pools: [{ template: 'general', desiredCount: 2, readyCount: 1, pendingCount: 1 }],
  desiredCount: 2,
  readyCount: 1,
  pendingCount: 1,
  template: 'general',
};

describe('WarmPoolEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
  });

  it('shows the empty state when no pools are configured', async () => {
    vi.mocked(api.fetchWarmPoolStatus).mockResolvedValue(emptyStatus);
    render(<WarmPoolEditor />);

    expect(await screen.findByTestId('warm-pool-empty')).toBeInTheDocument();
    expect(screen.queryByTestId('warm-pool-status')).not.toBeInTheDocument();
  });

  it('renders the status panel and a pre-populated row for each configured pool', async () => {
    vi.mocked(api.fetchWarmPoolStatus).mockResolvedValue(onePoolStatus);
    render(<WarmPoolEditor />);

    expect(await screen.findByTestId('warm-pool-status')).toBeInTheDocument();
    expect(screen.getByTestId('pool-status-general')).toHaveTextContent('general: 1 ready, 1 pending (target 2)');

    expect(screen.getByTestId('pool-row-template')).toHaveValue('general');
    expect(screen.getByTestId('pool-row-count')).toHaveValue(2);
  });

  it('adds a new row defaulting to the first unused template', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchWarmPoolStatus).mockResolvedValue(emptyStatus);
    render(<WarmPoolEditor />);

    await screen.findByTestId('warm-pool-empty');
    await waitFor(() => expect(api.fetchTemplates).toHaveBeenCalled());

    await user.click(screen.getByTestId('pool-add-row'));

    expect(screen.getByTestId('pool-row-template')).toHaveValue('general');
    expect(screen.getByTestId('pool-row-count')).toHaveValue(1);
  });

  it('removes a row when its remove button is clicked', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchWarmPoolStatus).mockResolvedValue(onePoolStatus);
    render(<WarmPoolEditor />);

    await screen.findByTestId('warm-pool-status');
    expect(screen.getByTestId('pool-row-template')).toBeInTheDocument();

    await user.click(screen.getByTestId('pool-row-remove'));

    expect(screen.queryByTestId('pool-row-template')).not.toBeInTheDocument();
  });

  it('saves the de-duplicated pool list and shows a confirmation message', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchWarmPoolStatus).mockResolvedValue(emptyStatus);
    vi.mocked(api.setWarmPoolPools).mockResolvedValue(undefined);
    render(<WarmPoolEditor />);

    await screen.findByTestId('warm-pool-empty');
    await waitFor(() => expect(api.fetchTemplates).toHaveBeenCalled());

    await user.click(screen.getByTestId('pool-add-row'));
    await user.click(screen.getByTestId('pool-save'));

    await waitFor(() => {
      expect(api.setWarmPoolPools).toHaveBeenCalledWith([{ template: 'general', desiredCount: 1 }]);
    });
    expect(await screen.findByTestId('warm-pool-message')).toHaveTextContent('Saved.');
  });

  it('shows an error message when saving fails', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchWarmPoolStatus).mockResolvedValue(onePoolStatus);
    vi.mocked(api.setWarmPoolPools).mockRejectedValue(new Error('backend rejected the request'));
    render(<WarmPoolEditor />);

    await screen.findByTestId('warm-pool-status');
    await user.click(screen.getByTestId('pool-save'));

    const message = await screen.findByTestId('warm-pool-message');
    expect(message).toHaveTextContent('Error: backend rejected the request');
  });
});
