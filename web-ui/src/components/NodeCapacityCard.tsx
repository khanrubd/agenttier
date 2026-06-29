/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, useEffect } from 'react';
import { fetchClusterNodes, type NodeCapacityResponse } from '../api/client';

// Formats millicores as cores (e.g. 1500 -> "1.5", 2000 -> "2").
function cores(milli: number): string {
  const c = milli / 1000;
  return Number.isInteger(c) ? String(c) : c.toFixed(1);
}

// Formats bytes as GiB (e.g. 8589934592 -> "8").
function gib(bytes: number): string {
  const g = bytes / (1024 * 1024 * 1024);
  return Number.isInteger(g) ? String(g) : g.toFixed(1);
}

function satColor(pct: number): string {
  if (pct >= 90) return '#dc2626';
  if (pct >= 70) return '#ca8a04';
  return '#16a34a';
}

// NodeCapacityCard shows the node fleet behind GET /cluster/nodes: a summary
// (ready/total nodes, CPU + memory saturation, node group) plus a per-node
// table behind an expander. Admin-only endpoint, so it quietly hides for
// non-admins (a 403 just leaves the card unrendered).
export default function NodeCapacityCard() {
  const [data, setData] = useState<NodeCapacityResponse | null>(null);
  const [denied, setDenied] = useState(false);

  useEffect(() => {
    const load = () => {
      fetchClusterNodes()
        .then(setData)
        .catch((e: any) => {
          if (String(e?.message || e).includes('403')) setDenied(true);
        });
    };
    load();
    const interval = setInterval(load, 10000);
    return () => clearInterval(interval);
  }, []);

  if (denied || !data) return null;

  const { summary, nodes } = data;
  const nodeGroup = nodes.find((n) => n.nodeGroup)?.nodeGroup;

  return (
    <section style={{ marginBottom: '32px' }}>
      <h2 style={{ fontSize: '16px', fontWeight: 600, marginBottom: '12px' }}>
        Cluster capacity
        {nodeGroup && (
          <span style={{ fontSize: '12px', fontWeight: 400, color: '#6b6375', marginLeft: '8px' }}>
            node group: {nodeGroup}
          </span>
        )}
      </h2>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: '16px', marginBottom: '12px' }}>
        <CapCard label="Ready nodes" value={`${summary.ready} / ${summary.total}`} />
        <CapCard
          label="CPU requested"
          value={`${summary.cpuSaturationPct}%`}
          color={satColor(summary.cpuSaturationPct)}
          sub={`${cores(summary.requests.cpuMillis)} / ${cores(summary.allocatable.cpuMillis)} cores`}
        />
        <CapCard
          label="Memory requested"
          value={`${summary.memSaturationPct}%`}
          color={satColor(summary.memSaturationPct)}
          sub={`${gib(summary.requests.memBytes)} / ${gib(summary.allocatable.memBytes)} Gi`}
        />
      </div>

      <details>
        <summary style={{ cursor: 'pointer', fontSize: '13px', color: '#6b6375' }}>
          Per-node detail ({nodes.length})
        </summary>
        <table style={{ width: '100%', borderCollapse: 'collapse', marginTop: '8px', fontSize: '13px' }}>
          <thead>
            <tr style={{ textAlign: 'left', color: '#6b6375' }}>
              <th style={{ padding: '6px 8px' }}>Node</th>
              <th style={{ padding: '6px 8px' }}>Type</th>
              <th style={{ padding: '6px 8px' }}>Ready</th>
              <th style={{ padding: '6px 8px' }}>CPU (req/alloc)</th>
              <th style={{ padding: '6px 8px' }}>Mem (req/alloc)</th>
            </tr>
          </thead>
          <tbody>
            {nodes.map((n) => (
              <tr key={n.name} style={{ borderTop: '1px solid #eee' }}>
                <td style={{ padding: '6px 8px', fontFamily: 'monospace' }}>{n.name}</td>
                <td style={{ padding: '6px 8px' }}>{n.instanceType || '—'}</td>
                <td style={{ padding: '6px 8px', color: n.ready ? '#16a34a' : '#dc2626' }}>{n.ready ? 'Ready' : 'NotReady'}</td>
                <td style={{ padding: '6px 8px' }}>{cores(n.requests.cpuMillis)} / {cores(n.allocatable.cpuMillis)}</td>
                <td style={{ padding: '6px 8px' }}>{gib(n.requests.memBytes)} / {gib(n.allocatable.memBytes)} Gi</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </section>
  );
}

function CapCard({ label, value, color, sub }: { label: string; value: string; color?: string; sub?: string }) {
  return (
    <div style={{ background: '#fff', border: '1px solid #e5e4e7', borderRadius: '10px', padding: '16px' }}>
      <div style={{ fontSize: '28px', fontWeight: 700, color: color || '#08060d' }}>{value}</div>
      <div style={{ fontSize: '12px', color: '#6b6375', marginTop: '4px' }}>{label}</div>
      {sub && <div style={{ fontSize: '11px', color: '#9ca3af', marginTop: '2px' }}>{sub}</div>}
    </div>
  );
}
