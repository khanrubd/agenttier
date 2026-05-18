/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

export interface Sandbox {
  id: string;
  name: string;
  status: 'creating' | 'running' | 'stopped' | 'error' | 'deleting';
  template: string;
  // Mode is "code" (interactive — humans drive via terminal) or "agent"
  // (configured entrypoint invoked over /configure + /invoke). Defaults
  // to "code" when the backend response omits the field, so older
  // clusters running pre-mode-aware sandboxes still render sensibly.
  mode: 'code' | 'agent';
  // Kubernetes namespace the sandbox lives in. Surfaced in the UI for
  // future multi-tenancy (per-namespace governance + RBAC).
  namespace: string;
  error_message: string | null;
  created_at: string;
  last_accessed_at: string | null;
  created_by: string;
  created_by_email: string;
}

export type SandboxStatus = Sandbox['status'];

export interface Template {
  name: string;
  description: string;
  image: string;
  resourceVersion?: string;
  spec?: TemplateSpec;
}

export interface TemplateSpec {
  description?: string;
  mode?: 'code' | 'agent';
  image?: { repository: string; pullPolicy?: string; pullSecret?: string };
  resources?: { requests?: Record<string, string>; limits?: Record<string, string> };
  storage?: { size?: string; storageClass?: string; mountPath?: string };
  network?: { allowInternet?: boolean; egressRules?: any[]; ingressRules?: any[]; allowedDomains?: string[] };
  env?: { name: string; value: string }[];
  timeout?: string;
  idleTimeout?: string;
  runtimeClass?: string;
  harness?: {
    command?: string[];
    args?: string[];
    workingDir?: string;
    shell?: string;
    tools?: { name: string; version?: string; installCommand?: string; verifyCommand?: string }[];
    systemPrompt?: { content?: string; path?: string };
    hooks?: { onStart?: string; onStop?: string; onIdle?: string; onResume?: string };
    constraints?: Record<string, any>;
  };
  initScripts?: string[];
  files?: { path: string; content?: string; mode?: number }[];
  credentials?: { secretName: string; mountAs?: string; mountPath?: string; envPrefix?: string }[];
  security?: { privileged?: boolean };
}

export interface ActivityEntry {
  timestamp: string;
  user_email: string;
  action: string;
  sandbox_id: string;
  sandbox_name: string;
  details: string;
}

export interface Analytics {
  total_sandboxes: number;
  status_breakdown: Record<string, number>;
  template_breakdown: Record<string, number>;
  daily_created: { date: string; count: number }[];
  unique_users: number;
  top_users: { user: string; count: number }[];
  error_rate: number;
}

export interface CostEstimate {
  total_estimated_monthly: number;
  running_sandboxes: number;
  stopped_sandboxes: number;
  per_template: { template: string; total: number; count: number }[];
}
