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
