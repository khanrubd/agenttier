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
  isAdmin?: boolean;
  groups?: string[];
}

export async function fetchCurrentUser(): Promise<User> {
  return request<User>('/user/me');
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

// --- Port Forwarding API ---

export interface PortForward {
  port: number;
  protocol: string;
  previewUrl?: string;
  internalUrl?: string;
}

export async function listPorts(sandboxId: string): Promise<PortForward[]> {
  const data = await request<{ ports: PortForward[] | null }>(`/sandboxes/${sandboxId}/ports`);
  return data.ports ?? [];
}

export async function forwardPort(sandboxId: string, port: number, protocol = 'http'): Promise<PortForward> {
  return request<PortForward>(`/sandboxes/${sandboxId}/ports`, {
    method: 'POST',
    body: JSON.stringify({ port, protocol }),
  });
}

export async function removePort(sandboxId: string, port: number): Promise<void> {
  await request(`/sandboxes/${sandboxId}/ports/${port}`, { method: 'DELETE' });
}

// previewProxyUrl returns the in-Router proxied URL that a user can click to
// reach their forwarded port even without a public Ingress.
export function previewProxyUrl(sandboxId: string, port: number): string {
  return `${API_BASE}/sandboxes/${sandboxId}/preview/${port}/`;
}

// --- Files API ---

export interface FileEntry {
  name: string;
  size: number;
  isDir: boolean;
  mode: string;
  modifiedAt: number;
}

export async function listFiles(sandboxId: string, path = '/workspace'): Promise<{ path: string; entries: FileEntry[] }> {
  const q = new URLSearchParams({ path });
  const data = await request<{ path: string; entries: FileEntry[] | null }>(`/sandboxes/${sandboxId}/files/?${q}`);
  return { path: data.path, entries: data.entries ?? [] };
}

// uploadFile PUTs a single File to the sandbox under parentPath/<filename>.
// We bypass the JSON-centric `request` helper because the router's PUT handler
// reads the raw request body and stores those bytes verbatim — a JSON wrapper
// would corrupt binary uploads.
export async function uploadFile(sandboxId: string, parentPath: string, file: File): Promise<void> {
  const cleaned = parentPath.replace(/\/+$/, '') || '';
  const target = `${cleaned}/${file.name}`.replace(/^\/+/, '');
  const res = await fetch(`${API_BASE}/sandboxes/${sandboxId}/files/${target}`, {
    method: 'PUT',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/octet-stream' },
    body: file,
  });
  if (!res.ok) {
    const body = await res.text().catch(() => res.statusText);
    throw new Error(`${res.status}: ${body}`);
  }
}

// downloadFileUrl returns a URL the browser can GET directly. We rely on the
// Content-Disposition header the router sets so the browser prompts a save.
export function downloadFileUrl(sandboxId: string, fullPath: string): string {
  const stripped = fullPath.replace(/^\/+/, '');
  return `${API_BASE}/sandboxes/${sandboxId}/files/${stripped}`;
}

export interface GovernancePolicy {
  maxSandboxesPerUser?: number;
  maxSandboxesTotal?: number;
  maxCpu?: string;
  maxMemory?: string;
  maxStorage?: string;
  maxTimeout?: string;
  maxIdleTimeout?: string;
  allowedTemplates?: string[];
  approvedRegistries?: string[];
  description?: string;
}

export interface GovernanceNamespacePolicy {
  namespace: string;
  policy: GovernancePolicy;
}

export interface GovernanceBundle {
  cluster: GovernancePolicy | null;
  namespaces: GovernanceNamespacePolicy[];
}

export async function fetchGovernance(): Promise<GovernanceBundle> {
  return request<GovernanceBundle>('/governance/policies');
}

export async function setClusterGovernance(policy: GovernancePolicy): Promise<void> {
  await request('/governance/policies', {
    method: 'PUT',
    body: JSON.stringify(policy),
  });
}

export async function setNamespaceGovernance(namespace: string, policy: GovernancePolicy): Promise<void> {
  await request(`/governance/policies/${encodeURIComponent(namespace)}`, {
    method: 'PUT',
    body: JSON.stringify(policy),
  });
}

export async function deleteNamespaceGovernance(namespace: string): Promise<void> {
  await request(`/governance/policies/${encodeURIComponent(namespace)}`, { method: 'DELETE' });
}

export async function fetchEffectiveGovernance(namespace: string): Promise<{ namespace: string; policy: GovernancePolicy }> {
  return request<{ namespace: string; policy: GovernancePolicy }>(
    `/governance/effective?namespace=${encodeURIComponent(namespace)}`,
  );
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
