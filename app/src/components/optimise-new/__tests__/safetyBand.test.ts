import { dependentRoleLabel, proximityLabel, provenanceLabel, isProdEnvironment } from '../safetyBand';

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
    expect(provenanceLabel(['k8s', 'traces'])).toBe('Observed in traces');
    expect(provenanceLabel(['manual', 'ebpf'])).toBe('User-declared');
    expect(provenanceLabel(['aws'])).toBe('Platform metadata');
    expect(provenanceLabel(['dns_resolver'])).toBe('Inferred');
    expect(provenanceLabel([])).toBeNull();
    expect(provenanceLabel(undefined)).toBeNull();
  });

  it('flags production environments like the backend isProdEnv', () => {
    expect(isProdEnvironment('prod')).toBe(true);
    expect(isProdEnvironment(' Production ')).toBe(true);
    expect(isProdEnvironment('staging')).toBe(false);
    expect(isProdEnvironment(undefined)).toBe(false);
  });
});
