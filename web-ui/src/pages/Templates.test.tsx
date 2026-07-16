/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import Templates from './Templates';
import * as api from '../api/client';
import type { Template } from '../types';

vi.mock('../api/client', () => ({
  fetchTemplates: vi.fn(),
  createTemplate: vi.fn(),
  updateTemplate: vi.fn(),
  deleteTemplate: vi.fn(),
}));

const sampleTemplates: Template[] = [
  {
    name: 'general-coding',
    description: 'General purpose coding sandbox',
    image: 'ghcr.io/agenttier/sandbox-general:latest',
    spec: { description: 'General purpose coding sandbox', image: { repository: 'ghcr.io/agenttier/sandbox-general:latest' } },
  },
  {
    name: 'no-description',
    description: '',
    image: 'ghcr.io/agenttier/sandbox-minimal:latest',
  },
];

describe('Templates', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
  });

  it('lists templates with their descriptions once loaded', async () => {
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
    render(<Templates />);

    expect(await screen.findByText('general-coding')).toBeInTheDocument();
    expect(screen.getByText('General purpose coding sandbox')).toBeInTheDocument();
    expect(screen.getByText('no-description')).toBeInTheDocument();
    expect(screen.getByText('No description')).toBeInTheDocument();
  });

  it('shows an empty-state placeholder before a template is selected', async () => {
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
    render(<Templates />);

    await screen.findByText('general-coding');
    expect(screen.getByText('Select a template to edit, or click + to create a new one.')).toBeInTheDocument();
  });

  it('shows an error message when the initial load fails', async () => {
    vi.mocked(api.fetchTemplates).mockRejectedValue(new Error('network down'));
    render(<Templates />);

    await waitFor(() => {
      expect(api.fetchTemplates).toHaveBeenCalled();
    });
    // The list is simply empty on failure — no crash, no stale list.
    expect(screen.queryByText('general-coding')).not.toBeInTheDocument();
  });

  it('opens the editor with the template YAML when a template is selected', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
    const { container } = render(<Templates />);

    await user.click(await screen.findByText('general-coding'));

    expect(screen.getByRole('heading', { name: 'general-coding' })).toBeInTheDocument();
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea.value).toContain('description: General purpose coding sandbox');
    expect(textarea.value).toContain('repository: ghcr.io/agenttier/sandbox-general:latest');
  });

  it('shows a delete button only when an existing template is selected', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
    render(<Templates />);

    expect(screen.queryByText('Delete')).not.toBeInTheDocument();
    await user.click(await screen.findByText('general-coding'));
    expect(screen.getByText('Delete')).toBeInTheDocument();
  });

  it('opens the create-template form with starter YAML on clicking +', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates).mockResolvedValue([]);
    const { container } = render(<Templates />);

    await waitFor(() => expect(api.fetchTemplates).toHaveBeenCalled());
    await user.click(screen.getByTitle('Create template'));

    expect(screen.getByPlaceholderText('template-name')).toBeInTheDocument();
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea.value).toContain('My custom template');
    // No delete button while creating (nothing to delete yet).
    expect(screen.queryByText('Delete')).not.toBeInTheDocument();
  });

  it('requires a name before creating a new template', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates).mockResolvedValue([]);
    render(<Templates />);

    await waitFor(() => expect(api.fetchTemplates).toHaveBeenCalled());
    await user.click(screen.getByTitle('Create template'));
    await user.click(screen.getByText('Save'));

    expect(await screen.findByText('Template name is required')).toBeInTheDocument();
    expect(api.createTemplate).not.toHaveBeenCalled();
  });

  it('creates a new template and shows a success message', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates).mockResolvedValue([]);
    vi.mocked(api.createTemplate).mockResolvedValue(sampleTemplates[0]);
    render(<Templates />);

    await waitFor(() => expect(api.fetchTemplates).toHaveBeenCalled());
    await user.click(screen.getByTitle('Create template'));
    await user.type(screen.getByPlaceholderText('template-name'), 'my-new-template');
    await user.click(screen.getByText('Save'));

    await waitFor(() => {
      expect(api.createTemplate).toHaveBeenCalledWith('my-new-template', expect.any(Object));
    });
    expect(await screen.findByText('Template "my-new-template" created')).toBeInTheDocument();
  });

  it('updates an existing template and shows a success message', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
    vi.mocked(api.updateTemplate).mockResolvedValue(sampleTemplates[0]);
    render(<Templates />);

    await user.click(await screen.findByText('general-coding'));
    await user.click(screen.getByText('Save'));

    await waitFor(() => {
      expect(api.updateTemplate).toHaveBeenCalledWith('general-coding', expect.any(Object));
    });
    expect(await screen.findByText('Template "general-coding" updated')).toBeInTheDocument();
  });

  it('shows an error message when saving fails', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
    vi.mocked(api.updateTemplate).mockRejectedValue(new Error('server rejected the update'));
    render(<Templates />);

    await user.click(await screen.findByText('general-coding'));
    await user.click(screen.getByText('Save'));

    expect(await screen.findByText('server rejected the update')).toBeInTheDocument();
  });

  it('deletes the selected template after confirmation and returns to the empty state', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchTemplates)
      .mockResolvedValueOnce(sampleTemplates)
      .mockResolvedValueOnce([sampleTemplates[1]]);
    vi.mocked(api.deleteTemplate).mockResolvedValue(undefined);
    render(<Templates />);

    await user.click(await screen.findByText('general-coding'));
    await user.click(screen.getByText('Delete'));

    await waitFor(() => {
      expect(api.deleteTemplate).toHaveBeenCalledWith('general-coding');
    });
    // Deleting clears `selected`, so the editor pane (and any success
    // message it would have shown) unmounts back to the empty-state hint.
    expect(
      await screen.findByText('Select a template to edit, or click + to create a new one.')
    ).toBeInTheDocument();
  });

  it('does not delete when the confirmation dialog is dismissed', async () => {
    const user = userEvent.setup();
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    vi.mocked(api.fetchTemplates).mockResolvedValue(sampleTemplates);
    render(<Templates />);

    await user.click(await screen.findByText('general-coding'));
    await user.click(screen.getByText('Delete'));

    expect(api.deleteTemplate).not.toHaveBeenCalled();
  });
});
