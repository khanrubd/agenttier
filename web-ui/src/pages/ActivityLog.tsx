/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, useEffect } from 'react';
import { fetchActivity } from '../api/client';
import type { ActivityEntry } from '../types';

export default function ActivityLog() {
  const [events, setEvents] = useState<ActivityEntry[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchActivity().then(setEvents).catch(() => {}).finally(() => setLoading(false));
  }, []);

  return (
    <div style={{ padding: '24px 32px' }}>
      <h1 style={{ fontSize: '28px', margin: '0 0 24px', letterSpacing: '-0.5px' }}>Activity Log</h1>

      {loading && <div style={{ color: '#6b6375', padding: '40px 0', textAlign: 'center' }}>Loading...</div>}

      {!loading && events.length === 0 && (
        <div style={{ color: '#6b6375', padding: '40px 0', textAlign: 'center' }}>No activity yet.</div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
        {events.map((event, i) => (
          <div key={i} style={{
            display: 'grid', gridTemplateColumns: '180px 140px 1fr 180px',
            gap: '12px', padding: '10px 12px', borderRadius: '6px', fontSize: '13px',
            alignItems: 'center', background: i % 2 === 0 ? '#fafafa' : 'transparent',
          }}>
            <span style={{ color: '#6b6375', fontFamily: 'monospace', fontSize: '12px' }}>
              {new Date(event.timestamp).toLocaleString()}
            </span>
            <span style={{ fontWeight: 500, textTransform: 'capitalize' }}>
              {event.action.replace('.', ' → ').replace(/_/g, ' ')}
            </span>
            <span style={{ color: '#aa3bff' }}>{event.sandbox_name || event.sandbox_id}</span>
            <span style={{ color: '#6b6375', textAlign: 'right' }}>{event.user_email}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
