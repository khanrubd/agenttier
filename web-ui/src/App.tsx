/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import Templates from './pages/Templates';
import Metrics from './pages/Metrics';
import ActivityLog from './pages/ActivityLog';
import Costs from './pages/Costs';
import Settings from './pages/Settings';
import Terminal from './pages/Terminal';

function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<Dashboard />} />
        <Route path="/templates" element={<Templates />} />
        <Route path="/metrics" element={<Metrics />} />
        <Route path="/activity" element={<ActivityLog />} />
        <Route path="/costs" element={<Costs />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
      <Route path="/sandbox/:id/terminal" element={<Terminal />} />
    </Routes>
  );
}

export default App;
