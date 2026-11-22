import React from 'react';
import { render, screen } from '@testing-library/react';
import ApprovedByRuleChip, { parseApprovedByRule } from '../ApprovedByRuleChip';

describe('ApprovedByRuleChip', () => {
  it('reads the approving rule from object or JSON metadata', () => {
    const rule = { rule_id: 'r1', rule_name: 'kubectl_rollout_restart', rule_revision: 2 };
    expect(parseApprovedByRule({ nb_approved_by_rule: rule })).toEqual(rule);
    expect(parseApprovedByRule(JSON.stringify({ nb_approved_by_rule: rule }))).toEqual(rule);
    expect(parseApprovedByRule({ nb_executed_target: { name: 'k8s-dev' } })).toBeNull();
    expect(parseApprovedByRule('not json')).toBeNull();
  });

  it('shows only for auto-approved steps', () => {
    const { rerender } = render(<ApprovedByRuleChip metadata={{ nb_approved_by_rule: { rule_name: 'restart', rule_revision: 1 } }} />);
    expect(screen.getByTestId('approved-by-rule-chip')).toHaveTextContent('Auto-approved');
    rerender(<ApprovedByRuleChip metadata={{}} />);
    expect(screen.queryByTestId('approved-by-rule-chip')).toBeNull();
  });
});
