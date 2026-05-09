/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useParams, Link } from 'react-router-dom';
import { useEffect, useRef, useState, useCallback } from 'react';
import { Terminal as XTerm } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import 'xterm/css/xterm.css';

// WebSocket URL — auto-detected from current origin in production.
// In dev, override via VITE_WS_BASE_URL.
const WS_BASE = import.meta.env.VITE_WS_BASE_URL || (() => {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}`;
})();

const MAX_RECONNECT_ATTEMPTS = 5;
const RECONNECT_INTERVAL_MS = 3000;

export default function Terminal() {
  const { id } = useParams<{ id: string }>();
  const terminalRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<XTerm | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const [connectionState, setConnectionState] = useState<'connecting' | 'connected' | 'reconnecting' | 'lost'>('connecting');

  const connectWebSocket = useCallback(() => {
    if (!id) return;
    if (wsRef.current) {
      wsRef.current.onclose = null;
      wsRef.current.close();
    }

    const ws = new WebSocket(`${WS_BASE}/ws/terminal/${id}`);
    wsRef.current = ws;

    ws.onopen = () => {
      reconnectAttemptsRef.current = 0;
      setConnectionState('connected');
      xtermRef.current?.write('\x1b[32mConnected.\x1b[0m\r\n');
      // Send initial resize
      if (xtermRef.current) {
        ws.send(JSON.stringify({ type: 'resize', cols: xtermRef.current.cols, rows: xtermRef.current.rows }));
      }
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === 'output') {
          xtermRef.current?.write(msg.data);
        } else if (msg.type === 'error') {
          xtermRef.current?.write(`\r\n\x1b[31mError: ${msg.message}\x1b[0m\r\n`);
        } else if (msg.type === 'close') {
          xtermRef.current?.write(`\r\n\x1b[33m[Session closed: ${msg.reason}]\x1b[0m\r\n`);
          setConnectionState('lost');
        }
      } catch {
        xtermRef.current?.write(event.data);
      }
    };

    ws.onclose = (event) => {
      if (event.code === 4004 || event.code === 4009) {
        setConnectionState('lost');
        return;
      }
      if (reconnectAttemptsRef.current < MAX_RECONNECT_ATTEMPTS) {
        setConnectionState('reconnecting');
        reconnectTimerRef.current = setTimeout(() => {
          reconnectAttemptsRef.current += 1;
          connectWebSocket();
        }, RECONNECT_INTERVAL_MS);
      } else {
        setConnectionState('lost');
      }
    };
    ws.onerror = () => {};
  }, [id]);

  const handleRetry = useCallback(() => {
    reconnectAttemptsRef.current = 0;
    setConnectionState('connecting');
    connectWebSocket();
  }, [connectWebSocket]);

  useEffect(() => {
    if (!terminalRef.current || !id) return;

    const term = new XTerm({
      theme: { background: '#1a1a2e', foreground: '#e0e0e0', cursor: '#aa3bff' },
      fontFamily: '"Fira Code", "Cascadia Code", "JetBrains Mono", monospace',
      fontSize: 14,
      cursorBlink: true,
    });
    xtermRef.current = term;
    const fitAddon = new FitAddon();
    fitAddonRef.current = fitAddon;
    term.loadAddon(fitAddon);
    term.open(terminalRef.current);
    fitAddon.fit();

    term.onData((data) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) return;
      ws.send(JSON.stringify({ type: 'input', data }));
    });

    const handleResize = () => {
      fitAddon.fit();
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
      }
    };
    window.addEventListener('resize', handleResize);
    connectWebSocket();

    return () => {
      window.removeEventListener('resize', handleResize);
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
      if (wsRef.current) {
        wsRef.current.onclose = null;
        wsRef.current.close();
        wsRef.current = null;
      }
      term.dispose();
      xtermRef.current = null;
    };
  }, [connectWebSocket, id]);

  return (
    <div data-testid="terminal-page" style={{
      display: 'flex', flexDirection: 'column', height: '100vh', width: '100vw',
      overflow: 'hidden', background: '#1a1a2e',
    }}>
      <div data-testid="terminal-toolbar" style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '8px 16px', background: '#16162a', borderBottom: '1px solid #2a2a4a', flexShrink: 0,
      }}>
        <Link to="/" data-testid="back-button"
          style={{ color: '#aa3bff', textDecoration: 'none', fontSize: '14px' }}>
          ← Dashboard
        </Link>
        <span data-testid="sandbox-name" style={{ color: '#e0e0e0', fontSize: '14px', fontWeight: 500 }}>
          Sandbox: {id}
        </span>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span style={{
            width: 8, height: 8, borderRadius: '50%',
            background: connectionState === 'connected' ? '#22c55e' : connectionState === 'reconnecting' ? '#eab308' : '#ef4444',
          }} />
          <span style={{ color: '#9ca3af', fontSize: '12px' }}>{connectionState}</span>
        </div>
      </div>

      {connectionState === 'reconnecting' && (
        <div data-testid="reconnecting-banner" style={{
          padding: '6px 16px', background: '#eab308', color: '#000',
          textAlign: 'center', fontSize: '13px', flexShrink: 0,
        }}>Reconnecting...</div>
      )}

      {connectionState === 'lost' && (
        <div data-testid="connection-lost-banner" style={{
          padding: '6px 16px', background: '#ef4444', color: '#fff',
          textAlign: 'center', fontSize: '13px', flexShrink: 0,
        }}>
          Connection lost.{' '}
          <button data-testid="retry-button" onClick={handleRetry}
            style={{ background: '#fff', color: '#ef4444', border: 'none',
              borderRadius: '4px', padding: '2px 10px', cursor: 'pointer', fontWeight: 600 }}>
            Retry
          </button>
        </div>
      )}

      <div ref={terminalRef} data-testid="terminal-container" style={{ flex: 1, overflow: 'hidden' }} />
    </div>
  );
}
