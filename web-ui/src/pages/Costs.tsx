/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, useEffect } from 'react';
import { fetchCosts } from '../api/client';

interface CostData {
  running_sandboxes: number;
  stopped_sandboxes: number;
  total_hourly_compute: number;
  total_hourly_storage: number;
  total_estimated_monthly: number;
  per_template: { template: string; hourly_cost: number; count: number }[];
}

export default function Costs() {
  const [data, setData] = useState<CostData | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchCosts()
      .then((d: any) => setData(d))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <div style={{ padding: '32px', color: '#6b6375' }}>Loading cost data...</div>;
  if (!data) return <div style={{ padding: '32px', color: '#6b6375' }}>Failed to load cost data.</div>;

  const fmt = (n: number) => `$${n.toFixed(2)}`;

  return (
    <div style={{ padding: '32px' }}>
      <h1 style={{ fontSize: '22px', fontWeight: 700, color: '#08060d', marginBottom: '24px' }}>Cost Estimator</h1>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: '16px', marginBottom: '32px' }}>
        <CostCard label="Monthly Estimate" value={fmt(data.total_estimated_monthly)} />
        <CostCard label="Hourly Compute" value={fmt(data.total_hourly_compute)} />
        <CostCard label="Running" value={String(data.running_sandboxes)} color="#16a34a" />
        <CostCard label="Stopped" value={String(data.stopped_sandboxes)} color="#6b7280" />
      </div>

      {data.per_template && data.per_template.length > 0 && (
        <section>
          <h2 style={{ fontSize: '16px', fontWeight: 600, marginBottom: '12px' }}>Cost by Template</h2>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
            {data.per_template.map(t => (
              <div key={t.template} style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 12px', borderRadius: '6px', background: '#fafafa' }}>
                <span style={{ fontSize: '13px', fontWeight: 500 }}>{t.template}</span>
                <span style={{ fontSize: '13px', color: '#6b6375' }}>{t.count} running · {fmt(t.hourly_cost)}/hr</span>
              </div>
            ))}
          </div>
        </section>
      )}

      <p style={{ color: '#9ca3af', fontSize: '11px', marginTop: '24px' }}>
        Estimates based on default rates: $0.04/CPU-hr, $0.005/GB-RAM-hr, $0.10/GB-storage-mo. Actual costs depend on your cloud provider pricing.
      </p>
    </div>
  );
}

function CostCard({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ background: '#fff', border: '1px solid #e5e4e7', borderRadius: '10px', padding: '16px' }}>
      <div style={{ fontSize: '28px', fontWeight: 700, color: color || '#08060d' }}>{value}</div>
      <div style={{ fontSize: '12px', color: '#6b6375', marginTop: '4px' }}>{label}</div>
    </div>
  );
}
