import { chipsToBlockLabelMatchers } from '@components1/k8s/common/QueryModeSwitcher';

describe('chipsToBlockLabelMatchers', () => {
  it('forwards all chip operators (none dropped)', () => {
    const block = {
      selectedMetric: 'up',
      queryItems: [
        { label: 'job', operator: '_eq', value: 'api-server' },
        { label: 'instance', operator: '_neq', value: 'node-1' },
        { label: 'pod', operator: '_regex', value: 'api-.*' },
      ],
    };
    expect(chipsToBlockLabelMatchers(block)).toEqual([
      { label: 'job', operator: '_eq', value: 'api-server' },
      { label: 'instance', operator: '_neq', value: 'node-1' },
      { label: 'pod', operator: '_regex', value: 'api-.*' },
    ]);
  });

  it('normalizes legacy UI operator tokens to backend tokens', () => {
    const block = {
      queryItems: [
        { label: 'job', operator: '=', value: 'api-server' },
        { label: 'instance', operator: '!=', value: 'node-1' },
      ],
    };
    const matchers = chipsToBlockLabelMatchers(block);
    expect(matchers[0].operator).toBe('_eq');
    expect(matchers[1].operator).toBe('_neq');
  });

  it('drops chips with empty label or empty value', () => {
    const block = {
      queryItems: [
        { label: '', operator: '_eq', value: 'x' },
        { label: 'job', operator: '_eq', value: '' },
        { label: 'instance', operator: '_eq', value: 'node-1' },
      ],
    };
    expect(chipsToBlockLabelMatchers(block)).toEqual([{ label: 'instance', operator: '_eq', value: 'node-1' }]);
  });

  it('returns matchers scoped to the input block only (no cross-block leakage)', () => {
    const blockA = { queryItems: [{ label: 'a', operator: '_eq', value: '1' }] };
    const blockB = { queryItems: [{ label: 'b', operator: '_neq', value: '2' }] };
    expect(chipsToBlockLabelMatchers(blockA)).toEqual([{ label: 'a', operator: '_eq', value: '1' }]);
    expect(chipsToBlockLabelMatchers(blockB)).toEqual([{ label: 'b', operator: '_neq', value: '2' }]);
  });

  it('handles missing or empty queryItems gracefully', () => {
    expect(chipsToBlockLabelMatchers({ queryItems: undefined })).toEqual([]);
    expect(chipsToBlockLabelMatchers({ queryItems: [] })).toEqual([]);
    expect(chipsToBlockLabelMatchers({})).toEqual([]);
  });

  it('returns empty array for null / undefined block', () => {
    expect(chipsToBlockLabelMatchers(undefined)).toEqual([]);
    expect(chipsToBlockLabelMatchers(null)).toEqual([]);
  });
});
