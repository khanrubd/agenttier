/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useState } from 'react';
import {
  fetchGovernance,
  setClusterGovernance,
  setNamespaceGovernance,
  deleteNamespaceGovernance,
  fetchTemplates,
} from '../api/client';
import type { GovernancePolicy, GovernanceNamespacePolicy } from '../api/client';

type Scope =
  | { kind: 'cluster' }
  | { kind: 'namespace'; name: string }
  | { kind: 'new' };

const emptyPolicy: GovernancePolicy = {};

function PolicyForm({
  value,
  templates,
  onChange,
}: {
  value: GovernancePolicy;
  templates: string[];
  onChange: (p: GovernancePolicy) => void;
}) {
  const patch = (p: Partial<GovernancePolicy>) => onChange({ ...value, ...p });

  const toggleTemplate = (name: string) => {
    const list = value.allowedTemplates ?? [];
    const next = list.includes(name) ? list.filter(t => t !== name) : [...list, name];
    patch({ allowedTemplates: next.length ? next : undefined });
  };

  const inputStyle = {
    width: '100%',
    padding: '8px 10px',
    borderRadius: '6px',
    border: '1px solid #e5e4e7',
    fontSize: '14px',
    boxSizing: 'border-box' as const,
  };
  const labelStyle = {
    display: 'block',
    fontSize: '12px',
    fontWeight: 500,
    marginBottom: '4px',
    color: '#4b4657',
  };

  return (
    <div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px', marginBottom: '16px' }}>
        <div>
          <label style={labelStyle}>Max sandboxes per user</label>
          <input
            type="number" min={0}
            value={value.maxSandboxesPerUser ?? 0}
            onChange={e => patch({ maxSandboxesPerUser: Number(e.target.value) || undefined })}
            style={inputStyle}
          />
        </div>
        <div>
          <label style={labelStyle}>Max sandboxes total</label>
          <input
            type="number" min={0}
            value={value.maxSandboxesTotal ?? 0}
            onChange={e => patch({ maxSandboxesTotal: Number(e.target.value) || undefined })}
            style={inputStyle}
          />
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '12px', marginBottom: '16px' }}>
        <div>
          <label style={labelStyle}>Max CPU (e.g. 2)</label>
          <input value={value.maxCpu ?? ''} onChange={e => patch({ maxCpu: e.target.value || undefined })} style={inputStyle} />
        </div>
        <div>
          <label style={labelStyle}>Max memory (e.g. 4Gi)</label>
          <input value={value.maxMemory ?? ''} onChange={e => patch({ maxMemory: e.target.value || undefined })} style={inputStyle} />
        </div>
        <div>
          <label style={labelStyle}>Max storage (e.g. 20Gi)</label>
          <input value={value.maxStorage ?? ''} onChange={e => patch({ maxStorage: e.target.value || undefined })} style={inputStyle} />
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px', marginBottom: '16px' }}>
        <div>
          <label style={labelStyle}>Max timeout (e.g. 24h)</label>
          <input value={value.maxTimeout ?? ''} onChange={e => patch({ maxTimeout: e.target.value || undefined })} style={inputStyle} />
        </div>
        <div>
          <label style={labelStyle}>Max idle timeout (e.g. 1h)</label>
          <input value={value.maxIdleTimeout ?? ''} onChange={e => patch({ maxIdleTimeout: e.target.value || undefined })} style={inputStyle} />
        </div>
      </div>

      <div style={{ marginBottom: '16px' }}>
        <label style={labelStyle}>Allowed templates (empty = any)</label>
        {templates.length === 0 ? (
          <div style={{ fontSize: '13px', color: '#6b6375' }}>No templates discovered.</div>
        ) : (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px' }}>
            {templates.map(t => {
              const on = (value.allowedTemplates ?? []).includes(t);
              return (
                <button
                  key={t}
                  type="button"
                  onClick={() => toggleTemplate(t)}
                  style={{
                    padding: '4px 10px',
                    borderRadius: '999px',
                    border: `1px solid ${on ? '#aa3bff' : '#e5e4e7'}`,
                    background: on ? '#f3e8ff' : '#ffffff',
                    color: on ? '#5b21b6' : '#4b4657',
                    fontSize: '12px',
                    cursor: 'pointer',
                  }}
                >
                  {t}
                </button>
              );
            })}
          </div>
        )}
      </div>

      <div style={{ marginBottom: '16px' }}>
        <label style={labelStyle}>Approved image registries (comma-separated; empty = any)</label>
        <input
          value={(value.approvedRegistries ?? []).join(', ')}
          onChange={e => {
            const list = e.target.value
              .split(',')
              .map(s => s.trim())
              .filter(s => s.length > 0);
            patch({ approvedRegistries: list.length ? list : undefined });
          }}
          placeholder="ghcr.io/agenttier, 582483581248.dkr.ecr.us-east-1.amazonaws.com"
          style={inputStyle}
        />
      </div>

      <div>
        <label style={labelStyle}>Description</label>
        <input value={value.description ?? ''} onChange={e => patch({ description: e.target.value || undefined })} style={inputStyle} />
      </div>
    </div>
  );
}

export default function GovernanceEditor({ isAdmin }: { isAdmin: boolean }) {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [flash, setFlash] = useState<string | null>(null);
  const [templates, setTemplates] = useState<string[]>([]);
  const [cluster, setCluster] = useState<GovernancePolicy>(emptyPolicy);
  const [namespaces, setNamespaces] = useState<GovernanceNamespacePolicy[]>([]);
  const [scope, setScope] = useState<Scope>({ kind: 'cluster' });
  const [draft, setDraft] = useState<GovernancePolicy>(emptyPolicy);
  const [draftNs, setDraftNs] = useState('default');

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const [bundle, templateList] = await Promise.all([fetchGovernance(), fetchTemplates()]);
        if (cancelled) return;
        setCluster(bundle.cluster ?? emptyPolicy);
        setNamespaces(bundle.namespaces ?? []);
        setTemplates((templateList ?? []).map(t => t.name));
        // Seed draft from current scope.
        if (scope.kind === 'cluster') setDraft(bundle.cluster ?? emptyPolicy);
      } catch (e: any) {
        if (!cancelled) setError(e.message || 'Failed to load governance policies');
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, []);

  // When the scope selector changes, reseed the draft.
  useEffect(() => {
    if (scope.kind === 'cluster') setDraft(cluster);
    else if (scope.kind === 'namespace') {
      const existing = namespaces.find(n => n.namespace === scope.name);
      setDraft(existing?.policy ?? emptyPolicy);
    } else {
      setDraft(emptyPolicy);
    }
  }, [scope, cluster, namespaces]);

  const refresh = async () => {
    const bundle = await fetchGovernance();
    setCluster(bundle.cluster ?? emptyPolicy);
    setNamespaces(bundle.namespaces ?? []);
  };

  const onSave = async () => {
    setSaving(true);
    setError(null);
    setFlash(null);
    try {
      if (scope.kind === 'cluster') {
        await setClusterGovernance(draft);
        setFlash('Cluster policy saved.');
      } else {
        const name = scope.kind === 'new' ? draftNs.trim() : scope.name;
        if (!name) throw new Error('Namespace name is required');
        await setNamespaceGovernance(name, draft);
        setScope({ kind: 'namespace', name });
        setFlash(`Policy for namespace "${name}" saved.`);
      }
      await refresh();
    } catch (e: any) {
      setError(e.message || 'Failed to save policy');
    } finally {
      setSaving(false);
    }
  };

  const onDelete = async () => {
    if (scope.kind !== 'namespace') return;
    if (!window.confirm(`Delete governance policy for namespace "${scope.name}"?`)) return;
    setSaving(true);
    setError(null);
    setFlash(null);
    try {
      await deleteNamespaceGovernance(scope.name);
      setFlash(`Policy for namespace "${scope.name}" removed.`);
      setScope({ kind: 'cluster' });
      await refresh();
    } catch (e: any) {
      setError(e.message || 'Failed to delete policy');
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <div style={{ fontSize: '13px', color: '#6b6375' }}>Loading governance policies…</div>;

  const scopeLabel =
    scope.kind === 'cluster'
      ? 'Cluster default'
      : scope.kind === 'new'
        ? 'New namespace policy'
        : `Namespace: ${scope.name}`;

  return (
    <section style={{ marginBottom: '32px' }}>
      <h2 style={{ fontSize: '16px', fontWeight: 600, color: '#08060d', marginBottom: '8px' }}>Governance policies</h2>
      <p style={{ fontSize: '13px', color: '#6b6375', marginTop: 0, marginBottom: '16px' }}>
        Cluster defaults apply to every namespace. Namespace policies override field-by-field — fields left
        at zero or empty fall through to the cluster default. Policies are enforced at sandbox creation.
      </p>

      {!isAdmin && (
        <div style={{ padding: '10px 12px', borderRadius: '6px', background: '#fef9c3', color: '#713f12', fontSize: '13px', marginBottom: '16px' }}>
          Read-only: editing governance requires an admin role.
        </div>
      )}

      <div style={{ display: 'flex', gap: '8px', alignItems: 'center', marginBottom: '16px', flexWrap: 'wrap' }}>
        <button
          type="button"
          onClick={() => setScope({ kind: 'cluster' })}
          style={{
            padding: '6px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '13px',
            background: scope.kind === 'cluster' ? '#f3e8ff' : '#ffffff',
            color: scope.kind === 'cluster' ? '#5b21b6' : '#4b4657',
            cursor: 'pointer',
          }}
        >
          Cluster default
        </button>
        {namespaces.map(ns => (
          <button
            key={ns.namespace}
            type="button"
            onClick={() => setScope({ kind: 'namespace', name: ns.namespace })}
            style={{
              padding: '6px 12px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '13px',
              background: scope.kind === 'namespace' && scope.name === ns.namespace ? '#f3e8ff' : '#ffffff',
              color: scope.kind === 'namespace' && scope.name === ns.namespace ? '#5b21b6' : '#4b4657',
              cursor: 'pointer',
            }}
          >
            {ns.namespace}
          </button>
        ))}
        <button
          type="button"
          onClick={() => setScope({ kind: 'new' })}
          style={{ padding: '6px 12px', borderRadius: '6px', border: '1px dashed #d1c9dc', background: '#ffffff', color: '#4b4657', fontSize: '13px', cursor: 'pointer' }}
        >
          + Add namespace
        </button>
      </div>

      <div style={{ padding: '16px', background: '#fafafa', border: '1px solid #eeeaf3', borderRadius: '10px' }}>
        <div style={{ fontSize: '14px', fontWeight: 600, marginBottom: '12px' }}>{scopeLabel}</div>
        {scope.kind === 'new' && (
          <div style={{ marginBottom: '16px' }}>
            <label style={{ display: 'block', fontSize: '12px', fontWeight: 500, marginBottom: '4px', color: '#4b4657' }}>Namespace</label>
            <input
              value={draftNs}
              onChange={e => setDraftNs(e.target.value)}
              placeholder="default"
              style={{ width: '260px', padding: '8px 10px', borderRadius: '6px', border: '1px solid #e5e4e7', fontSize: '14px' }}
            />
          </div>
        )}
        <fieldset disabled={!isAdmin} style={{ border: 0, padding: 0, margin: 0 }}>
          <PolicyForm value={draft} templates={templates} onChange={setDraft} />
        </fieldset>

        {error && (
          <div style={{ padding: '8px 12px', borderRadius: '6px', fontSize: '13px', marginTop: '16px', background: '#fef2f2', color: '#dc2626', border: '1px solid #fecaca' }}>
            {error}
          </div>
        )}
        {flash && !error && (
          <div style={{ padding: '8px 12px', borderRadius: '6px', fontSize: '13px', marginTop: '16px', background: '#f0fdf4', color: '#16a34a', border: '1px solid #bbf7d0' }}>
            {flash}
          </div>
        )}

        {isAdmin && (
          <div style={{ display: 'flex', gap: '8px', marginTop: '20px' }}>
            <button
              type="button"
              onClick={onSave}
              disabled={saving}
              style={{ padding: '8px 20px', borderRadius: '8px', border: 'none', background: saving ? '#d1d5db' : '#aa3bff', color: '#fff', fontSize: '14px', fontWeight: 600, cursor: saving ? 'not-allowed' : 'pointer' }}
            >
              {saving ? 'Saving…' : 'Save policy'}
            </button>
            {scope.kind === 'namespace' && (
              <button
                type="button"
                onClick={onDelete}
                disabled={saving}
                style={{ padding: '8px 16px', borderRadius: '8px', border: '1px solid #e5e4e7', background: '#ffffff', color: '#dc2626', fontSize: '14px', fontWeight: 500, cursor: saving ? 'not-allowed' : 'pointer' }}
              >
                Delete policy
              </button>
            )}
          </div>
        )}
      </div>
    </section>
  );
}
