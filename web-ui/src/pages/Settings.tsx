/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useState } from 'react';
import { fetchCurrentUser } from '../api/client';
import GovernanceEditor from '../components/GovernanceEditor';
import WarmPoolEditor from '../components/WarmPoolEditor';
import HeadroomEditor from '../components/HeadroomEditor';

// Settings is the cluster-wide admin page. Per-sandbox settings live at
// /sandbox/:id/settings; this page is for cluster-scoped configuration:
// governance policies, warm pool sizing across templates, and the
// optional headroom (spare-node) Deployment.
//
// All three editors are extracted into their own components so the page
// stays a thin composition. Adding a new section is one import + one
// <Section> wrapper.

export default function Settings() {
  const [isAdmin, setIsAdmin] = useState(false);

  useEffect(() => {
    fetchCurrentUser()
      .then((u) => setIsAdmin(Boolean(u.isAdmin)))
      .catch(() => setIsAdmin(false));
  }, []);

  return (
    <div style={{ padding: '32px', maxWidth: '880px' }}>
      <h1 style={{ fontSize: '22px', fontWeight: 700, color: '#08060d', marginBottom: '24px' }}>Settings</h1>

      <GovernanceEditor isAdmin={isAdmin} />

      <Section title="Warm pool" testId="section-warm-pool">
        <WarmPoolEditor />
      </Section>

      <Section title="Cluster autoscaling — headroom" testId="section-headroom">
        <HeadroomEditor />
      </Section>
    </div>
  );
}

function Section({ title, children, testId }: { title: string; children: React.ReactNode; testId?: string }) {
  return (
    <section
      data-testid={testId}
      style={{
        marginBottom: '32px',
      }}
    >
      <h2 style={{ fontSize: '16px', fontWeight: 600, color: '#08060d', marginBottom: '12px' }}>{title}</h2>
      {children}
    </section>
  );
}
