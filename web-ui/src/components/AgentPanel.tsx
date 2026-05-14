/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useRef, useState } from 'react';
import {
  cancelAgentInvoke,
  streamAgentConfigure,
  streamAgentInvoke,
  type AgentSSEEvent,
} from '../api/client';

interface Props {
  sandboxId: string;
  /** Panel only renders for Running sandboxes — both endpoints reject otherwise. */
  running: boolean;
}

interface LogLine {
  stream: 'stdout' | 'stderr' | 'meta';
  text: string;
}

// AgentPanel surfaces /configure + /invoke for mode: agent sandboxes. The
// component mirrors the FilesPanel pattern: small focused state, no third-
// party SSE library, deliberately plain CSS.
//
// Three sub-sections — configure, invoke, and a recent-invokes summary
// derived from the in-component history (we don't fetch from /audit/events
// because that's a different aggregation seam and the immediate-feedback UX
// matters more than persistence here).
export default function AgentPanel({ sandboxId, running }: Props) {
  // --- configure state -------------------------------------------------
  const [agentCode, setAgentCode] = useState<string>(
    "# /workspace/agent.py — replace with your own\n" +
    "import sys, json\n" +
    "data = sys.stdin.read()\n" +
    "print(f'echo: {data}')\n",
  );
  const [installCommand, setInstallCommand] = useState<string>('');
  const [entrypoint, setEntrypoint] = useState<string>('python /workspace/agent.py');
  const [configureLogs, setConfigureLogs] = useState<LogLine[]>([]);
  const [configuring, setConfiguring] = useState(false);
  const [configureResult, setConfigureResult] = useState<{
    skipped?: boolean;
    installExitCode?: number;
    installCommandHash?: string;
  } | null>(null);
  const [configureError, setConfigureError] = useState<string | null>(null);

  // --- invoke state ----------------------------------------------------
  const [invokePrompt, setInvokePrompt] = useState<string>('');
  const [invokeLogs, setInvokeLogs] = useState<LogLine[]>([]);
  const [invoking, setInvoking] = useState(false);
  const [activeInvokeId, setActiveInvokeId] = useState<string | null>(null);
  const [invokeError, setInvokeError] = useState<string | null>(null);
  const invokeAbortRef = useRef<AbortController | null>(null);

  // --- recent invokes (in-memory; fresh per page load) ----------------
  type RecentInvoke = {
    id: string;
    startedAt: number;
    durationMs?: number;
    exitCode?: number;
    reason?: string;
  };
  const [recent, setRecent] = useState<RecentInvoke[]>([]);

  // --- handlers --------------------------------------------------------

  const onConfigure = useCallback(async () => {
    if (!running || configuring) return;
    setConfiguring(true);
    setConfigureLogs([]);
    setConfigureResult(null);
    setConfigureError(null);

    const files = agentCode
      ? [{ path: '/workspace/agent.py', content: agentCode }]
      : undefined;
    const installArgs = installCommand.trim() ? installCommand.trim().split(/\s+/) : undefined;
    const entrypointArgs = entrypoint.trim() ? entrypoint.trim().split(/\s+/) : undefined;

    if (!files && !installArgs && !entrypointArgs) {
      setConfigureError('Provide at least one of: agent code, install command, or entrypoint.');
      setConfiguring(false);
      return;
    }

    try {
      for await (const evt of streamAgentConfigure(sandboxId, {
        files,
        installCommand: installArgs,
        entrypoint: entrypointArgs,
      })) {
        if (evt.event === 'log') {
          const stream = (evt.data.stream as string) ?? 'stdout';
          const data = (evt.data.data as string) ?? '';
          setConfigureLogs(prev => [
            ...prev,
            { stream: stream === 'stderr' ? 'stderr' : 'stdout', text: data },
          ]);
        } else if (evt.event === 'result') {
          setConfigureResult({
            skipped: Boolean(evt.data.skipped),
            installExitCode: typeof evt.data.installExitCode === 'number' ? evt.data.installExitCode : 0,
            installCommandHash: (evt.data.installCommandHash as string) ?? undefined,
          });
        } else if (evt.event === 'error') {
          setConfigureError(`${evt.data.phase ?? 'configure'}: ${evt.data.message ?? 'unknown error'}`);
        }
      }
    } catch (e: unknown) {
      setConfigureError(e instanceof Error ? e.message : 'Configure failed');
    } finally {
      setConfiguring(false);
    }
  }, [running, configuring, sandboxId, agentCode, installCommand, entrypoint]);

  const onInvoke = useCallback(async () => {
    if (!running || invoking) return;
    setInvoking(true);
    setInvokeLogs([]);
    setActiveInvokeId(null);
    setInvokeError(null);

    const ctrl = new AbortController();
    invokeAbortRef.current = ctrl;
    const startedAt = Date.now();

    try {
      // streamAgentInvoke uses fetch under the hood; closing the iterator
      // (e.g. via the cancel button calling controller.abort()) tears down
      // the stream. We capture the invokeId from the first event so the
      // cancel handler can call /invoke/cancel.
      for await (const evt of withSignal(streamAgentInvoke(sandboxId, {
        prompt: invokePrompt || undefined,
      }), ctrl.signal)) {
        if (evt.event === 'start') {
          const id = (evt.data.invokeId as string) ?? '';
          setActiveInvokeId(id);
          setInvokeLogs(prev => [...prev, { stream: 'meta', text: `started: ${id}` }]);
        } else if (evt.event === 'log') {
          const stream = (evt.data.stream as string) ?? 'stdout';
          const data = (evt.data.data as string) ?? '';
          setInvokeLogs(prev => [
            ...prev,
            { stream: stream === 'stderr' ? 'stderr' : 'stdout', text: data },
          ]);
        } else if (evt.event === 'exit') {
          const exitCode = typeof evt.data.exitCode === 'number' ? evt.data.exitCode : -1;
          const durationMs = typeof evt.data.durationMs === 'number' ? evt.data.durationMs : Date.now() - startedAt;
          const reason = (evt.data.reason as string) ?? 'completed';
          setInvokeLogs(prev => [
            ...prev,
            { stream: 'meta', text: `exit ${exitCode} (${reason}, ${durationMs}ms)` },
          ]);
          setRecent(prev => [
            {
              id: ((evt.data.invokeId as string) ?? activeInvokeId ?? `inv-${startedAt}`),
              startedAt,
              durationMs,
              exitCode,
              reason,
            },
            ...prev,
          ].slice(0, 5));
        } else if (evt.event === 'error') {
          setInvokeError((evt.data.message as string) ?? 'invoke error');
        }
      }
    } catch (e: unknown) {
      // AbortError surfaces here when the user clicks Cancel locally.
      if (!(e instanceof Error) || e.name !== 'AbortError') {
        setInvokeError(e instanceof Error ? e.message : 'Invoke failed');
      }
    } finally {
      setInvoking(false);
      invokeAbortRef.current = null;
    }
  }, [running, invoking, sandboxId, invokePrompt, activeInvokeId]);

  const onCancel = useCallback(async () => {
    // Two-step cancel: tell the Router via /invoke/cancel (best-effort,
    // races against in-flight completion), then abort the local fetch so
    // the SSE iterator unwinds. The Router treats either signal as
    // sufficient; both is the belt-and-braces version.
    const id = activeInvokeId;
    if (id) {
      try {
        await cancelAgentInvoke(sandboxId, id);
      } catch {
        // 404 just means the invoke already completed — fine to swallow.
      }
    }
    invokeAbortRef.current?.abort();
  }, [sandboxId, activeInvokeId]);

  // --- render ----------------------------------------------------------

  if (!running) {
    return null;
  }

  return (
    <div data-testid="agent-panel" style={{
      border: '1px solid #e5e4e7', borderRadius: '8px', padding: '12px',
      background: '#fdfcfe',
    }}>
      <div style={{ fontSize: '12px', fontWeight: 600, marginBottom: '10px', color: '#4b4657' }}>
        🤖 Agent
      </div>

      {/* Configure */}
      <Section title="Configure">
        <label style={lblStyle}>Agent code (uploads to /workspace/agent.py)</label>
        <textarea
          value={agentCode}
          onChange={e => setAgentCode(e.target.value)}
          rows={6}
          style={txtAreaStyle}
        />
        <label style={lblStyle}>Install command (optional)</label>
        <input
          type="text"
          value={installCommand}
          onChange={e => setInstallCommand(e.target.value)}
          placeholder='e.g. "pip install requests"'
          style={inputStyle}
        />
        <label style={lblStyle}>Entrypoint</label>
        <input
          type="text"
          value={entrypoint}
          onChange={e => setEntrypoint(e.target.value)}
          placeholder="python /workspace/agent.py"
          style={inputStyle}
        />
        <button
          onClick={onConfigure}
          disabled={configuring}
          data-testid="btn-configure"
          style={btnStyle(!configuring, '#aa3bff')}
        >
          {configuring ? 'Configuring…' : 'Configure'}
        </button>
        {configureError && <p style={errStyle}>{configureError}</p>}
        {configureLogs.length > 0 && (
          <LogViewer lines={configureLogs} testId="configure-logs" />
        )}
        {configureResult && (
          <p style={{ fontSize: '11px', color: '#4b4657', marginTop: '6px' }}>
            {configureResult.skipped ? '✓ skipped (no changes)' : `✓ install exit ${configureResult.installExitCode}`}
            {configureResult.installCommandHash && (
              <> · <code style={{ fontSize: '10px' }}>{configureResult.installCommandHash.slice(0, 12)}</code></>
            )}
          </p>
        )}
      </Section>

      {/* Invoke */}
      <Section title="Invoke">
        <label style={lblStyle}>Prompt (sent to entrypoint stdin)</label>
        <textarea
          value={invokePrompt}
          onChange={e => setInvokePrompt(e.target.value)}
          rows={3}
          placeholder="hello world"
          style={txtAreaStyle}
        />
        <div style={{ display: 'flex', gap: '8px' }}>
          <button
            onClick={onInvoke}
            disabled={invoking}
            data-testid="btn-invoke"
            style={btnStyle(!invoking, '#22c55e')}
          >
            {invoking ? 'Running…' : 'Invoke'}
          </button>
          {invoking && (
            <button
              onClick={onCancel}
              data-testid="btn-cancel-invoke"
              style={btnStyle(true, '#ef4444')}
            >
              Cancel
            </button>
          )}
        </div>
        {invokeError && <p style={errStyle}>{invokeError}</p>}
        {invokeLogs.length > 0 && <LogViewer lines={invokeLogs} testId="invoke-logs" />}
      </Section>

      {/* Recent invokes */}
      {recent.length > 0 && (
        <Section title="Recent invokes (this session)">
          <ul style={{ listStyle: 'none', padding: 0, margin: 0, fontSize: '12px' }}>
            {recent.map(r => (
              <li key={r.id} style={{
                display: 'flex', justifyContent: 'space-between', padding: '4px 0',
                borderBottom: '1px solid #f3f4f6', color: '#4b4657',
              }}>
                <code style={{ fontSize: '11px' }}>{r.id.slice(0, 16)}</code>
                <span>
                  {r.exitCode !== undefined && (
                    <span style={{ color: r.exitCode === 0 ? '#22c55e' : '#ef4444' }}>
                      exit {r.exitCode}
                    </span>
                  )}
                  {' · '}
                  <span>{r.durationMs ?? '?'}ms</span>
                </span>
              </li>
            ))}
          </ul>
        </Section>
      )}
    </div>
  );
}

// --- small inline components / styles ---------------------------------

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '12px' }}>
      <div style={{ fontSize: '11px', textTransform: 'uppercase', color: '#6b6375', marginBottom: '6px' }}>{title}</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>{children}</div>
    </div>
  );
}

function LogViewer({ lines, testId }: { lines: LogLine[]; testId: string }) {
  return (
    <pre data-testid={testId} style={{
      maxHeight: '180px', overflowY: 'auto',
      fontSize: '11px', fontFamily: 'monospace',
      background: '#0c0a14', color: '#e5e7eb',
      padding: '8px', borderRadius: '4px',
      margin: '4px 0', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
    }}>
      {lines.map((l, i) => (
        <div key={i} style={{
          color: l.stream === 'stderr' ? '#fca5a5' : l.stream === 'meta' ? '#a78bfa' : '#e5e7eb',
        }}>
          {l.text}
        </div>
      ))}
    </pre>
  );
}

const lblStyle: React.CSSProperties = { fontSize: '11px', color: '#6b6375', fontWeight: 500 };
const inputStyle: React.CSSProperties = {
  padding: '6px 8px', borderRadius: '4px', border: '1px solid #d4d0e0',
  fontSize: '12px', fontFamily: 'monospace',
};
const txtAreaStyle: React.CSSProperties = { ...inputStyle, resize: 'vertical' };
const errStyle: React.CSSProperties = { color: '#ef4444', fontSize: '12px', margin: '4px 0' };

function btnStyle(enabled: boolean, color: string): React.CSSProperties {
  return {
    padding: '6px 14px', borderRadius: '4px',
    border: `1px solid ${enabled ? color + '60' : '#d1d5db'}`,
    background: enabled ? color + '14' : '#f3f4f6',
    color: enabled ? color : '#9ca3af',
    fontSize: '12px', fontWeight: 500,
    cursor: enabled ? 'pointer' : 'not-allowed',
    opacity: enabled ? 1 : 0.6, alignSelf: 'flex-start',
  };
}

// withSignal wraps an AsyncIterable so an external AbortSignal can break
// the loop. Used so the Cancel button can interrupt the SSE consumer.
async function* withSignal<T>(it: AsyncIterable<T>, signal: AbortSignal): AsyncIterable<T> {
  if (signal.aborted) {
    throw new DOMException('Aborted', 'AbortError');
  }
  for await (const value of it) {
    if (signal.aborted) {
      throw new DOMException('Aborted', 'AbortError');
    }
    yield value;
  }
}
