import {
  dependentRoleLabel,
  proximityLabel,
  provenanceLabel,
  isProdEnvironment,
  coverageTone,
  coverageSubtitle,
  coverageExplainer,
  prodChipState,
  formatEnvironment,
  dependentCountNoun,
  nodeTypeLabel,
  impactSignalSources,
  getChangeClass,
  changeClassLabel,
  changeClassTone,
  deriveVerdict,
  criticalityTone,
  criticalityLabel,
  businessCriticalCount,
  observedAgeLabel,
  presentBand,
  presentRecommendation,
  presentBandOnly,
  coverageLabel,
} from '../safetyBand';

describe('safetyBand dependent categorization helpers', () => {
  it('maps relationships to role chips per direction', () => {
    expect(dependentRoleLabel('CALLS')).toBe('Caller');
    expect(dependentRoleLabel('RUNS_ON')).toBe('Hosted');
    expect(dependentRoleLabel('CALLS', 'downstream')).toBe('Calls');
    expect(dependentRoleLabel('SOMETHING_NEW')).toBe('Dependent');
    expect(dependentRoleLabel('SOMETHING_NEW', 'downstream')).toBe('Depends on');
    expect(dependentRoleLabel(undefined)).toBeNull();
  });

  it('renders proximity as Direct / N hops', () => {
    expect(proximityLabel(1)).toBe('Direct');
    expect(proximityLabel(3)).toBe('3 hops');
    expect(proximityLabel(0)).toBeNull();
    expect(proximityLabel(undefined)).toBeNull();
  });

  it('collapses sources into the strongest provenance claim', () => {
    expect(provenanceLabel(['ebpf'])).toBe('Observed traffic');
    expect(provenanceLabel(['eBPF'])).toBe('Observed traffic');
    expect(provenanceLabel(['k8s', 'traces'])).toBe('Observed in traces');
    expect(provenanceLabel(['manual', 'ebpf'])).toBe('User-declared');
    expect(provenanceLabel(['aws'])).toBe('Platform metadata');
    expect(provenanceLabel(['dns_resolver'])).toBe('Inferred');
    expect(provenanceLabel([])).toBeNull();
    expect(provenanceLabel(undefined)).toBeNull();
  });

  it('grades coverage presentation per tier', () => {
    expect(coverageTone('high')).toBe('success');
    expect(coverageTone('observed')).toBe('info');
    expect(coverageTone('low')).toBe('warning');
    expect(coverageTone(undefined)).toBe('neutral');
    expect(coverageSubtitle('low')).toBe('Based on a single source');
    expect(coverageSubtitle(undefined)).toBeNull();
    expect(coverageExplainer('high', 3)).toBeNull();
    expect(coverageExplainer('observed', 0)).toMatch(/reports nothing calling/);
    expect(coverageExplainer('observed', 4)).toMatch(/not been corroborated/);
    expect(coverageExplainer('low', 5)).toMatch(/single source/);
    expect(coverageExplainer('low', 0)).toMatch(/absence of evidence/);
    expect(coverageExplainer('none', 0)).toMatch(/not found/);
  });

  it('flags production environments like the backend isProdEnv', () => {
    expect(isProdEnvironment('prod')).toBe(true);
    expect(isProdEnvironment(' Production ')).toBe(true);
    expect(isProdEnvironment('staging')).toBe(false);
    expect(isProdEnvironment(undefined)).toBe(false);
  });
});

describe('prodChipState', () => {
  it('is null when the summary carries no prod count', () => {
    expect(prodChipState(null)).toBeNull();
    expect(prodChipState({})).toBeNull();
  });

  it('flags production dependents regardless of resolution regime', () => {
    expect(prodChipState({ production_dependents: 3 })).toBe('prod');
    expect(prodChipState({ production_dependents: 3, environment_resolved: true })).toBe('prod');
  });

  it('claims a verified zero only when environments were resolved', () => {
    expect(prodChipState({ production_dependents: 0, environment_resolved: true })).toBe('verified-zero');
  });

  it('treats a pre-resolution zero as unknown, not as evidence of absence', () => {
    expect(prodChipState({ production_dependents: 0 })).toBe('unknown');
    expect(prodChipState({ production_dependents: 0, environment_resolved: false })).toBe('unknown');
  });
});

describe('formatEnvironment', () => {
  it('prettifies the account-tier spellings', () => {
    expect(formatEnvironment('prod')).toBe('Production');
    expect(formatEnvironment('non_prod')).toBe('Non-production');
    expect(formatEnvironment('non-prod')).toBe('Non-production');
  });

  it('passes free-form label values through unchanged', () => {
    expect(formatEnvironment('staging')).toBe('staging');
  });
});

describe('dependentCountNoun', () => {
  it('names pods when every dependent is a pod', () => {
    expect(
      dependentCountNoun([
        { name: 'a', node_type: 'Pod' },
        { name: 'b', node_type: 'Pod' },
      ])
    ).toBe('Dependent pods');
  });

  it('defaults to services for mixed or empty lists', () => {
    expect(
      dependentCountNoun([
        { name: 'a', node_type: 'Pod' },
        { name: 'b', node_type: 'Workload' },
      ])
    ).toBe('Dependent services');
    expect(dependentCountNoun([])).toBe('Dependent services');
    expect(dependentCountNoun(undefined)).toBe('Dependent services');
  });
});

describe('nodeTypeLabel', () => {
  it('marks unresolved external callers as such', () => {
    expect(nodeTypeLabel('ExternalService')).toBe('External (unresolved)');
    expect(nodeTypeLabel('Workload')).toBe('Workload');
    expect(nodeTypeLabel(undefined)).toBeNull();
  });
});

describe('getChangeClass', () => {
  const rec = (cls: any) => ({ finops_score_breakdown: JSON.stringify({ change_class: cls }) });

  it('reads a valid class from the breakdown, string or object', () => {
    expect(getChangeClass(rec('additive'))).toBe('additive');
    expect(getChangeClass({ finops_score_breakdown: { change_class: 'destructive' } })).toBe('destructive');
  });

  it('rejects absent or unrecognized values', () => {
    expect(getChangeClass(rec(undefined))).toBeNull();
    expect(getChangeClass(rec('explosive'))).toBeNull();
    expect(getChangeClass(null)).toBeNull();
  });
});

describe('changeClass presentation', () => {
  it('maps class to label and tone', () => {
    expect(changeClassLabel('additive')).toBe('Additive');
    expect(changeClassTone('additive')).toBe('success');
    expect(changeClassTone('reductive')).toBe('warning');
    expect(changeClassTone('destructive')).toBe('critical');
    expect(changeClassLabel(null)).toBeNull();
    expect(changeClassTone(null)).toBe('neutral');
  });
});

describe('deriveVerdict', () => {
  it('headlines production dependents only when the band grades them dangerous', () => {
    expect(deriveVerdict('risky', 10, 10, false, 'reductive')).toEqual({ tone: 'warning', title: '10 production dependents in the blast radius' });
    expect(deriveVerdict('risky', 1, 1, false, 'reductive')).toEqual({ tone: 'warning', title: '1 production dependent in the blast radius' });
  });

  it('never contradicts a Review verdict on an additive change with production dependents', () => {
    const v = deriveVerdict('review', 5, 5, false, 'additive');
    expect(v.tone).toBe('success');
    expect(v.title).toBe('Capacity increase — dependents unaffected');

    // A partial summary (prod count without the total) must still name the case.
    const partial = deriveVerdict('review', 5, undefined, false, 'additive');
    expect(partial.tone).toBe('success');
    expect(partial.title).toBe('Capacity increase — dependents unaffected');
  });

  it('keeps a visible caution on irreversible changes, even with an empty neighbourhood', () => {
    expect(deriveVerdict('review', 0, 0, false, 'destructive')).toEqual({ tone: 'warning', title: 'Irreversible change' });
    expect(deriveVerdict('risky', 0, 0, false, 'destructive')).toEqual({ tone: 'critical', title: 'Irreversible change' });
  });

  it('keeps the pre-existing verdicts for everything else', () => {
    expect(deriveVerdict('risky', 0, 500, true, 'reductive')).toEqual({ tone: 'warning', title: 'Large blast radius' });
    expect(deriveVerdict('unknown', 0, 0, false, null)).toEqual({ tone: 'warning', title: 'Not in the dependency graph yet' });
    expect(deriveVerdict('safe', 0, 0, false, 'reductive')).toEqual({ tone: 'success', title: 'No known dependents' });
    expect(deriveVerdict('review', 0, 3, false, 'reductive')).toEqual({ tone: 'success', title: 'Contained blast radius' });
    expect(deriveVerdict(undefined, undefined, undefined, undefined, null).tone).toBe('success');
  });
});

describe('impactSignalSources', () => {
  it('unions and prettifies sources across both dependency lists', () => {
    expect(
      impactSignalSources({
        dependents: [
          { name: 'a', sources: ['ebpf', 'traces'] },
          { name: 'b', sources: ['ebpf'] },
        ],
        downstream_dependencies: [{ name: 'c', sources: ['k8s'] }],
      })
    ).toEqual(['eBPF traffic', 'Kubernetes metadata', 'Traces']);
  });

  it('is empty for pre-attribution summaries', () => {
    expect(impactSignalSources({ dependents: [{ name: 'a' }] })).toEqual([]);
    expect(impactSignalSources(null)).toEqual([]);
  });
});

describe('criticality presentation', () => {
  it('labels only the exceptional tiers — medium is the unstored default', () => {
    expect(criticalityLabel('critical')).toBe('Critical');
    expect(criticalityLabel('high')).toBe('High');
    expect(criticalityLabel('low')).toBe('Low');
    expect(criticalityLabel('medium')).toBeNull();
    expect(criticalityLabel(undefined)).toBeNull();
  });

  it('reuses the criticality manager tones so the two surfaces cannot drift', () => {
    expect(criticalityTone('critical')).toBe('critical');
    expect(criticalityTone('high')).toBe('warning');
    expect(criticalityTone('low')).toBe('neutral');
    expect(criticalityTone(undefined)).toBe('neutral');
  });

  it('falls back to neutral for a tier the UI does not recognise', () => {
    // The tier crosses a JSONB boundary from a text column, so a value outside
    // the union can reach here regardless of the declared type.
    expect(criticalityTone('urgent' as any)).toBe('neutral');
  });
});

describe('businessCriticalCount', () => {
  it('counts only critical and high tiers', () => {
    const deps = [
      { name: 'pay', criticality: 'critical' as const },
      { name: 'ingress', criticality: 'high' as const },
      { name: 'cron', criticality: 'low' as const },
      { name: 'plain' },
    ];
    expect(businessCriticalCount(deps)).toBe(2);
  });

  it('is zero when nothing is tiered, and tolerates absence', () => {
    expect(businessCriticalCount([{ name: 'a' }, { name: 'b' }])).toBe(0);
    expect(businessCriticalCount([])).toBe(0);
    expect(businessCriticalCount(undefined)).toBe(0);
  });
});

describe('observedAgeLabel', () => {
  const day = 24 * 60 * 60 * 1000;
  // A fresh snapshot: computed within the tombstone window relative to now.
  const freshComputed = new Date(Date.now() - day).toISOString();

  it('measures the age against the snapshot, in whole days, from two days up', () => {
    const seen = new Date(Date.parse(freshComputed) - 5 * day).toISOString();
    expect(observedAgeLabel({ name: 'a', last_observed_at: seen }, freshComputed)).toBe('Last seen 5d ago');
  });

  it('hides sub-two-day ages — hourly builds and the signal lookback make them meaningless', () => {
    const seen = new Date(Date.parse(freshComputed) - 1.25 * day).toISOString();
    expect(observedAgeLabel({ name: 'a', last_observed_at: seen }, freshComputed)).toBeNull();
  });

  it('hides the age entirely once the snapshot itself is older than the tombstone window', () => {
    const staleComputed = new Date(Date.now() - 10 * day).toISOString();
    const seen = new Date(Date.parse(staleComputed) - 5 * day).toISOString();
    expect(observedAgeLabel({ name: 'a', last_observed_at: seen }, staleComputed)).toBeNull();
  });

  it('needs both an observation and an anchor', () => {
    expect(observedAgeLabel({ name: 'a' }, freshComputed)).toBeNull();
    expect(observedAgeLabel({ name: 'a', last_observed_at: freshComputed }, undefined)).toBeNull();
    expect(observedAgeLabel({ name: 'a', last_observed_at: 'not-a-date' }, freshComputed)).toBeNull();
  });
});

describe('band presentation', () => {
  it('names the action, not the fear, and keeps the stored enum out of sight', () => {
    expect(presentBandOnly('safe')).toMatchObject({ key: 'safe', label: 'Safe', tone: 'success' });
    expect(presentBandOnly('review')).toMatchObject({ key: 'quick_check', label: 'Quick check', tone: 'info' });
    expect(presentBandOnly('risky')).toMatchObject({ key: 'plan', label: 'Plan it', tone: 'warning' });
    expect(presentBandOnly('unknown')).toMatchObject({ key: 'not_assessed', label: 'Not assessed', tone: 'neutral' });
    expect(presentBand(null, null).key).toBe('not_assessed');
    expect(presentBand('RISKY ', null).label).toBe('Plan it');
  });

  it('reserves red for an irreversible change the graph also grades risky; a dependent-free removal is amber', () => {
    expect(presentBand('risky', 'destructive')).toMatchObject({ key: 'irreversible', label: 'Irreversible', tone: 'critical' });
    expect(presentBand('review', 'destructive')).toMatchObject({ key: 'irreversible', label: 'Irreversible', tone: 'warning' });
    expect(presentBand('safe', 'destructive')).toMatchObject({ key: 'irreversible', tone: 'warning' });
    expect(presentBand('unknown', 'destructive').key).toBe('not_assessed');
  });

  it('never lets the chip argue with the banner beneath it', () => {
    // Assessed bands only: an unassessed row keeps a neutral chip on purpose
    // (a filter full of them must not read amber) while the panel banner
    // beneath it explains "Not in the dependency graph yet" in warning tone.
    expect(presentBandOnly('unknown').tone).toBe('neutral');
    expect(deriveVerdict('unknown', 0, 0, false, null).tone).toBe('warning');
    const bands = ['safe', 'review', 'risky'];
    const classes = [null, 'additive', 'reductive', 'destructive'] as const;
    for (const band of bands) {
      for (const cls of classes) {
        for (const prod of [0, 2]) {
          const verdict = deriveVerdict(band, prod, prod + 1, false, cls);
          if (verdict.tone === 'success') continue;
          expect(presentBand(band, cls).tone).toBe(verdict.tone);
        }
      }
    }
  });

  it('reads a recommendation row once, from band and change class together', () => {
    const rec = { safety_band: 'review', finops_score_breakdown: { change_class: 'destructive' } };
    expect(presentRecommendation(rec).label).toBe('Irreversible');
    expect(presentRecommendation({ safety_band: 'risky' }).label).toBe('Plan it');
    expect(presentRecommendation(undefined).key).toBe('not_assessed');
  });

  it('keeps coverage words out of the band vocabulary', () => {
    expect(coverageLabel('observed')).toBe('Observed');
    expect(coverageLabel(undefined)).toBe('');
  });
});
