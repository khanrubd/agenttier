/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, useEffect } from 'react';
import { fetchWarmPoolStatus, setWarmPoolConfig, fetchTemplates, fetchCurrentUser } from '../api/client';
import type { Template } from '../types';
import GovernanceEditor from '../components/GovernanceEditor';

export default function Settings() {
  const [warmPoolCount, setWarmPoolCount] = useState(0);
  const [warmPoolTemplate, setWarmPoolTemplate] = useState('general-coding');
  const [templates, setTemplates] = useState<Template[]>([]);
  const [currentStatus, setCurrentStatus] = useState<{ readyCount: number; pendingCount: number; desiredCount: number } | null>(null);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [isAdmin, setIsAdmin] = useState(false);

  useEffect(() => {
    fetchTemplates().then(setTemplates).catch(() => {});
    fetchWarmPoolStatus().then(s => {
      setCurrentStatus(s);
      setWarmPoolCount(s.desiredCount);
      if (s.template) setWarmPoolTemplate(s.template);
    }).catch(() => {});
    fetchCurrentUser()
      .then(u => setIsAdmin(Boolean(u.isAdmin)))
      .catch(() => setIsAdmin(false));
  }, []);

  const handleSave = async () => {
    setSaving(true);
    setMessage(null);
    try {
      await setWarmPoolConfig(warmPoolCount, warmPoolTemplate);
      setMessage('Saved. Pool will converge within ~10 seconds.');
      // Refresh status after a delay
      setTimeout(async () => {
        const s = await fetchWarmPoolStatus();
        setCurrentStatus(s);
      }, 3000);
    } catch (e: any) {
      setMessage('Error: ' + (e.message || 'Failed to save'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ padding: '32px', maxWidth: '760px' }}>
      <h1 style={{ fontSize: '22px', fontWeight: 700, color: '#08060d', marginBottom: '24px' }}>Settings</h1>

      <GovernanceEditor isAdmin={isAdmin} />

      <section style={{ marginBottom: '32px' }}>
        <h2 style={{ fontSize: '16px', fontWeight: 600, color: '#08060d', marginBottom: '12px' }}>Warm Pool</h2>
        <p style={{ fontSize: '13px', color: '#6b6375', marginTop: 0, marginBottom: '16px' }}>
          Pre-create idle sandbox pods so new sandboxes start instantly (&lt;2s) instead of waiting for provisioning (~10s).
          When a user creates a sandbox, it claims a warm pod. A replacement is created in the background.
        </p>

        {currentStatus && (
          <div style={{ padding: '12px 16px', background: '#f8f8fa', borderRadius: '8px', marginBottom: '16px', fontSize: '13px' }}>
            <strong>Current status:</strong> {currentStatus.readyCount} ready, {currentStatus.pendingCount} pending (target: {currentStatus.desiredCount})
          </div>
        )}

        <div style={{ display: 'flex', gap: '16px', alignItems: 'flex-end', marginBottom: '12px' }}>
          <div>
            <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, marginBottom: '4px' }}>Pool Size</label>
            <input
              type="number" min={0} max={10} value={warmPoolCount}
              onChange={e => setWarmPoolCount(Number(e.target.value))}
              style={{ width: '70px', padding: '8px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '15px' }}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, marginBottom: '4px' }}>Template</label>
            <select value={warmPoolTemplate} onChange={e => setWarmPoolTemplate(e.target.value)}
              style={{ width: '100%', padding: '8px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '14px' }}>
              {templates.map(t => (
                <option key={t.name} value={t.name}>{t.name}</option>
              ))}
            </select>
          </div>
        </div>

        {message && (
          <div style={{ padding: '8px 12px', borderRadius: '6px', fontSize: '13px', marginBottom: '16px',
            background: message.startsWith('Error') ? '#fef2f2' : '#f0fdf4',
            color: message.startsWith('Error') ? '#dc2626' : '#16a34a',
            border: `1px solid ${message.startsWith('Error') ? '#fecaca' : '#bbf7d0'}` }}>
            {message}
          </div>
        )}

        <button onClick={handleSave} disabled={saving}
          style={{ padding: '8px 20px', borderRadius: '8px', border: 'none', background: saving ? '#d1d5db' : '#aa3bff', color: '#fff', fontSize: '14px', fontWeight: 600, cursor: saving ? 'not-allowed' : 'pointer' }}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </section>
    </div>
  );
}
