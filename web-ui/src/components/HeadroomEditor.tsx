/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useState } from 'react';
import { fetchHeadroomConfig, setHeadroomConfig, type HeadroomConfig } from '../api/client';

// HeadroomEditor reads/writes the chart's spare-node pause-Pod
// Deployment so operators can resize headroom without `helm upgrade`.
//
// When the operator hasn't enabled `optional.headroom.enabled` in the
// chart values, the GET endpoint returns `enabled: false` and we
// render an explanatory banner pointing to docs/scaling.md instead
// of the form. This keeps the page useful even on small clusters
// that haven't opted into the proactive-scaling cost.

export default function HeadroomEditor() {
  const [cfg, setCfg] = useState<HeadroomConfig | null>(null);
  // Form state mirrors cfg but only updates onSave so polling refreshes
  // don't clobber the operator's edits.
  const [replicas, setReplicas] = useState(0);
  const [cpu, setCpu] = useState('');
  const [memory, setMemory] = useState('');
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    const refresh = async () => {
      try {
        const next = await fetchHeadroomConfig();
        if (cancelled) return;
        setCfg(next);
        if (!loaded) {
          setReplicas(next.replicas);
          setCpu(next.cpu);
          setMemory(next.memory);
          setLoaded(true);
        }
      } catch (e: any) {
        if (cancelled) return;
        setMessage('Error: ' + (e.message || 'Failed to load headroom config'));
      }
    };
    refresh();
    const interval = setInterval(refresh, 10000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [loaded]);

  const handleSave = async () => {
    setSaving(true);
    setMessage(null);
    try {
      const next = await setHeadroomConfig({ replicas, cpu, memory });
      setCfg(next);
      setReplicas(next.replicas);
      setCpu(next.cpu);
      setMemory(next.memory);
      setMessage('Saved.');
    } catch (e: any) {
      setMessage('Error: ' + (e.message || 'Failed to save'));
    } finally {
      setSaving(false);
    }
  };

  if (!cfg) {
    return <div data-testid="headroom-loading" style={{ color: '#6b6375', fontSize: '13px' }}>Loading…</div>;
  }

  if (!cfg.enabled) {
    return (
      <div data-testid="headroom-disabled" style={{ padding: '12px 16px', background: '#fffbeb', border: '1px solid #fde68a', borderRadius: '8px', fontSize: '13px', color: '#92400e' }}>
        Headroom is not enabled. Set <code>optional.headroom.enabled: true</code> in your Helm values to deploy the spare-node Deployment.
        See <a href="https://github.com/agenttier/agenttier/blob/main/docs/docs/scaling.md" target="_blank" rel="noopener" style={{ color: '#a16207' }}>docs/scaling.md</a> for sizing math + cost trade-offs.
      </div>
    );
  }

  return (
    <div data-testid="headroom-editor">
      <p style={{ fontSize: '13px', color: '#6b6375', marginTop: 0, marginBottom: '16px' }}>
        Pause Pods at deeply-negative priority squat on a spare node so real sandboxes preempt them and schedule instantly.
        The evicted Pods then trigger Cluster Autoscaler to add a fresh spare node — N+1 spare-node proactive scaling.
      </p>

      <div data-testid="headroom-status" style={{ padding: '12px 16px', background: '#f8f8fa', borderRadius: '8px', marginBottom: '16px', fontSize: '13px' }}>
        <strong>Current:</strong> {cfg.readyReplicas ?? 0} / {cfg.replicas} pause Pods ready
        ({cfg.cpu || '—'} CPU, {cfg.memory || '—'} memory each)
      </div>

      <div style={{ display: 'flex', gap: '12px', alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: '12px' }}>
        <div>
          <label style={{ display: 'block', fontSize: '12px', fontWeight: 500, marginBottom: '4px', color: '#6b6375' }}>
            Replicas
          </label>
          <input
            data-testid="headroom-replicas"
            type="number" min={0} max={50} value={replicas}
            onChange={(e) => setReplicas(Number(e.target.value))}
            style={{ width: '90px', padding: '8px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '15px' }}
          />
        </div>
        <div>
          <label style={{ display: 'block', fontSize: '12px', fontWeight: 500, marginBottom: '4px', color: '#6b6375' }}>
            CPU per replica
          </label>
          <input
            data-testid="headroom-cpu"
            type="text" placeholder="500m"
            value={cpu}
            onChange={(e) => setCpu(e.target.value)}
            style={{ width: '100px', padding: '8px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '15px' }}
          />
        </div>
        <div>
          <label style={{ display: 'block', fontSize: '12px', fontWeight: 500, marginBottom: '4px', color: '#6b6375' }}>
            Memory per replica
          </label>
          <input
            data-testid="headroom-memory"
            type="text" placeholder="1Gi"
            value={memory}
            onChange={(e) => setMemory(e.target.value)}
            style={{ width: '100px', padding: '8px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '15px' }}
          />
        </div>
        <button
          data-testid="headroom-save"
          type="button" onClick={handleSave} disabled={saving}
          style={{
            padding: '8px 20px', borderRadius: '8px', border: 'none',
            background: saving ? '#d1d5db' : '#aa3bff', color: '#fff',
            fontSize: '14px', fontWeight: 600,
            cursor: saving ? 'not-allowed' : 'pointer',
          }}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>

      {message && (
        <div
          data-testid="headroom-message"
          style={{
            marginTop: '8px',
            padding: '8px 12px', borderRadius: '6px', fontSize: '13px',
            background: message.startsWith('Error') ? '#fef2f2' : '#f0fdf4',
            color: message.startsWith('Error') ? '#dc2626' : '#16a34a',
            border: `1px solid ${message.startsWith('Error') ? '#fecaca' : '#bbf7d0'}`,
          }}
        >
          {message}
        </div>
      )}
    </div>
  );
}
