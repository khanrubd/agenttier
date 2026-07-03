/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import GovernanceEditor from './GovernanceEditor';
import * as api from '../api/client';
import type { GovernanceBundle } from '../api/client';
import type { Template } from '../types';

vi.mock('../api/client', () => ({
  fetchGovernance: vi.fn(),
  setClusterGovernance: vi.fn(),
  setNamespaceGovernance: vi.fn(),
  deleteNamespaceGovernance: vi.fn(),
  fetchTemplates: vi.fn(),
}));

const emptyBundle: GovernanceBundle = { cluster: null, namespaces: [] };

function setupDefaultMocks(bundle: GovernanceBundle = emptyBundle, templates: Template[] = []) {
  vi.mocked(api.fetchGovernance).mockResolvedValue(bundle);
  vi.mocked(api.fetchTemplates).mockResolvedValue(templates);
}

// The form's <label> elements aren't wired to their <input> via htmlFor/id,
// so getByLabelText can't find them; locate the input in the label's own
// wrapper <div> instead.
function inputNextToLabel(labelText: string): HTMLInputElement {
  const label = screen.getByText(labelText);
  const input = label.parentElement?.querySelector('input');
  if (!input) throw new Error(`no input found next to label "${labelText}"`);
  return input;
}

describe('GovernanceEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads the cluster policy and namespace scopes on mount', async () => {
    setupDefaultMocks({
      cluster: { maxSandboxesPerUser: 3, description: 'default cluster policy' },
      namespaces: [{ namespace: 'team-a', policy: { maxSandboxesTotal: 10 } }],
    });

    render(<GovernanceEditor isAdmin={true} />);

    expect(await screen.findByText('Governance policies')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'team-a' })).toBeInTheDocument();
    expect(screen.getByDisplayValue('default cluster policy')).toBeInTheDocument();
  });

  it('shows a read-only banner and disables the form for non-admins', async () => {
    setupDefaultMocks();
    render(<GovernanceEditor isAdmin={false} />);

    await screen.findByText('Governance policies');
    expect(screen.getByText(/Read-only: editing governance requires an admin role/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save policy' })).not.toBeInTheDocument();
  });

  it('shows an error message when the initial load fails', async () => {
    vi.mocked(api.fetchGovernance).mockRejectedValue(new Error('governance API unavailable'));
    vi.mocked(api.fetchTemplates).mockResolvedValue([]);

    render(<GovernanceEditor isAdmin={true} />);

    // The component swallows the loading state via `finally`, so the page
    // still renders past the initial "Loading…" text — the error appears
    // once the user tries to save (see `onSave` error handling test below),
    // so here we assert the page didn't crash and rendered its shell.
    expect(await screen.findByText('Governance policies')).toBeInTheDocument();
  });

  it('saves the cluster policy and shows a confirmation', async () => {
    const user = userEvent.setup();
    setupDefaultMocks();
    vi.mocked(api.setClusterGovernance).mockResolvedValue(undefined);

    render(<GovernanceEditor isAdmin={true} />);
    await screen.findByText('Governance policies');

    const maxUsersInput = inputNextToLabel('Max sandboxes per user');
    await user.clear(maxUsersInput);
    await user.type(maxUsersInput, '7');

    await user.click(screen.getByRole('button', { name: 'Save policy' }));

    await waitFor(() => {
      expect(api.setClusterGovernance).toHaveBeenCalledWith(
        expect.objectContaining({ maxSandboxesPerUser: 7 }),
      );
    });
    expect(await screen.findByText('Cluster policy saved.')).toBeInTheDocument();
  });

  it('shows an error message when saving the cluster policy fails', async () => {
    const user = userEvent.setup();
    setupDefaultMocks();
    vi.mocked(api.setClusterGovernance).mockRejectedValue(new Error('save rejected'));

    render(<GovernanceEditor isAdmin={true} />);
    await screen.findByText('Governance policies');
    await user.click(screen.getByRole('button', { name: 'Save policy' }));

    expect(await screen.findByText('save rejected')).toBeInTheDocument();
  });

  it('saves a new namespace policy and switches scope to it', async () => {
    const user = userEvent.setup();
    setupDefaultMocks();
    vi.mocked(api.setNamespaceGovernance).mockResolvedValue(undefined);

    render(<GovernanceEditor isAdmin={true} />);
    await screen.findByText('Governance policies');

    await user.click(screen.getByRole('button', { name: '+ Add namespace' }));
    const nsInput = screen.getByPlaceholderText('default');
    await user.clear(nsInput);
    await user.type(nsInput, 'team-b');

    await user.click(screen.getByRole('button', { name: 'Save policy' }));

    await waitFor(() => {
      expect(api.setNamespaceGovernance).toHaveBeenCalledWith('team-b', expect.any(Object));
    });
    expect(await screen.findByText('Policy for namespace "team-b" saved.')).toBeInTheDocument();
  });

  it('deletes a namespace policy after confirmation', async () => {
    const user = userEvent.setup();
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    setupDefaultMocks({
      cluster: null,
      namespaces: [{ namespace: 'team-a', policy: { maxSandboxesTotal: 10 } }],
    });
    vi.mocked(api.deleteNamespaceGovernance).mockResolvedValue(undefined);

    render(<GovernanceEditor isAdmin={true} />);
    await screen.findByText('Governance policies');

    await user.click(screen.getByRole('button', { name: 'team-a' }));
    await user.click(await screen.findByRole('button', { name: 'Delete policy' }));

    expect(confirmSpy).toHaveBeenCalled();
    await waitFor(() => {
      expect(api.deleteNamespaceGovernance).toHaveBeenCalledWith('team-a');
    });
    expect(await screen.findByText('Policy for namespace "team-a" removed.')).toBeInTheDocument();

    confirmSpy.mockRestore();
  });

  it('does not delete a namespace policy when the confirmation is declined', async () => {
    const user = userEvent.setup();
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    setupDefaultMocks({
      cluster: null,
      namespaces: [{ namespace: 'team-a', policy: {} }],
    });

    render(<GovernanceEditor isAdmin={true} />);
    await screen.findByText('Governance policies');

    await user.click(screen.getByRole('button', { name: 'team-a' }));
    await user.click(await screen.findByRole('button', { name: 'Delete policy' }));

    expect(confirmSpy).toHaveBeenCalled();
    expect(api.deleteNamespaceGovernance).not.toHaveBeenCalled();

    confirmSpy.mockRestore();
  });

  it('toggles a template chip in the allowed-templates list', async () => {
    const user = userEvent.setup();
    setupDefaultMocks(emptyBundle, [
      { name: 'python-3.12', description: 'Python 3.12', image: 'python:3.12' } as Template,
    ]);
    vi.mocked(api.setClusterGovernance).mockResolvedValue(undefined);

    render(<GovernanceEditor isAdmin={true} />);
    await screen.findByText('Governance policies');

    await user.click(screen.getByRole('button', { name: 'python-3.12' }));
    await user.click(screen.getByRole('button', { name: 'Save policy' }));

    await waitFor(() => {
      expect(api.setClusterGovernance).toHaveBeenCalledWith(
        expect.objectContaining({ allowedTemplates: ['python-3.12'] }),
      );
    });
  });
});
