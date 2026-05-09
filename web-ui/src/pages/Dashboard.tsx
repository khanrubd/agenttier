/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, useEffect, useCallback } from 'react';
import type { Sandbox } from '../types';
import { fetchSandboxes, stopSandbox, resumeSandbox, deleteSandbox } from '../api/client';
import SandboxCard from '../components/SandboxCard';
import CreateSandboxDialog from '../components/CreateSandboxDialog';

const POLL_INTERVAL = 5000;
const TOAST_DURATION = 4000;

export default function Dashboard() {
  const [sandboxes, setSandboxes] = useState<Sandbox[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set());

  const showToast = useCallback((message: string) => {
    setToast(message);
    setTimeout(() => setToast(null), TOAST_DURATION);
  }, []);

  const loadSandboxes = useCallback(async (showLoading = false) => {
    try {
      if (showLoading) setLoading(true);
      const data = await fetchSandboxes();
      setSandboxes(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load sandboxes');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadSandboxes(true);
    const interval = setInterval(() => loadSandboxes(false), POLL_INTERVAL);
    return () => clearInterval(interval);
  }, [loadSandboxes]);

  const handleStop = async (id: string) => {
    setBusyIds((prev) => new Set(prev).add(id));
    try {
      await stopSandbox(id);
      await loadSandboxes();
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to stop sandbox');
    } finally {
      setBusyIds((prev) => { const next = new Set(prev); next.delete(id); return next; });
    }
  };

  const handleResume = async (id: string) => {
    setBusyIds((prev) => new Set(prev).add(id));
    try {
      await resumeSandbox(id);
      await loadSandboxes();
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to resume sandbox');
    } finally {
      setBusyIds((prev) => { const next = new Set(prev); next.delete(id); return next; });
    }
  };

  const handleDelete = async (id: string) => {
    if (!window.confirm('Are you sure you want to delete this sandbox? This is irreversible.')) return;
    setBusyIds((prev) => new Set(prev).add(id));
    try {
      await deleteSandbox(id);
      setSandboxes((prev) => prev.filter((s) => s.id !== id));
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to delete sandbox');
    } finally {
      setBusyIds((prev) => { const next = new Set(prev); next.delete(id); return next; });
    }
  };

  return (
    <div style={{ padding: '0 32px 40px', textAlign: 'left' }}>
      <header style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', margin: '24px 0' }}>
        <h1 style={{ fontSize: '28px', margin: 0, letterSpacing: '-0.5px' }}>Sandboxes</h1>
        <button data-testid="create-sandbox-button" onClick={() => setDialogOpen(true)}
          style={{ padding: '10px 20px', borderRadius: '8px', border: 'none', background: '#aa3bff',
            color: '#fff', fontSize: '15px', fontWeight: 600, cursor: 'pointer', whiteSpace: 'nowrap' }}>
          + Create Sandbox
        </button>
      </header>

      <CreateSandboxDialog open={dialogOpen} onClose={() => setDialogOpen(false)} onCreated={() => loadSandboxes(true)} />

      <p style={{ color: '#6b6375', marginBottom: '24px' }}>Manage your cloud sandboxes</p>

      {loading && sandboxes.length === 0 && (
        <div data-testid="loading-state" style={{ textAlign: 'center', padding: '60px 0', color: '#6b6375' }}>
          <div style={{ width: 32, height: 32, border: '3px solid #e5e4e7', borderTopColor: '#aa3bff',
            borderRadius: '50%', animation: 'spin 0.8s linear infinite', margin: '0 auto 12px' }} />
          Loading sandboxes…
        </div>
      )}
      {error && sandboxes.length === 0 && (
        <div data-testid="error-state" style={{ textAlign: 'center', padding: '60px 0', color: '#ef4444' }}>{error}</div>
      )}
      {!loading && !error && sandboxes.length === 0 && (
        <div data-testid="empty-state" style={{ textAlign: 'center', padding: '60px 0', color: '#6b6375' }}>
          No sandboxes yet. Create one to get started.
        </div>
      )}

      <div data-testid="sandbox-list" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(380px, 1fr))', gap: '16px' }}>
        {sandboxes.map((sb) => (
          <SandboxCard key={sb.id} sandbox={sb} busy={busyIds.has(sb.id)}
            onStop={handleStop} onResume={handleResume} onDelete={handleDelete} />
        ))}
      </div>

      {toast && (
        <div data-testid="toast-notification" style={{ position: 'fixed', bottom: '24px', right: '24px',
          background: '#ef4444', color: '#fff', padding: '12px 20px', borderRadius: '8px', fontSize: '14px',
          fontWeight: 500, boxShadow: '0 4px 12px rgba(0,0,0,0.15)', zIndex: 2000, maxWidth: '400px' }}>
          {toast}
        </div>
      )}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  );
}
