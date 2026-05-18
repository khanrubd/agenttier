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

// fetchSandbox returns a single sandbox by ID. Used by the per-sandbox
// settings page; the list endpoint already returns enough fields, so
// this just delegates to GET /sandboxes/{id} and runs the same mapper.
export async function fetchSandbox(id: string): Promise<Sandbox> {
  const raw = await request<any>(`/sandboxes/${encodeURIComponent(id)}`);
  return mapSandbox(raw);
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

// PoolConfig is one entry in the per-template warm pool. Multiple entries
// run independently — adding or removing one never disturbs the others.
export interface PoolConfig {
  template: string;
  desiredCount: number;
}

// PoolStatus is the live state of one per-template pool. Mirrors the
// PoolConfig shape with extra observed-state fields.
export interface PoolStatus {
  template: string;
  desiredCount: number;
  readyCount: number;
  pendingCount: number;
}

// WarmPoolStatus carries both the per-template `pools` array (the new
// canonical shape) and the legacy single-template flat fields. The Web
// UI uses `pools` as the source of truth; the legacy fields are only
// populated when there's exactly one entry.
export interface WarmPoolStatus {
  pools?: PoolStatus[];
  desiredCount: number;
  readyCount: number;
  pendingCount: number;
  template: string;
}

export async function fetchWarmPoolStatus(): Promise<WarmPoolStatus> {
  return request<WarmPoolStatus>('/warmpool/status');
}

// Legacy single-template setter, retained for back-compat with any code
// path that hasn't migrated yet. Internally calls setWarmPoolPools with
// a one-entry array — keeps the wire format normalized.
export async function setWarmPoolConfig(desiredCount: number, template: string): Promise<void> {
  await setWarmPoolPools(desiredCount > 0 ? [{ template, desiredCount }] : []);
}

// setWarmPoolPools is the canonical setter. Takes the full target list
// of per-template pools; the controller reconciles by adding entries
// missing from the cluster, scaling existing ones, and dropping any
// pools whose templates aren't in the array.
export async function setWarmPoolPools(pools: PoolConfig[]): Promise<void> {
  await request('/warmpool/config', {
    method: 'PUT',
    body: JSON.stringify({ pools }),
  });
}

// --- Cluster Status API ---
//
// Used by the Web UI's left-nav glance widget to show node + pod counts
// alongside the warm pool status. Updates every few seconds.
export interface ClusterStatus {
  nodes: number;
  nodesReady: number;
  pods: number;
  sandboxPods: number;
  headroomReady: number;
  autoscalerEnabled: boolean;
}

export async function fetchClusterStatus(): Promise<ClusterStatus> {
  return request<ClusterStatus>('/cluster/status');
}

// --- Headroom API ---
//
// Read/write the chart's optional spare-node pause-Pod Deployment so
// operators can resize headroom from the Web UI. Admin-gated on the
// server side for write; read works for any authenticated user.
export interface HeadroomConfig {
  enabled: boolean;
  replicas: number;
  cpu: string;
  memory: string;
  readyReplicas?: number;
}

export async function fetchHeadroomConfig(): Promise<HeadroomConfig> {
  return request<HeadroomConfig>('/cluster/headroom');
}

export async function setHeadroomConfig(cfg: { replicas: number; cpu?: string; memory?: string }): Promise<HeadroomConfig> {
  return request<HeadroomConfig>('/cluster/headroom', {
    method: 'PUT',
    body: JSON.stringify(cfg),
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

// archiveUrl returns a URL the browser can GET to receive a streamed .zip
// of the directory rooted at `path` (default /workspace). The Router enforces
// that the path lives under /workspace; anything else returns 400.
export function archiveUrl(sandboxId: string, path = '/workspace'): string {
  const q = new URLSearchParams({ path });
  return `${API_BASE}/sandboxes/${sandboxId}/archive?${q}`;
}

// --- Agent mode (Phase 10) ---

// SSE event payload as the Router emits it. We surface the raw event name +
// JSON body so consumers can pattern-match without baking schema knowledge
// into the transport layer.
export interface AgentSSEEvent {
  event: string;
  data: Record<string, unknown>;
}

export interface AgentConfigureRequest {
  files?: { path: string; content?: string; contentBase64?: string }[];
  installCommand?: string[];
  entrypoint?: string[];
}

// streamAgentConfigure POSTs to /configure and yields each SSE event as it
// arrives. The fetch + ReadableStream + TextDecoder pattern is enough for
// the chunked SSE wire format — we deliberately don't pull in a third-party
// SSE library here. Closing the iterator cancels the underlying fetch which
// the Router treats as a client cancel.
export async function* streamAgentConfigure(
  sandboxId: string,
  body: AgentConfigureRequest,
): AsyncIterable<AgentSSEEvent> {
  const res = await fetch(`${API_BASE}/sandboxes/${sandboxId}/configure`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const errBody = await res.text().catch(() => res.statusText);
    throw new Error(`${res.status}: ${errBody}`);
  }
  yield* readSSE(res);
}

export interface AgentInvokeRequest {
  // Either a JSON body, a string, or null. The CLI / SDK accept bytes too,
  // but the Web UI invoke flow is text-first; users with binary payloads
  // should use the SDK.
  payload?: Record<string, unknown> | string | null;
  prompt?: string;
  // Per-invoke server-side timeout (e.g. "5m"). When unset the Router uses
  // the template's defaultInvokeTimeout (or 30 minutes when that's also unset).
  invokeTimeout?: string;
}

// streamAgentInvoke POSTs to /invoke and yields SSE events live. Returns the
// AsyncIterable so the Agent panel can render output as it arrives instead
// of waiting for the exit event.
export async function* streamAgentInvoke(
  sandboxId: string,
  req: AgentInvokeRequest = {},
): AsyncIterable<AgentSSEEvent> {
  const params = new URLSearchParams();
  if (req.prompt !== undefined) params.set('prompt', req.prompt);
  if (req.invokeTimeout !== undefined) params.set('timeout', req.invokeTimeout);
  const qs = params.toString();
  const url = `${API_BASE}/sandboxes/${sandboxId}/invoke${qs ? `?${qs}` : ''}`;

  let body: BodyInit | null = null;
  let contentType = 'application/octet-stream';
  if (typeof req.payload === 'string') {
    body = req.payload;
    contentType = 'text/plain; charset=utf-8';
  } else if (req.payload && typeof req.payload === 'object') {
    body = JSON.stringify(req.payload);
    contentType = 'application/json';
  }

  const res = await fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': contentType, 'Accept': 'text/event-stream' },
    body,
  });
  if (!res.ok) {
    const errBody = await res.text().catch(() => res.statusText);
    throw new Error(`${res.status}: ${errBody}`);
  }
  yield* readSSE(res);
}

export async function cancelAgentInvoke(sandboxId: string, invokeId: string): Promise<void> {
  await request(`/sandboxes/${sandboxId}/invoke/cancel`, {
    method: 'POST',
    body: JSON.stringify({ invokeId }),
  });
}

// readSSE consumes a Response body as Server-Sent Events. Yields one
// AgentSSEEvent per `\n\n`-delimited record. Comment lines (`:`-prefixed)
// are skipped so the Router's keepalive pings don't show up as events.
async function* readSSE(res: Response): AsyncIterable<AgentSSEEvent> {
  if (!res.body) return;
  const reader = res.body.getReader();
  const decoder = new TextDecoder('utf-8');
  let buf = '';
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });

      // Split on the SSE record terminator. We support both `\n\n` and
      // `\r\n\r\n` so behavior is identical regardless of the proxy.
      let idx;
      while ((idx = findRecordEnd(buf)) !== -1) {
        const record = buf.slice(0, idx);
        buf = buf.slice(idx + (buf[idx + 1] === '\n' ? 2 : 4));
        const evt = parseSSERecord(record);
        if (evt) yield evt;
      }
    }
    if (buf.trim()) {
      const evt = parseSSERecord(buf);
      if (evt) yield evt;
    }
  } finally {
    reader.releaseLock();
  }
}

function findRecordEnd(s: string): number {
  // Returns the index of the first character of the terminator, or -1.
  // Handles both `\n\n` and `\r\n\r\n`.
  for (let i = 0; i < s.length - 1; i++) {
    if (s[i] === '\n' && s[i + 1] === '\n') return i;
    if (i < s.length - 3 && s[i] === '\r' && s[i + 1] === '\n' && s[i + 2] === '\r' && s[i + 3] === '\n') return i;
  }
  return -1;
}

function parseSSERecord(record: string): AgentSSEEvent | null {
  let event = 'message';
  const dataLines: string[] = [];
  for (const raw of record.split(/\r?\n/)) {
    if (!raw || raw.startsWith(':')) continue;
    if (raw.startsWith('event:')) {
      event = raw.slice(6).trim();
    } else if (raw.startsWith('data:')) {
      dataLines.push(raw.slice(5).replace(/^ /, ''));
    }
  }
  if (dataLines.length === 0) return null;
  const joined = dataLines.join('\n');
  let data: Record<string, unknown>;
  try {
    const parsed = JSON.parse(joined);
    data = parsed && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : { data: parsed };
  } catch {
    data = { data: joined };
  }
  return { event, data };
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
  // Mode + namespace fall back to sensible defaults when the backend
  // response omits them. Older clusters that haven't updated to the
  // mode-aware sandboxToJSON still render "code" / "default" instead of
  // undefined — the UI treats those as "no mode badge" and "implicit
  // namespace" respectively.
  const rawMode = (raw.mode || 'code').toLowerCase();
  const mode = rawMode === 'agent' ? 'agent' : 'code';
  return {
    id: raw.sandboxId || raw.id || raw.name,
    name: raw.name || raw.sandboxId,
    status: (raw.status || 'creating').toLowerCase() as Sandbox['status'],
    template: raw.templateRef || raw.template || '',
    mode,
    namespace: raw.namespace || 'default',
    error_message: raw.message || null,
    created_at: raw.createdAt || raw.created_at || '',
    last_accessed_at: raw.lastActivityAt || raw.last_accessed_at || null,
    created_by: raw.createdBy?.displayName || raw.created_by || '',
    created_by_email: raw.createdBy?.email || raw.created_by_email || '',
  };
}
