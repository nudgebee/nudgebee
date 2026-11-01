import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ExecutedTargetChip, { parseExecutedTarget, parseResolvedIntegration, targetKey } from '../ExecutedTargetChip';

describe('executed target', () => {
  it('reads where a step ran from persisted JSON metadata', () => {
    const metadata = JSON.stringify({
      nb_executed_target: { provider: 'kubernetes', name: 'k8s-prod', account_id: 'prod-id' },
      planner_iteration: 2,
    });
    expect(parseExecutedTarget(metadata)).toEqual({ provider: 'kubernetes', name: 'k8s-prod', account_id: 'prod-id' });
  });

  it('ignores steps that ran on the conversation account or have bad metadata', () => {
    expect(parseExecutedTarget(null)).toBeNull();
    expect(parseExecutedTarget('{"planner_iteration":1}')).toBeNull();
    expect(parseExecutedTarget('not json')).toBeNull();
    expect(parseExecutedTarget({ nb_executed_target: { provider: 'aws' } })).toBeNull();
  });

  it('renders the target name only when there is one', () => {
    const { rerender } = render(<ExecutedTargetChip metadata={{ nb_executed_target: { provider: 'aws', name: 'aws-prod' } }} />);
    expect(screen.getByTestId('executed-target-chip')).toHaveTextContent('aws-prod');
    rerender(<ExecutedTargetChip metadata={null} />);
    expect(screen.queryByTestId('executed-target-chip')).toBeNull();
  });
});

describe('target with integration', () => {
  const k8sProd = { provider: 'kubernetes', name: 'k8s-prod', account_id: 'prod-id' };

  it('reads the integration and drops values that are not integration names', () => {
    expect(parseResolvedIntegration('{"nb_resolved_target":"prod"}')).toBe('prod');
    expect(parseResolvedIntegration({ nb_resolved_target: 'NoConfigToolsFound' })).toBeNull();
    expect(parseResolvedIntegration({ nb_resolved_target: 'Agent' })).toBeNull();
    expect(parseResolvedIntegration({ nb_resolved_target: '  ' })).toBeNull();
    expect(parseResolvedIntegration({ nb_resolved_target: { name: 'x' } })).toBeNull();
    expect(parseResolvedIntegration(null)).toBeNull();
  });

  it('shows account and integration together for a step on another account', async () => {
    const user = userEvent.setup();
    render(<ExecutedTargetChip metadata={{ nb_executed_target: k8sProd, nb_resolved_target: 'prod' }} />);
    const chip = screen.getByTestId('executed-target-chip');
    expect(chip).toHaveTextContent('k8s-prod · prod');
    expect(chip.querySelector('[data-tone]')).toHaveAttribute('data-tone', 'info');
    await user.hover(chip);
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Ran on the cluster k8s-prod, using the prod integration');
  });

  it('shows the integration alone, in grey, for a step on the conversation account', async () => {
    const user = userEvent.setup();
    render(<ExecutedTargetChip metadata={{ nb_resolved_target: 'prod-pg', nb_resolved_by: 'followup' }} />);
    const chip = screen.getByTestId('executed-target-chip');
    expect(chip).toHaveTextContent(/^prod-pg$/);
    expect(chip.querySelector('[data-tone]')).toHaveAttribute('data-tone', 'neutral');
    await user.hover(chip);
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Used the prod-pg integration (you picked it)');
  });

  it('does not repeat an integration named after the cluster', () => {
    render(<ExecutedTargetChip metadata={{ nb_executed_target: k8sProd, nb_resolved_target: 'k8s-prod' }} />);
    expect(screen.getByTestId('executed-target-chip')).toHaveTextContent(/^k8s-prod$/);
  });

  it('renders nothing for a placeholder integration on the conversation account', () => {
    render(<ExecutedTargetChip metadata={{ nb_resolved_target: 'NoConfigToolsFound' }} />);
    expect(screen.queryByTestId('executed-target-chip')).toBeNull();
  });

  it('keys calls by account and integration, so a switch from prod-pg to prod is a different target', () => {
    const a = { nb_executed_target: k8sProd, nb_resolved_target: 'prod-pg' };
    const b = { nb_executed_target: k8sProd, nb_resolved_target: 'prod' };
    expect(targetKey(a)).not.toBe(targetKey(b));
    expect(targetKey(JSON.stringify(b))).toBe(targetKey(b));
    expect(targetKey({ nb_resolved_target: 'Agent' })).toBe('');
  });
});

describe('target wording', () => {
  const hoverText = async (ui) => {
    const user = userEvent.setup();
    render(ui);
    await user.hover(screen.getByTestId('executed-target-chip'));
    return (await screen.findByRole('tooltip')).textContent;
  };

  it('names a VM account as such', async () => {
    const metadata = { nb_executed_target: { provider: 'vm', name: 'vulnerable-vms-test' } };
    expect(await hoverText(<ExecutedTargetChip metadata={metadata} />)).toContain('Ran on the VM account vulnerable-vms-test');
  });

  it('says "Tried on" for a failed step', async () => {
    const metadata = { nb_executed_target: { provider: 'kubernetes', name: 'k8s-prod' }, nb_resolved_target: 'prod-pg' };
    expect(await hoverText(<ExecutedTargetChip metadata={metadata} status='error' />)).toContain(
      'Tried on the cluster k8s-prod, using the prod-pg integration'
    );
  });

  it('says "Tried" for a failed own-account step', async () => {
    expect(await hoverText(<ExecutedTargetChip metadata={{ nb_resolved_target: 'dev-pg' }} status='FAILED' />)).toContain(
      'Tried the dev-pg integration'
    );
  });

  it('keeps "Ran on" for a step that succeeded or partly succeeded', async () => {
    const metadata = { nb_executed_target: { provider: 'kubernetes', name: 'k8s-prod' } };
    expect(await hoverText(<ExecutedTargetChip metadata={metadata} status='completed_with_errors' />)).toContain('Ran on the cluster k8s-prod');
  });
});
