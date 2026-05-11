/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useState } from 'react';
import {
  listPorts,
  forwardPort,
  removePort,
  previewProxyUrl,
} from '../api/client';
import type { PortForward } from '../api/client';

interface Props {
  sandboxId: string;
  /** Only show the panel when the sandbox is actively running. */
  running: boolean;
}

export default function PortForwardsPanel({ sandboxId, running }: Props) {
  const [ports, setPorts] = useState<PortForward[]>([]);
  const [loading, setLoading] = useState(false);
  const [draftPort, setDraftPort] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reload = async () => {
    try {
      setLoading(true);
      const list = await listPorts(sandboxId);
      setPorts(list);
    } catch (e: any) {
      setError(e.message ?? 'Failed to load ports');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (running) reload();
  }, [sandboxId, running]);

  if (!running) return null;

  const add = async () => {
    setError(null);
    const parsed = parseInt(draftPort, 10);
    if (!Number.isFinite(parsed) || parsed < 1 || parsed > 65535) {
      setError('Port must be between 1 and 65535');
      return;
    }
    setBusy(true);
    try {
      await forwardPort(sandboxId, parsed);
      setDraftPort('');
      await reload();
    } catch (e: any) {
      setError(e.message ?? 'Failed to forward port');
    } finally {
      setBusy(false);
    }
  };

  const del = async (port: number) => {
    setBusy(true);
    setError(null);
    try {
      await removePort(sandboxId, port);
      await reload();
    } catch (e: any) {
      setError(e.message ?? 'Failed to remove port');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ marginTop: '12px', padding: '10px 12px', borderRadius: '8px', background: '#faf9fc', border: '1px dashed #d4d0e0' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '6px' }}>
        <span style={{ fontSize: '12px', fontWeight: 600, color: '#4b4657', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
          Port forwards
        </span>
        {loading && <span style={{ fontSize: '11px', color: '#6b6375' }}>loading…</span>}
      </div>

      {ports.length === 0 && !loading && (
        <div style={{ fontSize: '12px', color: '#6b6375', marginBottom: '8px' }}>
          No ports exposed yet.
        </div>
      )}

      {ports.length > 0 && (
        <ul style={{ margin: 0, padding: 0, listStyle: 'none', marginBottom: '8px' }}>
          {ports.map(p => {
            const proxyHref = previewProxyUrl(sandboxId, p.port);
            return (
              <li key={p.port} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '4px 0', fontSize: '13px' }}>
                <span style={{
                  display: 'inline-block', padding: '1px 8px', borderRadius: '4px',
                  background: '#ede9fe', color: '#5b21b6', fontWeight: 500, fontSize: '12px',
                }}>
                  {p.port}
                </span>
                <a href={proxyHref} target="_blank" rel="noreferrer" style={{ color: '#aa3bff', fontSize: '12px' }}>
                  preview
                </a>
                {p.previewUrl && (
                  <a href={p.previewUrl} target="_blank" rel="noreferrer" style={{ color: '#6b6375', fontSize: '12px' }}>
                    public
                  </a>
                )}
                <button
                  onClick={() => del(p.port)}
                  disabled={busy}
                  style={{
                    marginLeft: 'auto', padding: '2px 8px', borderRadius: '4px',
                    border: '1px solid #fecaca', background: '#fff', color: '#dc2626',
                    fontSize: '11px', cursor: busy ? 'not-allowed' : 'pointer',
                  }}
                >
                  remove
                </button>
              </li>
            );
          })}
        </ul>
      )}

      <div style={{ display: 'flex', gap: '6px' }}>
        <input
          value={draftPort}
          onChange={e => setDraftPort(e.target.value.replace(/[^0-9]/g, ''))}
          placeholder="port (e.g. 8080)"
          style={{
            flex: 1, padding: '4px 8px', fontSize: '12px',
            borderRadius: '4px', border: '1px solid #e5e4e7',
          }}
        />
        <button
          onClick={add}
          disabled={busy || !draftPort}
          style={{
            padding: '4px 12px', fontSize: '12px', borderRadius: '4px',
            border: 'none', background: busy || !draftPort ? '#d1d5db' : '#aa3bff',
            color: '#fff', fontWeight: 500, cursor: busy || !draftPort ? 'not-allowed' : 'pointer',
          }}
        >
          Forward
        </button>
      </div>

      {error && (
        <div style={{ marginTop: '6px', fontSize: '12px', color: '#dc2626' }}>{error}</div>
      )}
    </div>
  );
}
