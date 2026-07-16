/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import StatusBadge from './StatusBadge';
import type { SandboxStatus } from '../types';

describe('StatusBadge', () => {
  const cases: { status: SandboxStatus; label: string }[] = [
    { status: 'creating', label: 'Creating' },
    { status: 'running', label: 'Running' },
    { status: 'stopped', label: 'Stopped' },
    { status: 'error', label: 'Error' },
    { status: 'deleting', label: 'Deleting' },
  ];

  it.each(cases)('renders the $label label for status "$status"', ({ status, label }) => {
    render(<StatusBadge status={status} />);
    expect(screen.getByTestId('status-badge')).toHaveTextContent(label);
  });

  it('falls back to the "creating" style for an unrecognized status', () => {
    render(<StatusBadge status={'bogus' as SandboxStatus} />);
    expect(screen.getByTestId('status-badge')).toHaveTextContent('Creating');
  });
});
