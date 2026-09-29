import { conditionDraft, parseCondition, wildcardBeforeVerb } from './ToolApprovalsAdminTab';
import type { ToolApprovalCondition } from '@api1/tool-approval';

describe('tool approval argument condition editor', () => {
  it.each<ToolApprovalCondition>([
    { path: 'metadata.namespace', operator: 'equals', value: 'production' },
    { path: 'replicas', operator: 'equals', value: 3 },
    { path: 'dry_run', operator: 'equals', value: false },
    { path: 'selector', operator: 'equals', value: { app: 'api' } },
    { path: 'nullable', operator: 'equals', value: null },
    { path: 'resource.name', operator: 'regex', value: 'prod-.*' },
  ])('preserves typed condition %#', (condition) => {
    expect(parseCondition(conditionDraft(condition))).toEqual(condition);
  });

  it('rejects an invalid numeric value before saving', () => {
    expect(() => parseCondition({ path: 'replicas', operator: 'equals', valueType: 'number', value: 'three' })).toThrow(
      '"replicas" needs a valid number.'
    );
  });

  it('requires a field path', () => {
    expect(() => parseCondition({ path: '  ', operator: 'equals', valueType: 'text', value: 'value' })).toThrow(
      'Every argument condition needs a field path.'
    );
  });
});

describe('regex patterns with a wildcard before the verb', () => {
  it.each(['kubectl.*rollout restart.*', 'kubectl .* rollout restart .*', 'kubectl.+delete'])('warns for %s', (pattern) => {
    expect(wildcardBeforeVerb(pattern)).toBe(true);
  });

  it.each(['kubectl rollout restart .*', 'kubectl get pods -n (staging|dev)', 'kubectl (-n \\S+ )?rollout restart .*'])('accepts %s', (pattern) => {
    expect(wildcardBeforeVerb(pattern)).toBe(false);
  });
});
