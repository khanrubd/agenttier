/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import type { Sandbox, Template, ActivityEntry, Analytics, CostEstimate } from '../types';

// API base URL — configurable via environment variable.
// In production (behind nginx), API is proxied through the same origin at /api/v1.
// For local dev, point to the Router directly.
const API_BASE = import.meta.env.VITE_API_BASE_URL || '/api/v1';

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  });
  if (!res.ok) {
    const body = await res.text().catch(() => res.statusText);
    throw new Error(`${res.status}: ${body}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

// --- Sandbox API ---

export async function fetchSandboxes(): Promise<Sandbox[]> {
  const data = await request<{ sandboxes: any[] }>('/sandboxes');
  return (data.sandboxes || []).map(mapSandbox);
}

export async function createSandbox(name: string, template: string): Promise<Sandbox> {
  const data = await request<any>('/sandboxes', {
    method: 'POST',
    body: JSON.stringify({
      name,
      templateRef: { name: template, kind: 'ClusterSandboxTemplate' },
    }),
  });
  return mapSandbox(data);
}

export async function stopSandbox(id: string): Promise<void> {
  await request(`/sandboxes/${id}/stop`, { method: 'POST' });
}

export async function resumeSandbox(id: string): Promise<void> {
  await request(`/sandboxes/${id}/resume`, { method: 'POST' });
}

export async function deleteSandbox(id: string): Promise<void> {
  await request(`/sandboxes/${id}`, { method: 'DELETE' });
}

// --- Template API ---

export async function fetchTemplates(): Promise<Template[]> {
  const data = await request<{ templates: any[] }>('/templates');
  return (data.templates || []).map((t: any) => ({
    name: t.name,
    description: t.description || '',
    image: t.image || '',
    resourceVersion: t.resourceVersion || '',
    spec: t.spec || undefined,
  }));
}

export async function fetchTemplate(name: string): Promise<Template> {
  const t = await request<any>(`/templates/${name}`);
  return {
    name: t.name,
    description: t.description || '',
    image: t.image || '',
    resourceVersion: t.resourceVersion || '',
    spec: t.spec || undefined,
  };
}

export async function createTemplate(name: string, spec: any): Promise<Template> {
  const t = await request<any>('/templates', {
    method: 'POST',
    body: JSON.stringify({ name, spec }),
  });
  return { name: t.name, description: t.description || '', image: t.image || '', resourceVersion: t.resourceVersion, spec: t.spec };
}

export async function updateTemplate(name: string, spec: any): Promise<Template> {
  const t = await request<any>(`/templates/${name}`, {
    method: 'PUT',
    body: JSON.stringify({ spec }),
  });
  return { name: t.name, description: t.description || '', image: t.image || '', resourceVersion: t.resourceVersion, spec: t.spec };
}

export async function deleteTemplate(name: string): Promise<void> {
  await request(`/templates/${name}`, { method: 'DELETE' });
}

// --- Activity API ---

export async function fetchActivity(): Promise<ActivityEntry[]> {
  const data = await request<{ events: any[] }>('/audit/events');
  return (data.events || []).map((e: any) => ({
    timestamp: e.timestamp,
    user_email: e.userEmail || '',
    action: e.eventType || '',
    sandbox_id: e.sandboxId || '',
    sandbox_name: e.sandboxName || '',
    details: e.details?.reason || '',
  }));
}

// --- Analytics API ---

export async function fetchAnalytics(): Promise<Analytics> {
  return request<Analytics>('/analytics/usage');
}

// --- Costs API ---

export async function fetchCosts(): Promise<CostEstimate> {
  return request<CostEstimate>('/analytics/costs');
}

// --- User API ---

export interface User {
  sub: string;
  email: string;
  name: string;
}

export async function fetchCurrentUser(): Promise<User> {
  return request<User>('/user/preferences');
}

// --- Warm Pool API ---

export interface WarmPoolStatus {
  desiredCount: number;
  readyCount: number;
  pendingCount: number;
  template: string;
}

export async function fetchWarmPoolStatus(): Promise<WarmPoolStatus> {
  return request<WarmPoolStatus>('/warmpool/status');
}

export async function setWarmPoolConfig(desiredCount: number, template: string): Promise<void> {
  await request('/warmpool/config', {
    method: 'PUT',
    body: JSON.stringify({ desiredCount, template }),
  });
}

// --- Helpers ---

function mapSandbox(raw: any): Sandbox {
  return {
    id: raw.sandboxId || raw.id || raw.name,
    name: raw.name || raw.sandboxId,
    status: (raw.status || 'creating').toLowerCase() as Sandbox['status'],
    template: raw.templateRef || raw.template || '',
    error_message: raw.message || null,
    created_at: raw.createdAt || raw.created_at || '',
    last_accessed_at: raw.lastActivityAt || raw.last_accessed_at || null,
    created_by: raw.createdBy?.displayName || raw.created_by || '',
    created_by_email: raw.createdBy?.email || raw.created_by_email || '',
  };
}
