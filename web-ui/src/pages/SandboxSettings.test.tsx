/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import SandboxSettings from './SandboxSettings';
import * as api from '../api/client';
import type { Sandbox } from '../types';

vi.mock('../api/client', () => ({
  fetchSandbox: vi.fn(),
}));

// The page composes PortForwardsPanel/FilesPanel/AgentPanel, each with its
// own data fetching already covered by their own test files — stub them out
// here so this page test stays focused on SandboxSettings' own header,
// overview, and loading/error states.
vi.mock('../components/PortForwardsPanel', () => ({
  default: ({ sandboxId, running }: { sandboxId: string; running: boolean }) => (
    <div data-testid="stub-port-forwards">{sandboxId}:{String(running)}</div>
  ),
}));
vi.mock('../components/FilesPanel', () => ({
  default: ({ sandboxId, running }: { sandboxId: string; running: boolean }) => (
    <div data-testid="stub-files">{sandboxId}:{String(running)}</div>
  ),
}));
vi.mock('../components/AgentPanel', () => ({
  default: ({ sandboxId, running }: { sandboxId: string; running: boolean }) => (
    <div data-testid="stub-agent">{sandboxId}:{String(running)}</div>
  ),
}));

const sampleSandbox: Sandbox = {
  id: 'sbx-1',
  name: 'demo-sandbox',
  status: 'running',
  template: 'general-coding',
  mode: 'code',
  namespace: 'default',
  error_message: null,
  created_at: '2026-01-01T00:00:00Z',
  last_accessed_at: '2026-01-02T00:00:00Z',
  created_by: 'user-1',
  created_by_email: 'user@example.com',
};

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/sandbox/:id/settings" element={<SandboxSettings />} />
      </Routes>
    </MemoryRouter>
  );
}

describe('SandboxSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows a loading state before the initial fetch resolves', () => {
    vi.mocked(api.fetchSandbox).mockReturnValue(new Promise(() => {}));
    renderAt('/sandbox/sbx-1/settings');
    expect(screen.getByTestId('sandbox-settings-loading')).toBeInTheDocument();
  });

  it('renders sandbox overview details once loaded', async () => {
    vi.mocked(api.fetchSandbox).mockResolvedValue(sampleSandbox);
    renderAt('/sandbox/sbx-1/settings');

    expect(await screen.findByTestId('sandbox-settings-page')).toBeInTheDocument();
    expect(screen.getByTestId('sandbox-settings-title')).toHaveTextContent('demo-sandbox');
    expect(screen.getByTestId('settings-sandbox-id')).toHaveTextContent('sbx-1');
    expect(screen.getByTestId('settings-template')).toHaveTextContent('general-coding');
    expect(screen.getByTestId('settings-mode')).toHaveTextContent('Code');
    expect(screen.getByTestId('settings-namespace')).toHaveTextContent('default');
    expect(screen.getByTestId('settings-created-by')).toHaveTextContent('user@example.com');
    expect(api.fetchSandbox).toHaveBeenCalledWith('sbx-1');
  });

  it('passes running=true through to child panels when the sandbox is running', async () => {
    vi.mocked(api.fetchSandbox).mockResolvedValue(sampleSandbox);
    renderAt('/sandbox/sbx-1/settings');

    await screen.findByTestId('sandbox-settings-page');
    expect(screen.getByTestId('stub-port-forwards')).toHaveTextContent('sbx-1:true');
    expect(screen.getByTestId('stub-files')).toHaveTextContent('sbx-1:true');
    expect(screen.getByTestId('stub-agent')).toHaveTextContent('sbx-1:true');
  });

  it('passes running=false through to child panels when the sandbox is stopped', async () => {
    vi.mocked(api.fetchSandbox).mockResolvedValue({ ...sampleSandbox, status: 'stopped' });
    renderAt('/sandbox/sbx-1/settings');

    await screen.findByTestId('sandbox-settings-page');
    expect(screen.getByTestId('stub-port-forwards')).toHaveTextContent('sbx-1:false');
  });

  it('renders the Agent mode badge for agent-mode sandboxes', async () => {
    vi.mocked(api.fetchSandbox).mockResolvedValue({ ...sampleSandbox, mode: 'agent' });
    renderAt('/sandbox/sbx-1/settings');

    await screen.findByTestId('sandbox-settings-page');
    expect(screen.getByTestId('sandbox-mode-badge')).toHaveAttribute('data-mode', 'agent');
    expect(screen.getByTestId('settings-mode')).toHaveTextContent('Agent');
  });

  it('falls back to em-dash placeholders for missing optional fields', async () => {
    vi.mocked(api.fetchSandbox).mockResolvedValue({
      ...sampleSandbox,
      template: '',
      created_by_email: '',
      last_accessed_at: null,
    });
    renderAt('/sandbox/sbx-1/settings');

    await screen.findByTestId('sandbox-settings-page');
    expect(screen.getByTestId('settings-template')).toHaveTextContent('—');
    expect(screen.getByTestId('settings-created-by')).toHaveTextContent('—');
    expect(screen.getByTestId('settings-last-accessed')).toHaveTextContent('—');
  });

  it('shows an error state when the fetch fails and no sandbox has loaded yet', async () => {
    vi.mocked(api.fetchSandbox).mockRejectedValue(new Error('not found'));
    renderAt('/sandbox/sbx-1/settings');

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-settings-error')).toBeInTheDocument();
    });
    expect(screen.getByText('not found')).toBeInTheDocument();
  });

  it('has a link back to the dashboard', async () => {
    vi.mocked(api.fetchSandbox).mockResolvedValue(sampleSandbox);
    renderAt('/sandbox/sbx-1/settings');

    await screen.findByTestId('sandbox-settings-page');
    expect(screen.getByTestId('back-to-dashboard')).toHaveAttribute('href', '/');
  });
});
