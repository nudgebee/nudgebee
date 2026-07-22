import { generateQuery } from '../LogGenerateQuery';

// For ES, `metricName` is the selected index, not a query term. The index is
// carried separately as onQueryChange's `index`, so generateQuery emitting it
// leaked the index name into the Code tab's CodeMirror the moment a user picked
// an index without adding a filter.
describe('generateQuery — ES index is not a query', () => {
  it('returns empty when an index is selected but no filters are applied', () => {
    expect(generateQuery('ES', [], [], 'logs-generic.otel-default', undefined, undefined, 'logs')).toBe('');
  });

  it('returns empty when operations exist but are all blank', () => {
    const blankOps = [{ op: 'contains', value: '   ' }];
    expect(generateQuery('ES', [], blankOps, 'logs-generic.otel-default', undefined, undefined, 'logs')).toBe('');
  });

  it('still builds a query once a chip is present', () => {
    const chips = [{ label: 'namespace', operator: '=', value: 'prod' }];
    const out = generateQuery('ES', chips, [], 'logs-generic.otel-default', undefined, undefined, 'logs');
    expect(out).not.toBe('');
    expect(out).toContain('namespace');
  });

  it('still builds a query from a line operation alone', () => {
    const ops = [{ op: 'CONTAINS', value: 'timeout' }];
    const out = generateQuery('ES', [], ops, 'logs-generic.otel-default', undefined, undefined, 'logs');
    expect(out).not.toBe('');
  });
});

// Providers that share the isLogsProvider branch must be untouched. In practice
// they never populate metricName (the picker renders for ES / metrics providers
// only), but pin it so the ES carve-out can't widen by accident.
describe('generateQuery — other providers keep returning metricName', () => {
  it('loki passes metricName through', () => {
    expect(generateQuery('loki', [], [], 'some-metric', undefined, undefined, 'logs')).toBe('some-metric');
  });

  it('prometheus passes metricName through', () => {
    expect(generateQuery('prometheus', [], [], 'container_cpu_usage', undefined, undefined, 'metrics')).toBe('container_cpu_usage');
  });

  it('datadog keeps its own aggregator shape', () => {
    const out = generateQuery('datadog', [], [], 'system.cpu.user', 'avg', [], 'metrics');
    expect(out).toContain('system.cpu.user');
  });
});
