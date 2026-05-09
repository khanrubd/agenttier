/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, useEffect } from 'react';
import { createSandbox, fetchTemplates } from '../api/client';
import type { Template } from '../types';

interface Props {
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
}

export default function CreateSandboxDialog({ open, onClose, onCreated }: Props) {
  const [name, setName] = useState('');
  const [template, setTemplate] = useState('');
  const [templates, setTemplates] = useState<Template[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) {
      fetchTemplates().then(setTemplates).catch(() => {});
    }
  }, [open]);

  if (!open) return null;

  const trimmed = name.trim();
  const isValid = trimmed.length > 0 && template.length > 0;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!trimmed) { setError('Name cannot be empty'); return; }
    if (!template) { setError('Please select a template'); return; }
    setError(null);
    setSubmitting(true);
    try {
      await createSandbox(trimmed, template);
      setName('');
      setTemplate('');
      onCreated();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create sandbox');
    } finally {
      setSubmitting(false);
    }
  };

  const handleClose = () => { setName(''); setTemplate(''); setError(null); onClose(); };

  return (
    <div data-testid="create-dialog-overlay" onClick={handleClose}
      style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.5)', display: 'flex',
        alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
      <div data-testid="create-dialog" onClick={(e) => e.stopPropagation()}
        style={{ background: '#fff', border: '1px solid #e5e4e7',
          borderRadius: '12px', padding: '28px', width: '440px', maxWidth: '90vw',
          boxShadow: '0 8px 30px rgba(0,0,0,0.15)' }}>
        <h2 style={{ margin: '0 0 20px', fontSize: '20px', color: '#08060d' }}>Create Sandbox</h2>
        <form onSubmit={handleSubmit}>
          {/* Sandbox name */}
          <label htmlFor="sandbox-name" style={{ display: 'block', marginBottom: '6px', fontSize: '14px', fontWeight: 500 }}>
            Name
          </label>
          <input id="sandbox-name" data-testid="sandbox-name-input" type="text" value={name}
            onChange={(e) => { setName(e.target.value); setError(null); }}
            maxLength={100} placeholder="my-sandbox" autoFocus
            style={{ width: '100%', padding: '10px 12px', borderRadius: '8px',
              border: '1px solid #e5e4e7', fontSize: '15px', background: '#fff', color: '#08060d',
              boxSizing: 'border-box', outline: 'none', marginBottom: '16px' }} />

          {/* Template dropdown */}
          <label htmlFor="template-select" style={{ display: 'block', marginBottom: '6px', fontSize: '14px', fontWeight: 500 }}>
            Template
          </label>
          <select id="template-select" value={template} onChange={e => { setTemplate(e.target.value); setError(null); }}
            style={{ width: '100%', padding: '10px 12px', borderRadius: '8px',
              border: '1px solid #e5e4e7', fontSize: '15px', background: '#fff', color: '#08060d',
              boxSizing: 'border-box', outline: 'none', marginBottom: '4px' }}>
            <option value="">Select a template…</option>
            {templates.map(t => (
              <option key={t.name} value={t.name}>{t.name} — {t.description || 'No description'}</option>
            ))}
          </select>
          <div style={{ fontSize: '11px', color: '#6b6375', marginBottom: '16px' }}>
            Templates define the image, tools, and configuration for your sandbox. Edit them on the Templates page.
          </div>

          {error && (
            <p data-testid="validation-error" style={{ color: '#ef4444', fontSize: '13px', margin: '0 0 12px' }}>{error}</p>
          )}

          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '10px' }}>
            <button type="button" data-testid="cancel-button" onClick={handleClose}
              style={{ padding: '8px 18px', borderRadius: '8px', border: '1px solid #e5e4e7',
                background: 'transparent', color: '#6b6375', fontSize: '14px', cursor: 'pointer' }}>Cancel</button>
            <button type="submit" data-testid="create-button" disabled={!isValid || submitting}
              style={{ padding: '8px 18px', borderRadius: '8px', border: 'none',
                background: isValid && !submitting ? '#aa3bff' : '#d1d5db',
                color: isValid && !submitting ? '#fff' : '#9ca3af',
                fontSize: '14px', fontWeight: 600, cursor: isValid && !submitting ? 'pointer' : 'not-allowed' }}>
              {submitting ? 'Creating…' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
