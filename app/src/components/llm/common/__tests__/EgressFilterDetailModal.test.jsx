// Regression tests for the egressfilter detail modal aggregators. Pure
// data functions — verified without rendering the modal itself. The
// modal render path is exercised by the ResponseMetaRail integration
// test (chip click → modal opens) in ResponseMetaRail.test.jsx.

import { __aggregateSecretsForTest as aggregateSecrets, __aggregatePiiForTest as aggregatePii } from '@components/llm/common/EgressFilterDetailModal';

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
