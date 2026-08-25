import { formatRuleName, ruleNameSearchText } from '@components/optimise-new/utils';

describe('formatRuleName', () => {
  it('labels a pod_right_sizing row under Configuration as a missing-requests finding', () => {
    expect(formatRuleName('pod_right_sizing', 'Configuration')).toBe('Missing Resource Requests');
  });

  it('keeps the sizing label under RightSizing and when no category is in hand', () => {
    expect(formatRuleName('pod_right_sizing', 'RightSizing')).toBe('Pod Right Sizing');
    expect(formatRuleName('pod_right_sizing')).toBe('Pod Right Sizing');
  });

  it('ignores the category for rules with no category-scoped label', () => {
    expect(formatRuleName('unused_pvc', 'Configuration')).toBe('Unused PVC');
    expect(formatRuleName('gcp_sql_no_backup', 'Configuration')).toBe('GCP Sql No Backup');
  });
});

describe('ruleNameSearchText', () => {
  it('matches a rule by every label it can render, so the Rules filter stays findable', () => {
    const text = ruleNameSearchText('pod_right_sizing');
    expect(text).toContain('Pod Right Sizing');
    expect(text).toContain('Missing Resource Requests');
  });

  it('is just the label for rules with a single framing', () => {
    expect(ruleNameSearchText('unused_pvc')).toBe('Unused PVC');
  });
});
