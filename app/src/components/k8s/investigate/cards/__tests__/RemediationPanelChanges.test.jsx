import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import apiKubernetes from '@api1/kubernetes';
import {
  ActionCard,
  OutcomeCheck,
  latestOutcome,
  OptionalHardening,
  RequiredChangeDetails,
  afterOf,
  noActionsText,
  runnableActionCount,
} from '../RemediationPanel';
import { appliedCommands, decisionsByStep } from '../../resolutionStatus';
import FollowupCheck, { followupText } from '../FollowupCheck';

jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { executeRemediationCommand: jest.fn() } }));

// confirmButton is the dialog's own button of that name, not the card's.
const confirmButton = async (name) => within(await screen.findByRole('dialog')).getByRole('button', { name });

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

  it('follows the server: applied only once that step succeeded on this plan version', () => {
    const done = { 'v2|0:apply': { status: 'Success' } };
    expect(afterOf(actions[1], actions, {}, done, 'v2')).toEqual({ name: 'Patch web config', applied: true });
    expect(afterOf(actions[1], actions, { 'kubectl patch configmap web -n demo -p x': 'x' }, {}, 'v2')).toEqual({
      name: 'Patch web config',
      applied: false,
    });
    expect(afterOf(actions[1], actions, {}, { 'v2|0:apply': { status: 'Declined' } }, 'v2').applied).toBe(false);
    expect(afterOf(actions[1], actions, {}, done, 'v3').applied).toBe(false);
  });

  it('is unapplied until a required change is marked made, and ignores actions without a known prerequisite', () => {
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
        { action: 'Restart runner', execute_command: 'kubectl rollout restart deployment runner -n x', delivery: 'operator' },
        { action: 'Set env', execute_command: 'kubectl set env deployment web -n demo A=b', delivery: 'operator', delivery_retry: true },
      ],
    };
    expect(runnableActionCount(plan)).toBe(3);
  });

  it('is zero when there is no plan', () => {
    expect(runnableActionCount(null)).toBe(0);
    expect(runnableActionCount({ actions: [] })).toBe(0);
    expect(runnableActionCount('could not load')).toBe(0);
  });
});

describe('ActionCard delivery', () => {
  const base = {
    index: 1,
    accountId: 'acc',
    eventId: 'evt',
    canRun: true,
    appliedAt: '',
    onExecuted: () => {},
    codeFix: null,
    alertControls: null,
    onAlertRuleApplied: () => {},
  };

  it('shows an operator step as a command to run yourself, with the reason and no Run button', () => {
    render(
      <ActionCard
        {...base}
        action={{
          action: 'Restart agent runner',
          kind: 'fix',
          delivery: 'operator',
          delivery_reason: 'NudgeBee runs commands on this cluster through its agent, and the agent is not connected right now.',
          execute_command: 'kubectl rollout restart deployment agent-runner -n nudgebee',
          verify_command: 'kubectl rollout status deployment agent-runner -n nudgebee',
        }}
      />
    );
    expect(screen.getByText(/Run this in your terminal/)).toBeInTheDocument();
    expect(screen.getByText(/the agent is not connected right now/)).toBeInTheDocument();
    expect(screen.queryByTestId('remediation-run-action-2')).toBeNull();
    expect(screen.queryByTestId('remediation-run-execute')).toBeNull();
    expect(screen.getByTestId('remediation-mark-done-2')).toHaveTextContent('I ran this');
  });

  it('keeps Run on a step refused permission earlier, since the permission may have been granted', () => {
    render(
      <ActionCard
        {...base}
        action={{
          action: 'Raise limit',
          kind: 'fix',
          delivery: 'operator',
          delivery_retry: true,
          delivery_reason: 'NudgeBee was refused permission to run "set" on this account 10 min ago: verb "set" not in read-only allowlist',
          execute_command: 'kubectl set resources deployment api -n shop --limits=memory=1Gi',
        }}
      />
    );
    expect(screen.getByText(/Run this in your terminal/)).toBeInTheDocument();
    expect(screen.getByText(/granted that permission since, run it here/)).toBeInTheDocument();
    expect(screen.getByTestId('remediation-run-action-2')).toBeInTheDocument();
    expect(screen.getByTestId('remediation-run-execute')).toBeInTheDocument();
    expect(screen.getByTestId('remediation-mark-done-2')).toHaveTextContent('I ran this');
  });

  it('keeps Run for a run step', () => {
    render(
      <ActionCard
        {...base}
        action={{
          action: 'Raise limit',
          kind: 'fix',
          delivery: 'run',
          execute_command: 'kubectl set resources deployment api -n shop --limits=memory=1Gi',
        }}
      />
    );
    expect(screen.getByTestId('remediation-run-action-2')).toBeInTheDocument();
    expect(screen.queryByText(/Run this in your terminal/)).toBeNull();
  });

  it('lets a source change be marked done, which unlocks the step after it', () => {
    const actions = [
      { action: 'Update auth secret', required_change: { target: 'Secret agent-auth', change: 'registered key' } },
      { action: 'Restart runner', after: 'Update auth secret', execute_command: 'kubectl rollout restart deployment runner -n x' },
    ];
    expect(afterOf(actions[1], actions, {})).toEqual({ name: 'Update auth secret', applied: false });
    expect(afterOf(actions[1], actions, { 'change:Update auth secret': '2026-10-05T10:00:00Z' })).toEqual({
      name: 'Update auth secret',
      applied: true,
    });
    render(<ActionCard {...base} action={{ ...actions[0], kind: 'fix', delivery: 'source' }} />);
    expect(screen.getByTestId('remediation-mark-done-2')).toHaveTextContent('I made this change');
  });

  it('shows why a step could not be marked done', async () => {
    const spy = jest
      .spyOn(apiKubernetes, 'executeRemediationCommand')
      .mockRejectedValue(new Error("this step is not part of the event's remediation plan"));
    render(
      <ActionCard
        {...base}
        action={{
          action: 'Update auth secret',
          kind: 'fix',
          delivery: 'source',
          required_change: { target: 'Secret agent-auth', change: 'registered key' },
        }}
      />
    );
    fireEvent.click(screen.getByTestId('remediation-mark-done-2'));
    expect(await screen.findByTestId('remediation-mark-done-error-2')).toHaveTextContent('not part of the event');
    spy.mockRestore();
  });

  it('shows a source change as applied once marked, without offering it again', () => {
    render(
      <ActionCard
        {...base}
        appliedAt='2026-10-05T10:00:00Z'
        action={{
          action: 'Update auth secret',
          kind: 'fix',
          delivery: 'source',
          required_change: { target: 'Secret agent-auth', change: 'registered key' },
        }}
      />
    );
    expect(screen.queryByTestId('remediation-mark-done-2')).toBeNull();
    expect(screen.getByText(/Applied/)).toBeInTheDocument();
  });
});

describe('OutcomeCheck', () => {
  const check = {
    command: 'kubectl rollout status deployment/web -n demo --timeout=90s',
    describes: 'deployment web in demo has all its pods updated and ready',
  };

  it('hands the fresh result up, and shows why a check could not run', async () => {
    const onChecked = jest.fn();
    const res = { passed: true, command: check.command, at: '2026-10-05T10:05:00Z' };
    apiKubernetes.executeRemediationCommand
      .mockResolvedValueOnce(res)
      .mockRejectedValueOnce(new Error("remediation: this is not the plan's outcome check"));
    render(<OutcomeCheck check={check} accountId='acc' eventId='evt' canRun lastOutcome={null} onChecked={onChecked} />);

    fireEvent.click(screen.getByTestId('remediation-outcome-run'));
    await waitFor(() => expect(onChecked).toHaveBeenCalledWith(res));
    expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledWith('acc', check.command, 'evt', undefined, 'outcome');

    fireEvent.click(screen.getByTestId('remediation-outcome-run'));
    expect(await screen.findByText("remediation: this is not the plan's outcome check")).toBeInTheDocument();
    expect(onChecked).toHaveBeenCalledTimes(1);
  });

  it('shows the last recorded outcome of this check, three-valued', () => {
    const card = (outcome) => (
      <OutcomeCheck check={check} accountId='acc' eventId='evt' canRun lastOutcome={{ command: check.command, ...outcome }} onChecked={() => {}} />
    );
    const { rerender } = render(card({ passed: false, at: '2026-10-05T10:00:00Z', output: 'x'.repeat(250) }));
    expect(screen.getByText('Pods not ready')).toBeInTheDocument();
    expect(screen.getByText(/x{200}\.\.\./)).toBeInTheDocument();
    expect(screen.getByText(`Checks that ${check.describes}.`)).toBeInTheDocument();
    rerender(card({ passed: true, at: '2026-10-05T10:05:00Z' }));
    expect(screen.getByText('Pods ready')).toBeInTheDocument();
    rerender(card({ passed: null, at: '2026-10-05T10:06:00Z' }));
    expect(screen.getByText('Could not tell')).toBeInTheDocument();
    rerender(card({ passed: true, at: '2026-10-05T10:07:00Z', command: 'kubectl rollout status deployment/old -n demo --timeout=90s' }));
    expect(screen.queryByText('Pods ready')).not.toBeInTheDocument();
    expect(screen.getByTestId('remediation-outcome-run')).toHaveTextContent('Check now');
  });

  it('labels an alarm check by the alarm state', () => {
    const alarm = { command: 'aws cloudwatch describe-alarms --alarm-names a', expect: 'OK', describes: 'CloudWatch alarm a is back to OK' };
    const card = (passed) => (
      <OutcomeCheck
        check={alarm}
        accountId='acc'
        eventId='evt'
        canRun
        lastOutcome={{ command: alarm.command, passed, at: '2026-10-05T10:00:00Z' }}
        onChecked={() => {}}
      />
    );
    const { rerender } = render(card(true));
    expect(screen.getByText('Alarm OK')).toBeInTheDocument();
    rerender(card(false));
    expect(screen.getByText('Alarm firing')).toBeInTheDocument();
    rerender(card(null));
    expect(screen.getByText('Could not tell')).toBeInTheDocument();
  });

  it('restores the newest outcome across attempts', () => {
    const rows = [
      { type: 'CommandExecution', data: { outcome: { passed: false, at: '2026-10-05T10:00:00Z' } } },
      { type: 'CommandExecution', data: { outcome: { passed: true, at: '2026-10-05T10:09:00Z' } } },
      { type: 'PullRequest', data: { outcome: { passed: false, at: '2026-10-05T11:00:00Z' } } },
    ];
    expect(latestOutcome(rows)).toEqual({ passed: true, at: '2026-10-05T10:09:00Z' });
    expect(latestOutcome([])).toBeNull();
  });
});

// A mitigation that gives something up states its cost and carries no likely-to-resolve score.
describe('ActionCard trade-off', () => {
  const base = { index: 0, accountId: 'acc', eventId: 'evt', canRun: true };

  it('shows what the step gives up instead of a score', () => {
    render(
      <ActionCard
        {...base}
        action={{
          action: 'Scale redis-replicas to 1',
          kind: 'mitigation',
          confidence: 45,
          trade_off: 'reverses a StatefulSet scaled to zero on purpose',
          execute_command: 'kubectl scale statefulset redis-replicas -n oss --replicas=1',
        }}
      />
    );
    expect(screen.getByTestId('remediation-trade-off-1')).toHaveTextContent('Before you run: reverses a StatefulSet scaled to zero on purpose');
    expect(screen.queryByText(/likely to resolve/)).toBeNull();
  });

  it('keeps the score on a fix that states a trade-off', () => {
    render(
      <ActionCard
        {...base}
        action={{
          action: 'Scale redis',
          kind: 'fix',
          confidence: 80,
          trade_off: 'scales statefulset/redis up from 0',
          execute_command: 'kubectl scale statefulset redis -n oss --replicas=1',
        }}
      />
    );
    expect(screen.getByTestId('remediation-trade-off-1')).toHaveTextContent('Before you run: scales statefulset/redis up from 0');
    expect(screen.getByText('80% likely to resolve')).toBeInTheDocument();
  });

  it('keeps the score for a step that gives nothing up', () => {
    render(
      <ActionCard
        {...base}
        action={{
          action: 'Raise limit',
          kind: 'fix',
          confidence: 90,
          execute_command: 'kubectl set resources deployment api -n shop --limits=memory=1Gi',
        }}
      />
    );
    expect(screen.getByText('90% likely to resolve')).toBeInTheDocument();
    expect(screen.queryByTestId('remediation-trade-off-1')).toBeNull();
  });
});

// Each step is decided once per plan version. The card shows the decision recorded on its own step, keeps
// Run disabled while a run is in flight, offers no Run once the step is decided, and asks before running
// a command again whose last run has an unknown outcome.
describe('ActionCard decisions', () => {
  const command = 'kubectl rollout restart deployment api -n shop';
  const base = {
    index: 0,
    accountId: 'acc',
    eventId: 'evt',
    canRun: true,
    appliedAt: '',
    onExecuted: () => {},
    codeFix: null,
    alertControls: null,
    onAlertRuleApplied: () => {},
    action: { action: 'Restart api', kind: 'mitigation', delivery: 'run', execute_command: command },
  };
  const row = (status, data = {}) => ({ type: 'CommandExecution', status, data: { plan_version: 'v1', step: '0:apply', ...data } });

  afterEach(() => apiKubernetes.executeRemediationCommand.mockReset());

  it('keeps Run disabled while the step is running', () => {
    render(<ActionCard {...base} decision={row('InProgress')} />);
    expect(screen.getByTestId('remediation-run-action-1')).toBeDisabled();
    expect(screen.getByTestId('remediation-run-execute')).toBeDisabled();
    expect(screen.getByTestId('remediation-decision-1')).toHaveTextContent('Running');
  });

  it('offers no Run once the step is decided', () => {
    for (const decision of [row('Failed', { exit_code_reported: true }), row('Declined'), row('Success', { manual: true })]) {
      const { unmount } = render(<ActionCard {...base} decision={decision} />);
      expect(screen.queryByTestId('remediation-run-action-1')).toBeNull();
      expect(screen.queryByTestId('remediation-run-execute')).toBeNull();
      unmount();
    }
  });

  // Review of #41596: a helm/argocd/shell step that ran without an exit code has no Applied chip, so after a
  // reload it keeps its status here.
  it('shows a success with no exit code as unverified', () => {
    render(<ActionCard {...base} decision={row('Success', { exit_code_reported: false })} />);
    expect(screen.getByTestId('remediation-decision-1')).toHaveTextContent('Outcome unverified');
  });

  // #40734 review: the refusal came only after a click. A declined step offers no command to run, and a
  // step that runs after one not yet succeeded on this plan has its Run disabled, saying why.
  it('offers no verify run on a declined step, and no Run before the step it follows', () => {
    const withVerify = { ...base.action, verify_command: 'kubectl get deployment api -n shop' };
    const { unmount } = render(<ActionCard {...base} action={withVerify} decision={row('Declined')} />);
    expect(screen.queryByTestId('remediation-run-verify')).toBeNull();
    unmount();
    render(<ActionCard {...base} action={{ ...base.action, after: 'Patch config' }} after={{ name: 'Patch config', applied: false }} />);
    expect(screen.getByTestId('remediation-run-action-1')).toBeDisabled();
    expect(screen.queryByTestId('remediation-run-execute')).toBeNull();
  });

  it('says how to try a failed step again, and does not offer to run it here', () => {
    render(
      <ActionCard
        {...base}
        action={{ ...base.action, delivery: 'operator', delivery_retry: true, delivery_reason: 'NudgeBee was refused permission to run "rollout"' }}
        decision={row('Failed', { exit_code_reported: true })}
      />
    );
    expect(screen.getByTestId('remediation-step-failed-1')).toHaveTextContent('regenerate the plan for a new proposal');
    expect(screen.queryByText(/run it here/)).toBeNull();
    expect(screen.queryByTestId('remediation-run-action-1')).toBeNull();
  });

  it('keeps Run when the last run provably never ran', () => {
    render(<ActionCard {...base} decision={row('Failed', { not_sent: true })} />);
    expect(screen.getByTestId('remediation-run-action-1')).toBeEnabled();
  });

  it('asks before running again after an unknown outcome, then sends the acknowledgement', async () => {
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Success', success: true, exit_code: 0 });
    render(<ActionCard {...base} decision={row('Unknown')} />);
    expect(screen.getByTestId('remediation-decision-1')).toHaveTextContent('Outcome unknown');
    expect(screen.queryByTestId('remediation-run-execute')).toBeNull();
    expect(screen.queryByTestId('remediation-decline-1')).toBeNull();
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    expect(apiKubernetes.executeRemediationCommand).not.toHaveBeenCalled();
    expect(await screen.findByTestId('remediation-rerun-warning-1')).toHaveTextContent(
      'It may have run, and its effects may still be in progress. Check the cluster before running it again.'
    );
    fireEvent.click(screen.getByText('I checked, run it again'));
    await waitFor(() =>
      expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledWith('acc', command, 'evt', undefined, 'execute', undefined, undefined, {
        acknowledgeUnknown: true,
        planVersion: undefined,
      })
    );
  });

  it('offers the acknowledged re-run when the server refuses over an unknown outcome elsewhere', async () => {
    apiKubernetes.executeRemediationCommand
      .mockRejectedValueOnce(
        new Error(
          'remediation: the last run of this command on this account has an unknown outcome. Check the cluster before running it again, then confirm that you checked.'
        )
      )
      .mockResolvedValueOnce({ status: 'Success', success: true, exit_code: 0 });
    render(<ActionCard {...base} />);
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('Run'));
    await waitFor(() => expect(screen.getByTestId('remediation-run-action-1')).toHaveTextContent('Run again…'));
    // This step has no run of its own, so declining it stays possible.
    expect(screen.getByTestId('remediation-decline-1')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('I checked, run it again'));
    await waitFor(() =>
      expect(apiKubernetes.executeRemediationCommand).toHaveBeenLastCalledWith('acc', command, 'evt', undefined, 'execute', undefined, undefined, {
        acknowledgeUnknown: true,
        planVersion: undefined,
      })
    );
    await waitFor(() => expect(screen.getByTestId('remediation-run-action-1')).not.toHaveTextContent('Run again…'));
  });

  it('shows a server refusal as not run, with the reason under its own label', async () => {
    apiKubernetes.executeRemediationCommand.mockRejectedValueOnce(new Error('remediation: the plan changed since you reviewed it. Review it again.'));
    render(<ActionCard {...base} />);
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('Run'));
    expect(await screen.findByText('Not run')).toBeInTheDocument();
    expect(screen.getByText('Why it did not run')).toBeInTheDocument();
    expect(screen.getByText('the plan changed since you reviewed it. Review it again.')).toBeInTheDocument();
    expect(screen.queryByText('Failed')).toBeNull();
    expect(screen.queryByText('stderr')).toBeNull();
  });

  it('keeps any other error a failure, since the command may have gone out', async () => {
    apiKubernetes.executeRemediationCommand.mockRejectedValueOnce(new Error('Upstream unreachable for ai_remediation_execute'));
    render(<ActionCard {...base} />);
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('Run'));
    expect(await screen.findByText('Failed')).toBeInTheDocument();
    expect(screen.getByText('Details')).toBeInTheDocument();
    expect(screen.queryByText('stderr')).toBeNull();
  });

  it('keeps a plain Run while the unknown outcome must still wait', async () => {
    apiKubernetes.executeRemediationCommand.mockRejectedValueOnce(
      new Error(
        'remediation: the last run of this command on this account has an unknown outcome. It may have run, and its effects may still be in progress. Check the cluster before running it again; you can run it again in 3 min.'
      )
    );
    render(<ActionCard {...base} />);
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('Run'));
    expect(await screen.findByText(/you can run it again in 3 min/)).toBeInTheDocument();
    expect(screen.getByTestId('remediation-run-action-1')).toHaveTextContent('Run');
    expect(screen.getByTestId('remediation-run-action-1')).not.toHaveTextContent('Run again…');
  });

  it('shows a run without an exit code as an unknown outcome and does not chain its verify', async () => {
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Unknown', success: false, error: 'No result came back.' });
    render(<ActionCard {...base} action={{ ...base.action, verify_command: 'kubectl rollout status deployment api -n shop' }} />);
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('Run'));
    expect(await screen.findByText('Outcome unknown')).toBeInTheDocument();
    expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledTimes(1);
  });
});

// A rollback is a step of its own. When its last run has an unknown outcome it is not silently lost: the
// card says so and offers the same acknowledged re-run as the action itself.
describe('ActionCard rollback decisions', () => {
  const rollback = 'kubectl rollout undo deployment api -n shop';
  const base = {
    index: 0,
    accountId: 'acc',
    eventId: 'evt',
    canRun: true,
    appliedAt: '',
    onExecuted: () => {},
    codeFix: null,
    alertControls: null,
    onAlertRuleApplied: () => {},
    action: {
      action: 'Restart api',
      kind: 'mitigation',
      delivery: 'run',
      execute_command: 'kubectl rollout restart deployment api -n shop',
      rollback_command: rollback,
    },
  };
  const rollbackRow = (status, data = {}) => ({ type: 'CommandExecution', status, data: { plan_version: 'v1', step: '0:rollback', ...data } });

  afterEach(() => apiKubernetes.executeRemediationCommand.mockReset());

  it('asks before running an unknown rollback again, then sends the acknowledgement for the rollback', async () => {
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Success', success: true, exit_code: 0 });
    render(<ActionCard {...base} rollbackDecision={rollbackRow('Unknown')} />);
    expect(screen.getByTestId('remediation-rollback-decision-1')).toHaveTextContent('Outcome unknown');
    expect(screen.queryByTestId('remediation-run-rollback')).toBeNull();
    fireEvent.click(screen.getByTestId('remediation-rollback-run-again-1'));
    expect(apiKubernetes.executeRemediationCommand).not.toHaveBeenCalled();
    expect(await screen.findByTestId('remediation-rerun-warning-1')).toHaveTextContent('It may have run, and its effects may still be in progress.');
    fireEvent.click(screen.getByText('I checked, run it again'));
    await waitFor(() =>
      expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledWith('acc', rollback, 'evt', undefined, 'rollback', undefined, undefined, {
        acknowledgeUnknown: true,
        planVersion: undefined,
      })
    );
  });

  it('cannot send a second acknowledged rollback while the first is in flight, and the dialog keeps its command', async () => {
    let finish;
    apiKubernetes.executeRemediationCommand.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    render(<ActionCard {...base} rollbackDecision={rollbackRow('Unknown')} />);
    fireEvent.click(screen.getByTestId('remediation-rollback-run-again-1'));
    await screen.findByTestId('remediation-rerun-warning-1');
    fireEvent.click(screen.getByText('I checked, run it again'));
    // While the dialog closes it still shows the rollback it confirmed, never the action's command.
    const closing = screen.queryByRole('dialog');
    if (closing) {
      expect(within(closing).queryByText(base.action.execute_command)).toBeNull();
      expect(within(closing).getByText(rollback)).toBeInTheDocument();
    }
    await waitFor(() => expect(screen.getByTestId('remediation-rollback-run-again-1')).toBeDisabled());
    // A second click, confirmed if it gets that far, sends nothing while the first is out.
    fireEvent.click(screen.getByTestId('remediation-rollback-run-again-1'));
    const again = screen.queryByText('I checked, run it again');
    if (again) fireEvent.click(again);
    expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledTimes(1);
    finish({ status: 'Success', success: true, exit_code: 0 });
    await waitFor(() => expect(screen.getByTestId('remediation-rollback-run-again-1')).toBeEnabled());
  });

  it('keeps the rollback disabled while it runs and offers nothing once it is decided', () => {
    const { unmount } = render(<ActionCard {...base} rollbackDecision={rollbackRow('InProgress')} />);
    expect(screen.getByTestId('remediation-run-rollback')).toBeDisabled();
    expect(screen.queryByTestId('remediation-rollback-run-again-1')).toBeNull();
    unmount();
    render(<ActionCard {...base} rollbackDecision={rollbackRow('Success', { exit_code_reported: true })} />);
    expect(screen.queryByTestId('remediation-run-rollback')).toBeNull();
    expect(screen.queryByTestId('remediation-rollback-run-again-1')).toBeNull();
  });

  it('keeps the rollback runnable when its last run never ran', () => {
    render(<ActionCard {...base} rollbackDecision={rollbackRow('Failed', { not_sent: true })} />);
    expect(screen.getByTestId('remediation-run-rollback')).toBeEnabled();
  });
});

// Every command that changes the cluster runs only after the person confirms what runs and where; any
// step can be declined with a reason; checks that are not strict reads are the person's to run.
describe('ActionCard confirmation and decline', () => {
  const command = 'kubectl rollout restart deployment api -n shop';
  const undo = 'kubectl rollout undo deployment api -n shop';
  const base = {
    index: 0,
    accountId: 'acc',
    eventId: 'evt',
    canRun: true,
    appliedAt: '',
    onExecuted: () => {},
    codeFix: null,
    alertControls: null,
    onAlertRuleApplied: () => {},
    planVersion: 'pv-1',
    target: { account_id: 'acc', account_name: 'shop-prod' },
    action: {
      action: 'Restart api',
      kind: 'mitigation',
      delivery: 'run',
      execute_command: command,
      rollback_command: undo,
      trade_off: 'Restarts every api pod.',
    },
  };

  afterEach(() => apiKubernetes.executeRemediationCommand.mockReset());

  it('shows what runs and where before running, and sends the plan version it showed', async () => {
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Success', success: true, exit_code: 0 });
    render(<ActionCard {...base} />);
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    const dialog = await screen.findByTestId('remediation-confirm-1');
    expect(apiKubernetes.executeRemediationCommand).not.toHaveBeenCalled();
    expect(screen.getByTestId('remediation-confirm-target-1')).toHaveTextContent('Runs on: shop-prod');
    expect(within(dialog).getByText(command)).toBeInTheDocument();
    expect(within(dialog).getByText(undo)).toBeInTheDocument();
    expect(within(dialog).getByText('Mitigation')).toBeInTheDocument();
    expect(dialog).toHaveTextContent('Before you run: Restarts every api pod.');
    fireEvent.click(await confirmButton('Run'));
    await waitFor(() =>
      expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledWith('acc', command, 'evt', undefined, 'execute', undefined, undefined, {
        acknowledgeUnknown: false,
        planVersion: 'pv-1',
      })
    );
  });

  it('asks before the Execute and Rollback buttons too', async () => {
    render(<ActionCard {...base} />);
    fireEvent.click(screen.getByTestId('remediation-run-execute'));
    expect(await screen.findByTestId('remediation-confirm-1')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Cancel'));
    fireEvent.click(screen.getByTestId('remediation-run-rollback'));
    const dialog = await screen.findByTestId('remediation-confirm-1');
    expect(within(dialog).getByText(undo)).toBeInTheDocument();
    expect(apiKubernetes.executeRemediationCommand).not.toHaveBeenCalled();
  });

  it('records a decline with the reason and the plan version, running nothing', async () => {
    const onExecuted = jest.fn();
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Declined', success: false });
    render(<ActionCard {...base} onExecuted={onExecuted} />);
    fireEvent.click(screen.getByTestId('remediation-decline-1'));
    await screen.findByTestId('remediation-decline-dialog-1');
    fireEvent.change(document.getElementById('remediation-decline-reason-1'), { target: { value: '  not during the sale  ' } });
    fireEvent.click(await confirmButton('Decline'));
    await waitFor(() =>
      expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledWith('acc', command, 'evt', undefined, 'decline', undefined, 'Restart api', {
        planVersion: 'pv-1',
        reason: 'not during the sale',
      })
    );
    await waitFor(() => expect(onExecuted).toHaveBeenCalled());
  });

  it('offers nothing on a declined step', () => {
    render(<ActionCard {...base} decision={{ type: 'CommandExecution', status: 'Declined', data: { plan_version: 'pv-1', step: '0:apply' } }} />);
    expect(screen.getByTestId('remediation-decision-1')).toHaveTextContent('Declined');
    expect(screen.queryByTestId('remediation-run-action-1')).toBeNull();
    expect(screen.queryByTestId('remediation-decline-1')).toBeNull();
    expect(screen.queryByTestId('remediation-run-execute')).toBeNull();
  });

  it('asks for the plan again when a decision is refused', async () => {
    const onPlanStale = jest.fn();
    apiKubernetes.executeRemediationCommand.mockRejectedValue(new Error('remediation: the plan changed since you reviewed it. Review it again.'));
    render(<ActionCard {...base} onPlanStale={onPlanStale} />);
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('Run'));
    await waitFor(() => expect(onPlanStale).toHaveBeenCalled());
    expect(await screen.findByText(/the plan changed/)).toBeInTheDocument();
  });

  it('shows why a decision from a stale page was refused, without the server prefix, and reloads the page state', async () => {
    const onPlanStale = jest.fn();
    apiKubernetes.executeRemediationCommand.mockRejectedValue(
      new Error('remediation: this step of the plan was already decided: it was declined. A new proposal is needed to act on it again.')
    );
    render(<ActionCard {...base} action={{ ...base.action, delivery: 'operator' }} onPlanStale={onPlanStale} />);
    fireEvent.click(screen.getByTestId('remediation-mark-done-1'));
    const error = await screen.findByTestId('remediation-mark-done-error-1');
    expect(error).toHaveTextContent(/^this step of the plan was already decided: it was declined/);
    expect(onPlanStale).toHaveBeenCalled();
  });

  it('leaves a verify that is not a strict read to the person, and Run does not chain it', async () => {
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Success', success: true, exit_code: 0, stdout: 'ok' });
    const reason = 'this check is not a read NudgeBee runs without a confirmation. Check it yourself.';
    render(<ActionCard {...base} action={{ ...base.action, verify_command: 'helm status api -n shop', verify_reason: reason }} />);
    expect(screen.queryByTestId('remediation-run-verify')).toBeNull();
    expect(screen.getByTestId('remediation-verify-check-yourself-1')).toHaveTextContent('Check it yourself');
    fireEvent.click(screen.getByTestId('remediation-run-action-1'));
    fireEvent.click(await confirmButton('Run'));
    await waitFor(() => expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledTimes(1));
  });
});

describe('OutcomeCheck that is not a strict read', () => {
  it('shows why and offers no Check now', () => {
    render(
      <OutcomeCheck
        check={{
          command: 'aws cloudwatch describe-alarms --alarm-names cpu',
          reason: 'NudgeBee runs checks only as kubectl reads. Check it yourself.',
        }}
        accountId='acc'
        eventId='evt'
        canRun
        onChecked={() => {}}
      />
    );
    expect(screen.queryByTestId('remediation-outcome-run')).toBeNull();
    expect(screen.getByTestId('remediation-outcome-check-yourself')).toHaveTextContent('Check it yourself');
  });
});

describe('run rows', () => {
  const rows = [
    {
      type: 'CommandExecution',
      status: 'Unknown',
      type_reference_id: 'a',
      created_at: '2026-10-14T10:05:00',
      data: { plan_version: 'v1', step: '0:apply' },
    },
    {
      type: 'CommandExecution',
      status: 'Success',
      type_reference_id: 'a',
      created_at: '2026-10-14T10:00:00',
      data: { plan_version: 'v1', step: '0:apply' },
    },
    { type: 'CommandExecution', status: 'Success', type_reference_id: 'b', updated_at: '2026-10-14T09:00:00', data: { exit_code_reported: true } },
    {
      type: 'CommandExecution',
      status: 'Success',
      type_reference_id: 'change:Update secret',
      updated_at: '2026-10-14T08:00:00',
      data: { manual: true },
    },
    { type: 'CommandExecution', status: 'Success', type_reference_id: 'c', data: '{"exit_code_reported":false}' },
  ];

  it('keys decisions by plan version and step, the newest winning', () => {
    expect(decisionsByStep(rows)['v1|0:apply'].status).toBe('Unknown');
    expect(Object.keys(decisionsByStep(rows))).toEqual(['v1|0:apply']);
  });

  it('never lets a run that did not send hide an earlier unknown outcome', () => {
    const notSent = {
      type: 'CommandExecution',
      status: 'Failed',
      type_reference_id: 'a',
      created_at: '2026-10-14T10:20:00',
      data: { plan_version: 'v1', step: '0:apply', not_sent: true },
    };
    expect(decisionsByStep([notSent, ...rows])['v1|0:apply'].status).toBe('Unknown');
    expect(decisionsByStep([notSent])['v1|0:apply']).toBeUndefined();
  });

  it('marks applied only what NudgeBee saw succeed or the operator marked done', () => {
    expect(appliedCommands(rows)).toEqual({ b: '2026-10-14T09:00:00', 'change:Update secret': '2026-10-14T08:00:00' });
  });
});

// NudgeBee's own check after a run (#40734): what it says, its report as markdown, and a link to it.
describe('FollowupCheck', () => {
  it.each([
    [{}, 'Checking whether the last step fixed the problem…'],
    [{ done_at: 'x', outcome: 'proposed' }, 'The problem persists. A new plan is proposed above; confirm each command before it runs.'],
    [{ done_at: 'x', outcome: 'reported', summary: 's' }, 'Follow-up check finished. What it found:'],
    [{ done_at: 'x', outcome: 'reported' }, 'Follow-up check finished.'],
    [{ done_at: 'x', outcome: 'stopped' }, 'The follow-up check was stopped.'],
    [
      { done_at: 'x', outcome: 'needs_human', reason: 'attempt_limit' },
      'Several steps were tried for this alert in the last 24 hours, so no new plan will be proposed. Decide what to try next.',
    ],
    [{ done_at: 'x', outcome: 'failed', reason: 'budget' }, 'The follow-up check could not complete. Check the alert yourself.'],
  ])('followupText(%j)', (followup, text) => expect(followupText(followup)).toBe(text));

  it.each([
    ['a reported check', { done_at: 'x', outcome: 'reported', summary: '**Root cause:** Argo CD reverted it.' }, 'Root cause: Argo CD reverted it.'],
    // The agent asked to look again later: the card says when and why, and shows the report so far.
    [
      'a scheduled re-check',
      { recheck_at: '2026-10-15T05:30:00Z', recheck_reason: 'p95 still includes the restart.', summary: '**So far:** ok.' },
      'So far: ok.',
    ],
  ])('renders %s as markdown, with a link to the investigation', (_, followup, summary) => {
    render(<FollowupCheck followup={{ session: 's2', ...followup }} accountId='acc-1' />);
    expect(screen.getByTestId('remediation-followup-summary')).toHaveTextContent(summary);
    expect(screen.getByTestId('remediation-followup-summary')).not.toHaveTextContent('**');
    expect(screen.getByRole('link', { name: /See the follow-up investigation/ })).toHaveAttribute(
      'href',
      '/ask-nudgebee?accountId=acc-1&session_id=s2'
    );
  });

  it('says when a re-check is due and why', () => {
    expect(followupText({ recheck_at: '2026-10-15T05:30:00Z', recheck_reason: 'p95 still includes the restart.' })).toMatch(
      /^Too early to tell\. Checking again at \d\d \w{3} \d\d:\d\d\. p95 still includes/
    );
  });
});
