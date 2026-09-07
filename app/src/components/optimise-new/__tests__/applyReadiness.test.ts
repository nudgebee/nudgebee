import { applyReadiness, autoOptimizeNotice, restartSafeguard } from '../applyReadiness';

const rec = (band: string | null, impact: Record<string, unknown>, changeClass = 'reductive') => ({
  safety_band: band,
  finops_score_breakdown: { safety_band: band, change_class: changeClass, impact_summary: impact },
});

describe('applyReadiness', () => {
  it('claims nothing for a row loaded without its safety columns', () => {
    expect(applyReadiness({ id: 'r1', estimated_savings: 12 }, true)).toBeNull();
    expect(applyReadiness(undefined, true)).toBeNull();
  });

  it('gives a safe verdict positive confirmation and no acknowledgement', () => {
    const r = applyReadiness(rec('safe', { dependent_count: 0, safety_reason: 'no known dependents' }), true);
    expect(r).toMatchObject({ tone: 'success', title: 'No known dependents — safe to apply', needsAck: false });
    expect(r?.message).toBe('No known dependents.');
  });

  it('states the fact and the safeguard for production callers, and asks for one acknowledgement', () => {
    const r = applyReadiness(
      rec('risky', {
        dependent_count: 3,
        production_dependents: 2,
        safety_reason: '2 production dependent(s) in the blast radius',
        dependents: [{ name: 'a', criticality: 'critical' }, { name: 'b' }],
      }),
      false
    );
    expect(r).toMatchObject({ tone: 'warning', title: '2 production dependents in the blast radius', needsAck: true });
    expect(r?.message).toBe(
      '2 production dependent(s) in the blast radius. 1 business-critical. Turn on No-restart (in-place), or apply in a maintenance window.'
    );
  });

  it('names the in-place path when it is on, without promising no restart everywhere', () => {
    const r = applyReadiness(rec('risky', { dependent_count: 1, production_dependents: 1, safety_reason: 'x' }), true);
    expect(r?.message).toContain('Kubernetes 1.35+');
    expect(r?.message).toContain('older clusters roll pods one at a time');
  });

  it('review with production callers acknowledges; review without them does not', () => {
    const additive = applyReadiness(
      rec('review', { dependent_count: 2, production_dependents: 2, safety_reason: 'adds capacity only' }, 'additive'),
      true
    );
    expect(additive?.needsAck).toBe(false);
    expect(additive?.tone).toBe('success');
    const contained = applyReadiness(
      rec('review', { dependent_count: 2, production_dependents: 0, safety_reason: '2 dependent(s); none detected as production' }),
      true
    );
    expect(contained).toMatchObject({ tone: 'success', title: 'Contained blast radius', needsAck: false });
  });

  it('an irreversible change stays red, names a removal safeguard, and always acknowledges — even at Review', () => {
    const review = applyReadiness(rec('review', { dependent_count: 0, safety_reason: 'no known dependents' }, 'destructive'), true);
    expect(review).toMatchObject({ tone: 'warning', title: 'Irreversible change', needsAck: true });
    expect(review?.message).toContain("Removal can't be undone");
    expect(review?.message).not.toContain('No-restart');
    const risky = applyReadiness(
      rec('risky', { dependent_count: 2, production_dependents: 0, safety_reason: 'irreversible change with 2 dependent(s)' }, 'destructive'),
      true
    );
    expect(risky).toMatchObject({ tone: 'critical', title: 'Irreversible change', needsAck: true });
  });

  it('reads an unassessed verdict as not-yet-in-graph, never as risky', () => {
    const r = applyReadiness(rec(null, {}), true);
    expect(r).toMatchObject({ tone: 'info', title: 'Not in the dependency graph yet', needsAck: false });
    expect(r?.message).not.toMatch(/risk/i);
  });

  it('never uses discouraging wording', () => {
    const all = [
      applyReadiness(rec('risky', { dependent_count: 3, production_dependents: 2, safety_reason: 'x' }), false),
      applyReadiness(rec('unknown', {}), true),
      restartSafeguard(true),
      restartSafeguard(false),
    ]
      .map((r) => (typeof r === 'string' ? r : `${r?.title} ${r?.message}`))
      .join(' ');
    expect(all).not.toMatch(/are you sure|caution|carefully|don't assume|dangerous/i);
  });
});

describe('autoOptimizeNotice', () => {
  it('speaks only when production services depend on the workload', () => {
    expect(autoOptimizeNotice(rec('risky', { production_dependents: 0 }))).toBeNull();
    expect(autoOptimizeNotice(undefined)).toBeNull();
    expect(autoOptimizeNotice(rec('risky', { production_dependents: 1 }))).toMatch(/^1 production service depends on this workload/);
    expect(autoOptimizeNotice(rec('risky', { production_dependents: 3 }))).toMatch(/^3 production services depend on this workload/);
  });
});
