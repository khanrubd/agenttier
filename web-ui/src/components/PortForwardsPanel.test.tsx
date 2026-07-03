/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import PortForwardsPanel from './PortForwardsPanel';
import * as api from '../api/client';
import type { PortForward } from '../api/client';

vi.mock('../api/client', () => ({
  listPorts: vi.fn(),
  forwardPort: vi.fn(),
  removePort: vi.fn(),
  previewProxyUrl: vi.fn((sandboxId: string, port: number) => `/preview/${sandboxId}/${port}`),
}));

const samplePorts: PortForward[] = [
  { port: 8080, protocol: 'http' },
  { port: 3000, protocol: 'http', previewUrl: 'https://public.example.com/3000' },
];

describe('PortForwardsPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders nothing when the sandbox is not running', () => {
    const { container } = render(<PortForwardsPanel sandboxId="sbx-1" running={false} />);
    expect(container).toBeEmptyDOMElement();
    expect(api.listPorts).not.toHaveBeenCalled();
  });

  it('loads and shows the empty state when no ports are forwarded', async () => {
    vi.mocked(api.listPorts).mockResolvedValue([]);
    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);

    expect(await screen.findByText('No ports exposed yet.')).toBeInTheDocument();
    expect(api.listPorts).toHaveBeenCalledWith('sbx-1');
  });

  it('renders a row per forwarded port with preview links', async () => {
    vi.mocked(api.listPorts).mockResolvedValue(samplePorts);
    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);

    expect(await screen.findByText('8080')).toBeInTheDocument();
    expect(screen.getByText('3000')).toBeInTheDocument();
    // Both ports get an in-Router "preview" link; only the one with
    // previewUrl also gets a "public" link.
    expect(screen.getAllByText('preview')).toHaveLength(2);
    expect(screen.getAllByText('public')).toHaveLength(1);
  });

  it('shows a load error when listing ports fails', async () => {
    vi.mocked(api.listPorts).mockRejectedValue(new Error('boom'));
    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);
    expect(await screen.findByText('boom')).toBeInTheDocument();
  });

  it('validates the port range before calling forwardPort', async () => {
    const user = userEvent.setup();
    vi.mocked(api.listPorts).mockResolvedValue([]);
    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);
    await screen.findByText('No ports exposed yet.');

    const input = screen.getByPlaceholderText('port (e.g. 8080)');
    await user.type(input, '99999');
    await user.click(screen.getByText('Forward'));

    expect(await screen.findByText('Port must be between 1 and 65535')).toBeInTheDocument();
    expect(api.forwardPort).not.toHaveBeenCalled();
  });

  it('forwards a valid port and reloads the list', async () => {
    const user = userEvent.setup();
    vi.mocked(api.listPorts)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ port: 8080, protocol: 'http' }]);
    vi.mocked(api.forwardPort).mockResolvedValue({ port: 8080, protocol: 'http' });

    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);
    await screen.findByText('No ports exposed yet.');

    const input = screen.getByPlaceholderText('port (e.g. 8080)');
    await user.type(input, '8080');
    await user.click(screen.getByText('Forward'));

    await waitFor(() => {
      expect(api.forwardPort).toHaveBeenCalledWith('sbx-1', 8080);
    });
    expect(await screen.findByText('8080')).toBeInTheDocument();
  });

  it('shows an error when forwarding fails', async () => {
    const user = userEvent.setup();
    vi.mocked(api.listPorts).mockResolvedValue([]);
    vi.mocked(api.forwardPort).mockRejectedValue(new Error('port already in use'));

    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);
    await screen.findByText('No ports exposed yet.');

    const input = screen.getByPlaceholderText('port (e.g. 8080)');
    await user.type(input, '8080');
    await user.click(screen.getByText('Forward'));

    expect(await screen.findByText('port already in use')).toBeInTheDocument();
  });

  it('removes a port and reloads the list', async () => {
    const user = userEvent.setup();
    vi.mocked(api.listPorts)
      .mockResolvedValueOnce(samplePorts)
      .mockResolvedValueOnce([samplePorts[1]]);
    vi.mocked(api.removePort).mockResolvedValue(undefined);

    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);
    await screen.findByText('8080');

    await user.click(screen.getAllByText('remove')[0]);

    await waitFor(() => {
      expect(api.removePort).toHaveBeenCalledWith('sbx-1', 8080);
    });
  });

  it('disables the Forward button until a port is entered', async () => {
    vi.mocked(api.listPorts).mockResolvedValue([]);
    render(<PortForwardsPanel sandboxId="sbx-1" running={true} />);
    await screen.findByText('No ports exposed yet.');

    expect(screen.getByText('Forward')).toBeDisabled();
  });
});
