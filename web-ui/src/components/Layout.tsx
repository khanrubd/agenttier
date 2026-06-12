/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { NavLink, Outlet } from 'react-router-dom';
import { useState, useEffect } from 'react';
import { fetchWarmPoolStatus, type WarmPoolStatus, fetchClusterStatus, type ClusterStatus } from '../api/client';

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
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null);

  useEffect(() => {
    const fetchAll = () => {
      fetchWarmPoolStatus().then(setPoolStatus).catch(() => {});
      fetchClusterStatus().then(setClusterStatus).catch(() => {});
    };
    fetchAll();
    const interval = setInterval(fetchAll, 10000);
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

        {/* Cluster status — node + pod headcount.
            Always visible (even on a fixed-size cluster) so operators
            can see at a glance how busy the cluster is and whether
            autoscaling is on. The autoscaler dot turns green when the
            chart's optional CAS Deployment is running. */}
        <div style={{ borderTop: '1px solid #e5e4e7', paddingTop: '12px', marginTop: '12px' }}>
          {clusterStatus ? (
            <div style={{ padding: '0 8px', fontSize: '11px', color: '#6b6375' }}>
              <div style={{ fontWeight: 600, marginBottom: '4px', display: 'flex', alignItems: 'center', gap: '6px' }}>
                Cluster
                <span title={clusterStatus.autoscalerEnabled ? 'Cluster Autoscaler running' : 'Fixed-size node group'}
                  style={{
                    width: 7, height: 7, borderRadius: '50%',
                    background: clusterStatus.autoscalerEnabled ? '#22c55e' : '#9ca3af',
                  }} />
              </div>
              <div>{clusterStatus.nodesReady} / {clusterStatus.nodes} nodes ready</div>
              <div>{clusterStatus.sandboxPods} sandboxes, {clusterStatus.pods} pods</div>
              {clusterStatus.headroomReady > 0 && (
                <div>{clusterStatus.headroomReady} headroom spare</div>
              )}
            </div>
          ) : (
            <div style={{ padding: '0 8px', fontSize: '11px', color: '#9ca3af' }}>
              Cluster: …
            </div>
          )}
        </div>

        {/* Warm pool status. The backend returns a per-template `pools`
            array (canonical) plus legacy flat fields that are only
            populated when exactly one pool is configured. Aggregate over
            `pools` so the widget reflects every configured template — the
            legacy scalars alone read as "off" the moment a second pool
            exists. */}
        <div style={{ borderTop: '1px solid #e5e4e7', paddingTop: '12px', marginTop: '12px' }}>
          {(() => {
            const pools = poolStatus?.pools && poolStatus.pools.length > 0
              ? poolStatus.pools
              : poolStatus && poolStatus.desiredCount > 0
                // Legacy single-pool shape — promote the flat fields.
                ? [{
                    template: poolStatus.template,
                    desiredCount: poolStatus.desiredCount,
                    readyCount: poolStatus.readyCount,
                    pendingCount: poolStatus.pendingCount,
                  }]
                : [];
            const target = pools.reduce((sum, p) => sum + (p.desiredCount || 0), 0);
            const ready = pools.reduce((sum, p) => sum + (p.readyCount || 0), 0);
            const pending = pools.reduce((sum, p) => sum + (p.pendingCount || 0), 0);
            return target > 0 ? (
              <div style={{ padding: '0 8px', fontSize: '11px', color: '#6b6375' }}>
                <div style={{ fontWeight: 600, marginBottom: '4px' }}>
                  Warm Pool{pools.length > 1 ? ` (${pools.length} templates)` : ''}
                </div>
                <div>{ready} ready / {target} target</div>
                {pending > 0 && <div>{pending} provisioning…</div>}
              </div>
            ) : (
              <div style={{ padding: '0 8px', fontSize: '11px', color: '#9ca3af' }}>
                Warm pool: off
              </div>
            );
          })()}
        </div>
      </nav>

      {/* Main content */}
      <main style={{ flex: 1, overflow: 'auto' }}>
        <Outlet />
      </main>
    </div>
  );
}
