/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import type { Sandbox } from '../types';
import { fetchSandbox } from '../api/client';
import StatusBadge from '../components/StatusBadge';
import PortForwardsPanel from '../components/PortForwardsPanel';
import FilesPanel from '../components/FilesPanel';
import AgentPanel from '../components/AgentPanel';

// SandboxSettings is the per-sandbox details page reachable from the
// gear icon on each Dashboard card.
//
// Why a dedicated page instead of an in-card accordion (the previous
// design): per-sandbox settings are growing — port forwards, files,
// agent invoke, and future additions like governance overrides,
// network rules, env vars, mode-specific knobs. Stuffing all of those
// behind a card-internal "Advanced" toggle worked while there were
// three sub-panels but stops scaling once the list crosses ~6.
//
// URL shape: /sandbox/:id/settings — sibling of /sandbox/:id/terminal.
// Lives inside the standard <Layout> chrome so the left nav (cluster
// status, warm pool, navigation) stays visible — operators usually
// keep the dashboard open in another tab and use this page as a deep-
// dive view.
//
// Polling: re-fetches the sandbox every 5s to mirror the Dashboard's
// polling cadence. Keeps the status badge fresh without a separate
// refresh control.

const POLL_INTERVAL = 5000;

export default function SandboxSettings() {
  const { id } = useParams<{ id: string }>();
  const [sandbox, setSandbox] = useState<Sandbox | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const sb = await fetchSandbox(id);
        if (cancelled) return;
        setSandbox(sb);
        setError(null);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : 'Failed to load sandbox');
      }
    };
    refresh();
    const interval = setInterval(refresh, POLL_INTERVAL);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [id]);

  if (!id) {
    return <ErrorState message="Missing sandbox id." />;
  }
  if (error && !sandbox) {
    return <ErrorState message={error} />;
  }
  if (!sandbox) {
    return (
      <div data-testid="sandbox-settings-loading" style={{ padding: '40px', color: '#6b6375' }}>
        Loading sandbox…
      </div>
    );
  }

  const running = sandbox.status === 'running';

  return (
    <div data-testid="sandbox-settings-page" style={{ padding: '0 32px 40px', maxWidth: '900px' }}>
      {/* Header: back link + sandbox name + status + mode. Mirrors the
          Dashboard card's title row so the navigation context is
          obvious. */}
      <header style={{ margin: '24px 0 16px' }}>
        <Link to="/" data-testid="back-to-dashboard" style={{ color: '#aa3bff', fontSize: '13px', textDecoration: 'none' }}>
          ← Dashboard
        </Link>
        <h1 data-testid="sandbox-settings-title" style={{ display: 'flex', alignItems: 'center', gap: '12px', fontSize: '24px', fontWeight: 700, color: '#08060d', margin: '8px 0 4px' }}>
          {sandbox.name}
          <StatusBadge status={sandbox.status} />
          <ModeBadge mode={sandbox.mode} />
        </h1>
        <div style={{ fontSize: '13px', color: '#6b6375' }}>
          Settings &amp; advanced controls
        </div>
      </header>

      {/* Overview block: read-only metadata. Mirrors the card's metadata
          rows but with more detail because we have the room here. */}
      <Section title="Overview">
        <KeyValue label="Sandbox ID" value={sandbox.id} testId="settings-sandbox-id" />
        <KeyValue label="Template" value={sandbox.template || '—'} testId="settings-template" />
        <KeyValue label="Mode" value={sandbox.mode === 'agent' ? 'Agent' : 'Code'} testId="settings-mode" />
        <KeyValue label="Namespace" value={sandbox.namespace} testId="settings-namespace" />
        <KeyValue label="Created by" value={sandbox.created_by_email || '—'} testId="settings-created-by" />
        <KeyValue label="Created at" value={formatDate(sandbox.created_at)} testId="settings-created-at" />
        <KeyValue label="Last accessed" value={formatDate(sandbox.last_accessed_at)} testId="settings-last-accessed" />
      </Section>

      {/* Port forwards. Pre-existing component, lifted from the inline
          card panel into this page. Accepts a `running` prop so it
          renders a "sandbox is not running" hint when stopped. */}
      <Section title="Port forwards">
        <PortForwardsPanel sandboxId={sandbox.id} running={running} />
      </Section>

      {/* Files. Same lift-and-shift. */}
      <Section title="Files">
        <FilesPanel sandboxId={sandbox.id} running={running} />
      </Section>

      {/* Agent invoke. Same lift-and-shift. The component itself
          short-circuits to a "no-op" UI when mode !== 'agent', so we
          can render it for code-mode sandboxes too without breaking. */}
      <Section title="Agent">
        <AgentPanel sandboxId={sandbox.id} running={running} />
      </Section>

      {/* Future settings (governance overrides, env vars, network
          rules, mode-specific knobs) plug in here as new <Section>
          blocks. Adding a new one is one component import + one
          <Section> wrapper. */}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section data-testid={`section-${title.toLowerCase().replace(/\s+/g, '-')}`}
      style={{
        marginBottom: '24px',
        background: '#fff',
        border: '1px solid #e8e6ed',
        borderRadius: '10px',
        padding: '20px 24px',
      }}>
      <h2 style={{ fontSize: '15px', fontWeight: 600, color: '#08060d', margin: '0 0 12px' }}>{title}</h2>
      {children}
    </section>
  );
}

function KeyValue({ label, value, testId }: { label: string; value: string; testId: string }) {
  return (
    <div data-testid={testId} style={{
      display: 'grid',
      gridTemplateColumns: '160px 1fr',
      fontSize: '13px',
      color: '#4b4657',
      padding: '4px 0',
    }}>
      <span style={{ color: '#6b6375', fontWeight: 500 }}>{label}</span>
      <span style={{ fontFamily: 'inherit' }}>{value}</span>
    </div>
  );
}

function ModeBadge({ mode }: { mode: 'code' | 'agent' }) {
  const isAgent = mode === 'agent';
  return (
    <span
      data-testid="sandbox-mode-badge"
      data-mode={mode}
      style={{
        fontSize: '11px',
        fontWeight: 600,
        padding: '3px 9px',
        borderRadius: '999px',
        background: isAgent ? '#dcfce7' : '#eef2ff',
        color: isAgent ? '#15803d' : '#4338ca',
        textTransform: 'uppercase',
        letterSpacing: '0.04em',
      }}
    >
      {isAgent ? 'Agent' : 'Code'}
    </span>
  );
}

function ErrorState({ message }: { message: string }) {
  return (
    <div data-testid="sandbox-settings-error" style={{ padding: '40px 32px', maxWidth: '600px' }}>
      <Link to="/" style={{ color: '#aa3bff', fontSize: '13px', textDecoration: 'none' }}>← Dashboard</Link>
      <h1 style={{ marginTop: '12px', fontSize: '20px', color: '#ef4444' }}>Sandbox not found</h1>
      <p style={{ color: '#6b6375', fontSize: '14px' }}>{message}</p>
    </div>
  );
}

function formatDate(iso: string | null): string {
  if (!iso) return '—';
  return new Date(iso).toLocaleString();
}
