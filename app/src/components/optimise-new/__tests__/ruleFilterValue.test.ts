import { makeRuleFilterValue, parseRuleFilterValue, formatRuleName } from '../utils';

describe('makeRuleFilterValue / parseRuleFilterValue', () => {
  it('round-trips a category-scoped value', () => {
    const value = makeRuleFilterValue('pod_right_sizing', 'Configuration');
    expect(value).toBe('pod_right_sizing::Configuration');
    expect(parseRuleFilterValue(value)).toEqual({ ruleName: 'pod_right_sizing', category: 'Configuration' });
  });

  it('emits a bare rule_name when no category is given', () => {
    expect(makeRuleFilterValue('pod_right_sizing')).toBe('pod_right_sizing');
    expect(makeRuleFilterValue('pod_right_sizing', '')).toBe('pod_right_sizing');
  });

  it('parses a bare (legacy) value as unscoped', () => {
    expect(parseRuleFilterValue('pod_right_sizing')).toEqual({ ruleName: 'pod_right_sizing' });
  });

  it('keeps the same rule under two categories distinct', () => {
    expect(makeRuleFilterValue('pod_right_sizing', 'Configuration')).not.toBe(makeRuleFilterValue('pod_right_sizing', 'RightSizing'));
  });

  it('labels the two framings the way the table does', () => {
    // The Configuration framing is a scheduling-hygiene defect, not a sizing one.
    expect(formatRuleName('pod_right_sizing', 'Configuration')).toBe('Missing Resource Requests');
    expect(formatRuleName('pod_right_sizing', 'RightSizing')).toBe('Pod Right Sizing');
  });
});
