/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, useEffect } from 'react';
import { fetchAnalytics } from '../api/client';
import NodeCapacityCard from '../components/NodeCapacityCard';

interface MetricsData {
  total_sandboxes: number;
  status_breakdown: Record<string, number>;
  template_breakdown: Record<string, number>;
  avg_startup_ms: number;
  startup_sample_count: number;
}

export default function Metrics() {
  const [data, setData] = useState<MetricsData | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const load = () => {
      fetchAnalytics()
        .then((d: any) => setData(d))
        .catch(() => {})
        .finally(() => setLoading(false));
    };
    load();
    const interval = setInterval(load, 10000);
    return () => clearInterval(interval);
  }, []);

  if (loading) return <div style={{ padding: '32px', color: '#6b6375' }}>Loading metrics...</div>;
  if (!data) return <div style={{ padding: '32px', color: '#6b6375' }}>Failed to load metrics.</div>;

  const statusColors: Record<string, string> = {
    Running: '#16a34a', Creating: '#ca8a04', Stopped: '#6b7280', Error: '#dc2626',
  };

  return (
    <div style={{ padding: '32px' }}>
      <h1 style={{ fontSize: '22px', fontWeight: 700, color: '#08060d', marginBottom: '24px' }}>Metrics</h1>

      {/* Summary cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: '16px', marginBottom: '32px' }}>
        <MetricCard label="Total Sandboxes" value={data.total_sandboxes} />
        <MetricCard label="Running" value={data.status_breakdown?.Running || 0} color="#16a34a" />
        <MetricCard label="Stopped" value={data.status_breakdown?.Stopped || 0} color="#6b7280" />
        <MetricCard label="Avg Startup" value={data.avg_startup_ms > 0 ? `${data.avg_startup_ms}ms` : '—'} />
      </div>

      {/* Cluster capacity — node fleet allocatable/requests + saturation.
          Admin-only endpoint; the card hides itself for non-admins. */}
      <NodeCapacityCard />

      {/* Status breakdown */}
      <section style={{ marginBottom: '32px' }}>
        <h2 style={{ fontSize: '16px', fontWeight: 600, marginBottom: '12px' }}>By Status</h2>
        <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
          {Object.entries(data.status_breakdown || {}).map(([status, count]) => (
            <div key={status} style={{ padding: '8px 16px', borderRadius: '8px', background: '#f8f8fa', border: '1px solid #e5e4e7' }}>
              <span style={{ color: statusColors[status] || '#08060d', fontWeight: 600 }}>{count}</span>
              <span style={{ color: '#6b6375', marginLeft: '6px', fontSize: '13px' }}>{status}</span>
            </div>
          ))}
        </div>
      </section>

      {/* Template breakdown */}
      <section>
        <h2 style={{ fontSize: '16px', fontWeight: 600, marginBottom: '12px' }}>By Template</h2>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
          {Object.entries(data.template_breakdown || {}).map(([tmpl, count]) => (
            <div key={tmpl} style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 12px', borderRadius: '6px', background: '#fafafa' }}>
              <span style={{ fontSize: '13px', fontWeight: 500 }}>{tmpl}</span>
              <span style={{ fontSize: '13px', color: '#6b6375' }}>{count} sandbox{count !== 1 ? 'es' : ''}</span>
            </div>
          ))}
          {Object.keys(data.template_breakdown || {}).length === 0 && (
            <div style={{ color: '#6b6375', fontSize: '13px' }}>No sandboxes yet.</div>
          )}
        </div>
      </section>
    </div>
  );
}

function MetricCard({ label, value, color }: { label: string; value: string | number; color?: string }) {
  return (
    <div style={{ background: '#fff', border: '1px solid #e5e4e7', borderRadius: '10px', padding: '16px' }}>
      <div style={{ fontSize: '28px', fontWeight: 700, color: color || '#08060d' }}>{value}</div>
      <div style={{ fontSize: '12px', color: '#6b6375', marginTop: '4px' }}>{label}</div>
    </div>
  );
}
