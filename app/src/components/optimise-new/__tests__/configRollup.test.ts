import { foldConfigRules } from '../configRollup';

describe('foldConfigRules', () => {
  it('returns nothing for empty or missing input', () => {
    expect(foldConfigRules([])).toEqual([]);
    expect(foldConfigRules(null as any)).toEqual([]);
  });

  it('sums a rule across accounts and keeps the per-account split', () => {
    const [rule] = foldConfigRules([
      { rule_name: 'aws_lambda_tracing', severity: 'Low', account_id: 'a', count: 1194 },
      { rule_name: 'aws_lambda_tracing', severity: 'Low', account_id: 'b', count: 37 },
      { rule_name: 'aws_lambda_tracing', severity: 'Low', account_id: 'c', count: 2 },
    ]);

    expect(rule.count).toBe(1233);
    expect(rule.accountIds).toEqual(['a', 'b', 'c']);
    expect(rule.countByAccount).toEqual({ a: 1194, b: 37, c: 2 });
  });

  it('reports the worst severity a rule reaches in any account', () => {
    const [rule] = foldConfigRules([
      { rule_name: 'health_check', severity: 'Low', account_id: 'a', count: 4 },
      { rule_name: 'health_check', severity: 'Critical', account_id: 'b', count: 1 },
      { rule_name: 'health_check', severity: 'Medium', account_id: 'c', count: 2 },
    ]);

    expect(rule.severity).toBe('Critical');
  });

  it('keeps a per-band breakdown for a rule spanning several severities', () => {
    const [rule] = foldConfigRules([
      { rule_name: 'misconfigurations', severity: 'Medium', account_id: 'a', count: 159 },
      { rule_name: 'misconfigurations', severity: 'Critical', account_id: 'a', count: 2 },
      { rule_name: 'misconfigurations', severity: 'Info', account_id: 'a', count: 3 },
    ]);

    // The row displays the worst band, but the breakdown is what severity
    // filtering reads — otherwise the rule vanishes from Medium and Info.
    expect(rule.severity).toBe('Critical');
    expect(rule.countBySeverity).toEqual({ Medium: 159, Critical: 2, Info: 3 });
    expect(rule.count).toBe(164);
  });

  it('orders worst first, however loud the lower bands are', () => {
    const rules = foldConfigRules([
      { rule_name: 'aws_tags', severity: 'Low', account_id: 'a', count: 969 },
      { rule_name: 'certificate_expiry', severity: 'Critical', account_id: 'a', count: 23 },
      { rule_name: 'aws_lambda_tracing', severity: 'Low', account_id: 'a', count: 1233 },
    ]);

    // 23 Critical findings outrank 1,233 Low ones — the count only orders within a band.
    expect(rules.map((r) => r.ruleName)).toEqual(['certificate_expiry', 'aws_lambda_tracing', 'aws_tags']);
  });

  it('breaks a severity tie on blast radius', () => {
    const rules = foldConfigRules([
      { rule_name: 'narrow_rule', severity: 'High', account_id: 'a', count: 3 },
      { rule_name: 'wide_rule', severity: 'High', account_id: 'a', count: 300 },
    ]);

    expect(rules.map((r) => r.ruleName)).toEqual(['wide_rule', 'narrow_rule']);
  });

  it('ranks a multi-band rule by the worst band it reaches', () => {
    const rules = foldConfigRules([
      { rule_name: 'busy_low', severity: 'Low', account_id: 'a', count: 500 },
      { rule_name: 'misconfigurations', severity: 'Medium', account_id: 'a', count: 159 },
      { rule_name: 'misconfigurations', severity: 'Critical', account_id: 'a', count: 2 },
    ]);

    expect(rules[0].ruleName).toBe('misconfigurations');
  });

  it('ignores rows with no rule name and treats a missing severity as Unknown', () => {
    const rules = foldConfigRules([
      { rule_name: '', severity: 'High', account_id: 'a', count: 5 },
      { rule_name: 'aws_tags', account_id: 'a', count: 3 },
    ]);

    expect(rules).toHaveLength(1);
    expect(rules[0].severity).toBe('Unknown');
  });

  it('tolerates a missing account id without inventing one', () => {
    const [rule] = foldConfigRules([
      { rule_name: 'aws_tags', severity: 'Low', count: 4 },
      { rule_name: 'aws_tags', severity: 'Low', account_id: 'a', count: 6 },
    ]);

    expect(rule.count).toBe(10);
    expect(rule.accountIds).toEqual(['a']);
    expect(rule.countByAccount).toEqual({ a: 6 });
  });
});
