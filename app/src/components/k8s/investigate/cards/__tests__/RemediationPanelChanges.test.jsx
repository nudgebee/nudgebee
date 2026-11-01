import { render, screen } from '@testing-library/react';
import { OptionalHardening, RequiredChangeDetails, afterOf, noActionsText, runnableActionCount } from '../RemediationPanel';

// A fix no command applies (a chart value, a node pool) is shown as the change to make, not as the
// event's code diff.
describe('RequiredChangeDetails', () => {
  it('names where the change is made, its target and the change', () => {
    render(
      <RequiredChangeDetails
        change={{ where: 'deployment_source', target: 'Helm release eval-k4 values', change: 'image tag 1.27-alpine-doesnotexist -> 1.27-alpine' }}
      />
    );
    expect(screen.getByText('Change required — Deployment source (chart or GitOps values)')).toBeInTheDocument();
    expect(screen.getByText('Helm release eval-k4 values')).toBeInTheDocument();
    expect(screen.getByText('image tag 1.27-alpine-doesnotexist -> 1.27-alpine')).toBeInTheDocument();
  });

  it('falls back to a generic heading for an unknown location', () => {
    render(<RequiredChangeDetails change={{ where: 'elsewhere', change: 'do the thing' }} />);
    expect(screen.getByText('Change required — Change required')).toBeInTheDocument();
  });
});

// Hardening is labelled as not required, so a recovered one-off does not read as needing a fix.
describe('OptionalHardening', () => {
  it('labels the items as not required and shows each one', () => {
    render(
      <OptionalHardening
        items={[
          { target: 'lambda_function.py', change: 'reject malformed batches', reason: 'a future bad batch fails cleanly' },
          { change: 'alert on duration at 80% of the timeout' },
        ]}
      />
    );
    expect(screen.getByTestId('remediation-optional-hardening')).toHaveTextContent('Optional hardening — not required to resolve this incident');
    expect(screen.getByText('reject malformed batches', { exact: false })).toBeInTheDocument();
    expect(screen.getByText('alert on duration at 80% of the timeout')).toBeInTheDocument();
  });
});

describe('afterOf', () => {
  const actions = [
    { action: 'Patch web config', execute_command: 'kubectl patch configmap web -n demo -p x' },
    { action: 'Restart web', after: 'Patch web config', execute_command: 'kubectl rollout restart deployment web -n demo' },
    { action: 'Add API_TOKEN key', required_change: { target: 'Secret demo/api', change: 'add key' } },
    { action: 'Restart api', after: 'Add API_TOKEN key', execute_command: 'kubectl rollout restart deployment api -n demo' },
  ];

  it('is unapplied until the prerequisite command has run, then applied', () => {
    expect(afterOf(actions[1], actions, {})).toEqual({ name: 'Patch web config', applied: false });
    expect(afterOf(actions[1], actions, { 'kubectl patch configmap web -n demo -p x': '2026-09-30T09:00:00Z' })).toEqual({
      name: 'Patch web config',
      applied: true,
    });
  });

  it('cannot see a required change being made, and ignores actions without a known prerequisite', () => {
    expect(afterOf(actions[3], actions, {})).toEqual({ name: 'Add API_TOKEN key', applied: false });
    expect(afterOf(actions[0], actions, {})).toBeNull();
    expect(afterOf({ after: 'nothing like this' }, actions, {})).toBeNull();
  });
});

// A plan with no actions explains itself once: the root-cause card already carries the summary.
describe('noActionsText', () => {
  it('is empty when the root-cause card has already printed the summary', () => {
    expect(noActionsText({ root_cause: 'Undetermined', summary: 'Evidence is partial, so no action is recommended.' })).toBe('');
  });

  it('is the summary when there is no root-cause card to carry it', () => {
    expect(noActionsText({ summary: 'Evidence is partial, so no action is recommended.' })).toBe('Evidence is partial, so no action is recommended.');
  });

  it('falls back to a plain sentence when the plan has no summary', () => {
    expect(noActionsText({ root_cause: 'Undetermined' })).toBe('No automated remediation is recommended: the root cause is not conclusive enough.');
    expect(noActionsText(null)).toBe('No automated remediation is recommended: the root cause is not conclusive enough.');
  });
});

// The tab count is what the operator can run from the tab, not every fix the plan describes.
describe('runnableActionCount', () => {
  it('counts actions with a command or an alert rule change, and not a change to make elsewhere', () => {
    const plan = {
      actions: [
        {
          action: 'Fix unbounded cache',
          kind: 'fix',
          execute_command: '',
          required_change: { where: 'code', target: 'cache.py', change: 'bound the cache' },
        },
        { action: 'Raise memory limit', kind: 'mitigation', execute_command: 'kubectl set resources deployment web -n demo --limits=memory=1Gi' },
        { action: 'Lengthen the alert window', kind: 'fix', alert_rule_change: { operation: 'edit', duration: '10m' } },
        { action: 'Blank command', execute_command: '   ' },
      ],
    };
    expect(runnableActionCount(plan)).toBe(2);
  });

  it('is zero when there is no plan', () => {
    expect(runnableActionCount(null)).toBe(0);
    expect(runnableActionCount({ actions: [] })).toBe(0);
    expect(runnableActionCount('could not load')).toBe(0);
  });
});
