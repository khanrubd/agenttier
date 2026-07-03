/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect } from 'vitest';
import App from './App';

describe('App', () => {
  it('renders the dashboard route without crashing', async () => {
    render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>
    );
    expect(screen.getByText('AgentTier')).toBeInTheDocument();

    // Dashboard's mount effect kicks off an unmocked fetchSandboxes() call
    // that resolves/rejects after this test's synchronous body would
    // otherwise have returned, triggering a React "not wrapped in act"
    // warning. Wait for the resulting state update (loading spinner gone)
    // to settle before the test exits.
    await waitFor(() => {
      expect(screen.queryByTestId('loading-state')).not.toBeInTheDocument();
    });
  });
});
