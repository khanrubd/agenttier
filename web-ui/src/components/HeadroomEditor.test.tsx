/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import HeadroomEditor from './HeadroomEditor';
import * as api from '../api/client';
import type { HeadroomConfig } from '../api/client';

vi.mock('../api/client', () => ({
  fetchHeadroomConfig: vi.fn(),
  setHeadroomConfig: vi.fn(),
}));

const enabledConfig: HeadroomConfig = {
  enabled: true,
  replicas: 2,
  cpu: '500m',
  memory: '1Gi',
  readyReplicas: 2,
}

describe('HeadroomEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows a loading state before the initial fetch resolves', () => {
    vi.mocked(api.fetchHeadroomConfig).mockReturnValue(new Promise(() => {}));
    render(<HeadroomEditor />);
    expect(screen.getByTestId('headroom-loading')).toBeInTheDocument();
  });

  it('renders a disabled banner when headroom is not enabled', async () => {
    vi.mocked(api.fetchHeadroomConfig).mockResolvedValue({
      enabled: false,
      replicas: 0,
      cpu: '',
      memory: '',
    });
    render(<HeadroomEditor />);
    expect(await screen.findByTestId('headroom-disabled')).toBeInTheDocument();
    expect(screen.getByText(/optional.headroom.enabled/)).toBeInTheDocument();
  });

  it('renders the form pre-populated from the fetched config when enabled', async () => {
    vi.mocked(api.fetchHeadroomConfig).mockResolvedValue(enabledConfig);
    render(<HeadroomEditor />);

    expect(await screen.findByTestId('headroom-editor')).toBeInTheDocument();
    expect(screen.getByTestId('headroom-replicas')).toHaveValue(2);
    expect(screen.getByTestId('headroom-cpu')).toHaveValue('500m');
    expect(screen.getByTestId('headroom-memory')).toHaveValue('1Gi');
    expect(screen.getByTestId('headroom-status')).toHaveTextContent('2 / 2 pause Pods ready');
  });

  it('saves edited values and shows a confirmation message', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchHeadroomConfig).mockResolvedValue(enabledConfig);
    vi.mocked(api.setHeadroomConfig).mockResolvedValue({
      enabled: true,
      replicas: 5,
      cpu: '750m',
      memory: '2Gi',
      readyReplicas: 5,
    });

    render(<HeadroomEditor />);
    await screen.findByTestId('headroom-editor');

    const replicasInput = screen.getByTestId('headroom-replicas');
    await user.clear(replicasInput);
    await user.type(replicasInput, '5');

    const cpuInput = screen.getByTestId('headroom-cpu');
    await user.clear(cpuInput);
    await user.type(cpuInput, '750m');

    await user.click(screen.getByTestId('headroom-save'));

    await waitFor(() => {
      expect(api.setHeadroomConfig).toHaveBeenCalledWith({ replicas: 5, cpu: '750m', memory: '1Gi' });
    });
    expect(await screen.findByTestId('headroom-message')).toHaveTextContent('Saved.');
  });

  it('shows an error message when saving fails', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchHeadroomConfig).mockResolvedValue(enabledConfig);
    vi.mocked(api.setHeadroomConfig).mockRejectedValue(new Error('backend rejected the request'));

    render(<HeadroomEditor />);
    await screen.findByTestId('headroom-editor');

    await user.click(screen.getByTestId('headroom-save'));

    const message = await screen.findByTestId('headroom-message');
    expect(message).toHaveTextContent('Error: backend rejected the request');
  });

  it('shows an error message when the initial fetch fails', async () => {
    vi.mocked(api.fetchHeadroomConfig).mockRejectedValue(new Error('network down'));
    render(<HeadroomEditor />);

    await waitFor(() => {
      expect(screen.getByTestId('headroom-loading')).toBeInTheDocument();
    });
  });
});
