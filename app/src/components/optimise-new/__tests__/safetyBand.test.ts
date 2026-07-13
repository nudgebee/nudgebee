import {
  dependentRoleLabel,
  proximityLabel,
  provenanceLabel,
  isProdEnvironment,
  coverageTone,
  coverageSubtitle,
  coverageExplainer,
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
