/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { NavLink, Outlet } from 'react-router-dom';
import { useState, useEffect } from 'react';
import { fetchWarmPoolStatus, type WarmPoolStatus } from '../api/client';

const navItems = [
  { to: '/', label: 'Sandboxes' },
  { to: '/templates', label: 'Templates' },
  { to: '/metrics', label: 'Metrics' },
  { to: '/activity', label: 'Activity Log' },
  { to: '/costs', label: 'Cost Estimator' },
  { to: '/settings', label: 'Settings' },
];

export default function Layout() {
  const [poolStatus, setPoolStatus] = useState<WarmPoolStatus | null>(null);

  useEffect(() => {
    const fetchPool = () => {
      fetchWarmPoolStatus().then(setPoolStatus).catch(() => {});
    };
    fetchPool();
    const interval = setInterval(fetchPool, 10000);
    return () => clearInterval(interval);
  }, []);

  const linkStyle = (isActive: boolean): React.CSSProperties => ({
    display: 'flex', alignItems: 'center', gap: '10px', padding: '10px 16px',
    borderRadius: '8px', textDecoration: 'none', fontSize: '14px', fontWeight: 500,
    color: isActive ? '#aa3bff' : '#6b6375',
    background: isActive ? 'rgba(170,59,255,0.08)' : 'transparent',
    transition: 'all 0.15s',
  });

  return (
    <div style={{ display: 'flex', height: '100vh', overflow: 'hidden' }}>
      {/* Left Nav */}
      <nav style={{
        width: '220px', minWidth: '220px', borderRight: '1px solid #e5e4e7',
        display: 'flex', flexDirection: 'column', padding: '20px 12px',
        background: '#fafafa',
      }}>
        <div style={{ fontSize: '18px', fontWeight: 700, padding: '0 16px 20px', color: '#08060d',
          borderBottom: '1px solid #e5e4e7', marginBottom: '16px' }}>
          AgentTier
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: '4px', flex: 1 }}>
          {navItems.map(({ to, label }) => (
            <NavLink key={to} to={to} end={to === '/'} style={({ isActive }) => linkStyle(isActive)}>
              {label}
            </NavLink>
          ))}
        </div>

        {/* Warm pool status */}
        <div style={{ borderTop: '1px solid #e5e4e7', paddingTop: '12px', marginTop: '12px' }}>
          {poolStatus && poolStatus.desiredCount > 0 ? (
            <div style={{ padding: '0 8px', fontSize: '11px', color: '#6b6375' }}>
              <div style={{ fontWeight: 600, marginBottom: '4px' }}>Warm Pool</div>
              <div>{poolStatus.readyCount} ready / {poolStatus.desiredCount} target</div>
              {poolStatus.pendingCount > 0 && <div>{poolStatus.pendingCount} provisioning…</div>}
            </div>
          ) : (
            <div style={{ padding: '0 8px', fontSize: '11px', color: '#9ca3af' }}>
              Warm pool: off
            </div>
          )}
        </div>
      </nav>

      {/* Main content */}
      <main style={{ flex: 1, overflow: 'auto' }}>
        <Outlet />
      </main>
    </div>
  );
}
