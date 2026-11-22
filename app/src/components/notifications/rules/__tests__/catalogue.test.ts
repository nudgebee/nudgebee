import { ALL_TYPE_KEYS, FALLBACK_TYPE_KEYS, RULE_TYPE, isRuleTypeKey } from '../catalogue';

describe('AI Gateway budget alert rule type', () => {
  it('is its own rule type, outside "all types" and its fallback', () => {
    expect(isRuleTypeKey('llm_budget')).toBe(true);
    expect(RULE_TYPE.llm_budget.alone).toBe(true);
    expect(ALL_TYPE_KEYS).not.toContain('llm_budget');
    expect(FALLBACK_TYPE_KEYS).not.toContain('llm_budget');
  });
});
