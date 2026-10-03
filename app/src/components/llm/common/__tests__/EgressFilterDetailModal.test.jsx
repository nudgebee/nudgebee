// Regression tests for the egressfilter detail modal aggregators. Pure
// data functions — verified without rendering the modal itself. The
// modal render path is exercised by the ResponseMetaRail integration
// test (chip click → modal opens) in ResponseMetaRail.test.jsx.

import React from 'react';
import { render, screen } from '@testing-library/react';
import {
  __aggregateSecretsForTest as aggregateSecrets,
  __aggregatePiiForTest as aggregatePii,
  __DetectedValuesForTest as DetectedValues,
  __isSafeHttpUrlForTest as isSafeHttpUrl,
} from '@components/llm/common/EgressFilterDetailModal';

describe('aggregateSecrets', () => {
  it('rolls up rule / source / agent counts from hits[] per event', () => {
    const events = [
      {
        detector: 'secrets',
        audit_id: 'egress-a',
        agent_name: 'k8s_orchestrator_lean',
        hit_count: 3,
        hits: [
          { rule_id: 'aws-access-key-id', source: 'user' },
          { rule_id: 'high-entropy-blob', source: 'user' },
          { rule_id: 'high-entropy-blob', source: 'user' },
        ],
      },
      {
        detector: 'secrets',
        audit_id: 'egress-b',
        agent_name: 'postgres',
        hit_count: 2,
        hits: [
          { rule_id: 'high-entropy-blob', source: 'system' },
          { rule_id: 'high-entropy-blob', source: 'system' },
        ],
      },
    ];
    const agg = aggregateSecrets(events);
    expect(agg.totalHits).toBe(5);
    expect(agg.rules).toEqual({ 'aws-access-key-id': 1, 'high-entropy-blob': 4 });
    expect(agg.sources).toEqual({ user: 3, system: 2 });
    expect(agg.agents).toEqual({ k8s_orchestrator_lean: 3, postgres: 2 });
    expect(agg.auditIds).toEqual(['egress-a', 'egress-b']);
  });

  it('agent count uses hits.length not hit_count when both present (Gemini review)', () => {
    // If backend caps/truncates hits[] but leaves hit_count reflecting
    // the pre-cap total, the "sum by axis = total" invariant breaks
    // unless we base agent tallies on the actual hits[] we see.
    const events = [
      {
        detector: 'secrets',
        audit_id: 'egress-cap',
        agent_name: 'agent_a',
        hit_count: 999, // stale / pre-cap
        hits: [
          { rule_id: 'high-entropy-blob', source: 'user' },
          { rule_id: 'high-entropy-blob', source: 'user' },
        ],
      },
    ];
    const agg = aggregateSecrets(events);
    expect(agg.totalHits).toBe(2);
    expect(agg.agents).toEqual({ agent_a: 2 }); // NOT 999 — matches sum(rules) + sum(sources)
  });

  it('falls back to rule_ids + hit_sources when hits[] is absent (legacy rows)', () => {
    const events = [
      {
        detector: 'secrets',
        audit_id: 'egress-legacy',
        agent_name: 'k8s_orchestrator_lean',
        hit_count: 4,
        rule_ids: ['aws-access-key-id', 'high-entropy-blob'],
        hit_sources: ['user'],
      },
    ];
    const agg = aggregateSecrets(events);
    expect(agg.totalHits).toBe(4);
    expect(agg.rules).toEqual({ 'aws-access-key-id': 1, 'high-entropy-blob': 1 });
    expect(agg.sources).toEqual({ user: 1 });
    expect(agg.agents).toEqual({ k8s_orchestrator_lean: 4 });
  });
});

describe('aggregatePii', () => {
  it('reads category_counts + agent_counts directly (post-enrichment events)', () => {
    const events = [
      {
        detector: 'pii',
        audit_id: 'scrub-a',
        hit_count: 51,
        categories: ['EMAIL', 'LOCATION', 'PERSON'],
        category_counts: { EMAIL: 3, LOCATION: 12, PERSON: 36 },
        agent_counts: { k8s_orchestrator_lean: 20, memory_compose: 31 },
        agent_name: 'k8s_orchestrator_lean,memory_compose',
      },
    ];
    const agg = aggregatePii(events);
    expect(agg.totalHits).toBe(51);
    expect(agg.categories).toEqual({ EMAIL: 3, LOCATION: 12, PERSON: 36 });
    expect(agg.agents).toEqual({ k8s_orchestrator_lean: 20, memory_compose: 31 });
    expect(agg.auditIds).toEqual(['scrub-a']);
  });

  it('falls back to categories + agent_name string when new fields are absent (legacy rows)', () => {
    const events = [
      {
        detector: 'pii',
        audit_id: 'scrub-legacy',
        hit_count: 5,
        categories: ['EMAIL', 'PERSON'],
        agent_name: 'agent_a,agent_b',
      },
    ];
    const agg = aggregatePii(events);
    expect(agg.totalHits).toBe(5);
    // No per-count data → 1-each placeholder so at least the categories/agents
    // still appear in the modal (the totalHits at the top is authoritative).
    expect(agg.categories).toEqual({ EMAIL: 1, PERSON: 1 });
    expect(agg.agents).toEqual({ agent_a: 0, agent_b: 0 });
  });

  it('sums across multiple events (dedup by key across events, not values)', () => {
    const events = [
      {
        detector: 'pii',
        audit_id: 'scrub-a',
        hit_count: 3,
        category_counts: { EMAIL: 2, PERSON: 1 },
        agent_counts: { A: 3 },
      },
      {
        detector: 'pii',
        audit_id: 'scrub-b',
        hit_count: 2,
        category_counts: { EMAIL: 1, LOCATION: 1 },
        agent_counts: { B: 2 },
      },
    ];
    const agg = aggregatePii(events);
    expect(agg.totalHits).toBe(5);
    expect(agg.categories).toEqual({ EMAIL: 3, PERSON: 1, LOCATION: 1 });
    expect(agg.agents).toEqual({ A: 3, B: 2 });
  });

  it('handles empty / missing input gracefully', () => {
    const agg = aggregatePii([]);
    expect(agg.totalHits).toBe(0);
    expect(agg.categories).toEqual({});
    expect(agg.agents).toEqual({});
    expect(agg.auditIds).toEqual([]);
  });
});

describe('per-value detail collection', () => {
  it('collects annotated hits for secrets and ignores un-annotated legacy hits', () => {
    const agg = aggregateSecrets([
      {
        detector: 'secrets',
        audit_id: 'egress-a',
        hits: [
          { rule_id: 'db-url-with-password', source: 'tool_result', tool: 'kubectl_execute', length: 42, shape: 'aaaaaaaaaa://aaaa:AAAA9@aaaa' },
          { rule_id: 'high-entropy-blob', source: 'user' }, // legacy: no shape/value
        ],
      },
    ]);
    expect(agg.details).toHaveLength(1);
    expect(agg.details[0].tool).toBe('kubectl_execute');
  });

  it('collects values[] for PII and preserves origin / doc_url', () => {
    const agg = aggregatePii([
      {
        detector: 'pii',
        audit_id: 'scrub-a',
        hit_count: 1,
        values: [
          {
            token: '[EMAIL_1]',
            category: 'EMAIL',
            source: 'user',
            origin: 'knowledge_base',
            doc_url: 'https://wiki/pages/1',
            length: 20,
            shape: 'aaaaaa.a@aaaaaa.aaaa',
          },
        ],
      },
    ]);
    expect(agg.details).toHaveLength(1);
    expect(agg.details[0].origin).toBe('knowledge_base');
    expect(agg.details[0].doc_url).toBe('https://wiki/pages/1');
    expect(agg.details[0].value).toBeUndefined(); // reveal off → no raw value
  });

  it('returns an empty detail list for legacy events with no per-value data', () => {
    expect(aggregatePii([{ detector: 'pii', hit_count: 2, categories: ['EMAIL'] }]).details).toEqual([]);
    expect(aggregateSecrets([{ detector: 'secrets', rule_ids: ['ssn'], hit_count: 1 }]).details).toEqual([]);
  });
});

describe('doc_url link safety', () => {
  it('accepts http(s) and rejects javascript:/data:/non-string urls', () => {
    // Imported from the component, not re-declared — a local copy would
    // keep passing while the real guard regressed.
    // doc_url comes from synced KB metadata, so a javascript: url must
    // never become a clickable href.
    expect(isSafeHttpUrl('https://wiki/1')).toBe(true);
    expect(isSafeHttpUrl('http://wiki/1')).toBe(true);
    // Schemes are case-insensitive (RFC 3986) — valid urls survive any
    // casing, dangerous ones stay blocked in any casing.
    expect(isSafeHttpUrl('HTTPS://wiki/1')).toBe(true);
    expect(isSafeHttpUrl('Http://wiki/1')).toBe(true);
    // eslint-disable-next-line no-script-url
    expect(isSafeHttpUrl('JavaScript:alert(1)')).toBe(false);
    // eslint-disable-next-line no-script-url
    expect(isSafeHttpUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeHttpUrl('data:text/html,<script>')).toBe(false);
    expect(isSafeHttpUrl({ href: 'https://wiki/1' })).toBe(false);
    expect(isSafeHttpUrl(undefined)).toBe(false);
  });
});

describe('malformed metadata resilience', () => {
  it('drops null/undefined entries in values[] instead of crashing the modal', () => {
    const agg = aggregatePii([
      { detector: 'pii', hit_count: 2, values: [null, { token: '[EMAIL_1]', category: 'EMAIL', shape: 'aaa@aa.aa' }, undefined] },
    ]);
    expect(agg.details).toHaveLength(1);
    expect(agg.details[0].token).toBe('[EMAIL_1]');
  });
});

describe('capped values[]', () => {
  it('propagates values_truncated so the UI can say the list is partial', () => {
    const agg = aggregatePii([
      { detector: 'pii', hit_count: 287, values: [{ token: '[EMAIL_1]', category: 'EMAIL', shape: 'aaa@aa.aa' }], values_truncated: true },
    ]);
    expect(agg.truncated).toBe(true);
    expect(agg.totalHits).toBe(287); // count stays truthful
  });

  it('is not truncated when the backend did not cap', () => {
    const agg = aggregatePii([{ detector: 'pii', hit_count: 2, values: [{ token: '[EMAIL_1]', category: 'EMAIL' }] }]);
    expect(agg.truncated).toBe(false);
  });
});

describe('DetectedValues rendering', () => {
  const base = {
    token: '[EMAIL_1]',
    category: 'EMAIL',
    source: 'tool_result',
    agent: 'events',
    tool: 'kubectl_execute',
    length: 20,
    shape: 'aaaaaa.a@aaaaaa.aaaa',
  };

  it('renders shape, token and provenance, and no testing badge when values are absent', () => {
    render(<DetectedValues details={[base]} />);
    expect(screen.getByText('aaaaaa.a@aaaaaa.aaaa')).toBeInTheDocument();
    expect(screen.getByText('[EMAIL_1]')).toBeInTheDocument();
    expect(screen.getByText(/tool: kubectl_execute/)).toBeInTheDocument();
    expect(screen.getByText(/agent: events/)).toBeInTheDocument();
    expect(screen.queryByText(/testing mode/i)).not.toBeInTheDocument();
  });

  it('shows the testing-mode badge and the raw value when the backend revealed it', () => {
    render(<DetectedValues details={[{ ...base, value: 'zaphod.b@galaxy.test' }]} />);
    expect(screen.getByText('zaphod.b@galaxy.test')).toBeInTheDocument();
    expect(screen.getByText(/raw values shown — testing mode/i)).toBeInTheDocument();
  });

  it('renders an unknown origin verbatim rather than dropping it', () => {
    render(<DetectedValues details={[{ ...base, origin: 'some_future_origin' }]} />);
    expect(screen.getByText(/some_future_origin/)).toBeInTheDocument();
  });

  it('shows the truncation notice when the backend capped the list', () => {
    render(<DetectedValues details={[base]} truncated />);
    expect(screen.getByText(/Showing the first 1 values/i)).toBeInTheDocument();
  });
});
