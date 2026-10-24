import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiKubernetes from '@api1/kubernetes';
import RemediationLoopPanel, { answerApproval } from '../RemediationLoopPanel';
import RemediationResolutionCard from '../../RemediationResolutionCard';
import { clockOnly, loopNeedsPoll } from '../../remediationLoop';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { askRemediationPlan: jest.fn(), executeRemediationCommand: jest.fn(), confirmRemediationResolve: jest.fn() },
}));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { aiFollowupResponse: jest.fn(), aiGenerateInvestigate: jest.fn() } }));
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => true }));

// Fixtures shaped exactly like the C7 read (plan fields at the top level + loop) and the C1 run rows.
const plan = {
  root_cause: 'Memory limit 100Mi is below shared_buffers 128MB',
  plan_version: 'pv1',
  actions: [
    { action: 'Raise memory in the GitOps source', kind: 'fix', confidence: 95, title: 'Pull request to sample-app' },
    {
      action: 'Live patch the Deployment',
      kind: 'mitigation',
      confidence: 50,
      trade_off: 'Argo CD puts 100Mi back',
      rollback_command: 'kubectl set resources deploy/db --limits=memory=100Mi',
    },
  ],
};
const ready = { plan_status: 'ready', relevance: { alert_state: 'firing', firings_since_plan: 0, runs_since_plan: 0, stale: false } };
const webApproval = {
  conversation_id: 'conv-1',
  message_id: 'msg-1',
  agent_id: 'agent-1',
  tool: 'kubectl',
  command: 'kubectl set resources deploy/db --limits=memory=256Mi',
  created_at: '2026-10-22T20:12:00Z',
  origin: 'web',
};
const run = {
  id: 'run-1',
  type: 'CommandExecution',
  resolver_type: 'User',
  resolver_display_name: 'Asha',
  type_reference_id: webApproval.command,
  status: 'Success',
  created_at: '2026-10-22T20:13:00Z',
  data: {
    source: 'chat',
    tool: 'kubectl',
    command: webApproval.command,
    approval_key: 'kubectl:msg-1',
    approved_by: 'asha',
    plan_version: 'pv1',
    option: 2,
    exit_code: 0,
    exit_code_reported: true,
    slot: 'execute',
  },
};

const renderPanel = (remediation, { resolutions = [], nbStatus = 'OPEN', ...rest } = {}) => {
  const props = {
    accountId: 'acc-1',
    eventId: 'ev-1',
    fingerprint: 'fp1',
    nbStatus,
    remediation,
    resolutions,
    assistantName: 'nubi',
    onOpenChat: jest.fn(),
    onOpenEvent: jest.fn(),
    onReload: jest.fn(),
    onReinvestigate: jest.fn(),
    ...rest,
  };
  render(<RemediationLoopPanel {...props} />);
  return props;
};

beforeEach(() => jest.clearAllMocks());

const pending = () => new Promise(() => {});
const sentOk = { data: { data: { ai_execute_investigation: { data: { query: 'q', status: 'QUEUED' } } } } };
const v6 = { ...plan, version: 6, generated_at: '2026-10-22T20:00:00Z', target: { account_id: 'acc-1', account_name: 'prod-east' } };
const where = () => screen.getByTestId('remediation-loop-where');
const headline = () => screen.getByTestId('remediation-loop-status');
const baseProps = () => ({
  accountId: 'acc-1',
  eventId: 'ev-1',
  fingerprint: 'fp1',
  nbStatus: 'OPEN',
  resolutions: [],
  assistantName: 'nubi',
  onOpenChat: jest.fn(),
  onOpenEvent: jest.fn(),
  onReload: jest.fn(),
  onWaitingChange: jest.fn(),
});
const withFollowup = (followup, over = {}) => ({ ...run, ...over, data: { ...run.data, followup } });

describe('A. Where it stands', () => {
  it('no fix prepared after a request: the reason and a secondary Try again, which says "Asking Nubi…" at once', () => {
    apiKubernetes.askRemediationPlan.mockReturnValue(pending());
    renderPanel({
      actions: null,
      loop: {
        plan_status: 'none',
        last_request_mode: 'auto',
        last_request_at: '2026-10-22T20:00:00Z',
        plan_status_reason: 'The conversation was busy; gave up.',
      },
    });
    expect(headline()).toHaveTextContent('No fix was prepared: The conversation was busy; gave up.');
    expect(screen.getByTestId('remediation-loop-ask-plan')).toHaveTextContent('Try again');
    fireEvent.click(screen.getByTestId('remediation-loop-ask-plan'));
    expect(screen.getByTestId('remediation-loop-ask-plan')).toHaveTextContent('Asking Nubi…');
    expect(screen.getByTestId('remediation-loop-ask-plan')).toBeDisabled();
    expect(apiKubernetes.askRemediationPlan).toHaveBeenCalledWith({ accountId: 'acc-1', eventId: 'ev-1', mode: 'plan' });
  });

  it('preparing: one headline with when it started and Watch in chat; never generates', () => {
    const props = renderPanel({ actions: null, loop: { plan_status: 'preparing', preparing_since: '2026-10-22T20:08:00Z' } });
    expect(headline()).toHaveTextContent('Preparing the fix from the investigation');
    expect(screen.getAllByText(/preparing/i)).toHaveLength(1);
    expect(apiKubernetes.askRemediationPlan).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('remediation-loop-watch-chat'));
    expect(props.onOpenChat).toHaveBeenCalledWith({ sessionId: 'event-fp1', draft: '', eventId: 'ev-1' });
  });

  it('plan ready: "Fix options · from the investigation at HH:MM" · "2 options below", with Still firing when it applies', () => {
    renderPanel({ ...v6, loop: { ...ready, relevance: { ...ready.relevance, firings_since_plan: 3 } } });
    expect(headline()).toHaveTextContent(`Fix options · from the investigation at ${clockOnly(v6.generated_at)}`);
    expect(screen.getByTestId('remediation-loop-where-detail')).toHaveTextContent('2 options below · Still firing · 3 firings since these fixes');
  });

  it('out of date: the reason and Re-check, no go with N buttons', async () => {
    apiKubernetes.askRemediationPlan.mockResolvedValue({});
    renderPanel({ ...v6, loop: { ...ready, relevance: { ...ready.relevance, stale: true, stale_reason: 'written more than 24 h ago' } } });
    expect(headline()).toHaveTextContent('These fixes are out of date');
    expect(where()).toHaveTextContent('written more than 24 h ago');
    expect(screen.queryByTestId('remediation-loop-ask-option-1')).toBeNull();
    fireEvent.click(screen.getByTestId('remediation-loop-recheck'));
    await waitFor(() => expect(apiKubernetes.askRemediationPlan).toHaveBeenCalledWith({ accountId: 'acc-1', eventId: 'ev-1', mode: 'recheck' }));
  });

  it('approval: "Approve: Nubi wants to <option> on <account>", the command, Approve / Deny; no tool names', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockResolvedValue(sentOk);
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    const props = baseProps();
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: ready }} />);
    fireEvent.click(screen.getByTestId('remediation-loop-ask-option-2'));
    await screen.findByTestId('remediation-loop-preparing-change');
    rerender(
      <RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, pending_approvals: [{ ...webApproval, tool: 'run_step' }] } }} />
    );
    expect(headline()).toHaveTextContent('Approve: Nubi wants to live patch the Deployment on prod-east');
    expect(where()).toHaveTextContent(webApproval.command);
    expect(where()).not.toHaveTextContent('run_step');
    expect(where()).toHaveTextContent('same approval as in chat');
    fireEvent.click(screen.getByTestId('remediation-loop-approve'));
    await waitFor(() =>
      expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalledWith({
        account_id: 'acc-1',
        conversation_id: 'conv-1',
        message_id: 'msg-1',
        agent_id: 'agent-1',
        query: 'yes',
      })
    );
  });

  it('approval from Slack: no buttons, links the thread when known', () => {
    renderPanel({
      ...v6,
      loop: { ...ready, pending_approvals: [{ ...webApproval, origin: 'slack', thread_url: 'https://example.slack.com/archives/C1/p1' }] },
    });
    expect(headline()).toHaveTextContent('Approve in Slack');
    expect(screen.queryByTestId('remediation-loop-approve')).toBeNull();
    expect(screen.getByRole('link', { name: /Open Slack thread/ })).toHaveAttribute('href', 'https://example.slack.com/archives/C1/p1');
  });

  it('fixing: checking, then verifying until followup.hold_until, else "Verifying it holds"', () => {
    const props = baseProps();
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: ready }} resolutions={[withFollowup(undefined)]} />);
    expect(headline()).toHaveTextContent('Fixing: Live patch the Deployment');
    expect(where()).toHaveTextContent('Checking the change');
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: ready }}
        resolutions={[withFollowup({ claimed_at: 'x', recheck_at: '2026-10-22T20:20:00Z' })]}
      />
    );
    expect(where()).toHaveTextContent('Verifying it holds');
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: ready }}
        resolutions={[withFollowup({ recheck_at: '2026-10-22T20:20:00Z', hold_until: '2026-10-22T20:23:00Z' })]}
      />
    );
    expect(where()).toHaveTextContent(/Verifying it holds until /);
  });

  it('not fixed / unclear: the verdict as a sentence and Re-check plan', () => {
    const props = baseProps();
    const { rerender } = render(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: ready }}
        resolutions={[withFollowup({ verdict: 'not_fixed', verdict_reason: 'fired_again_after_change', verdict_at: '2026-10-22T20:30:00Z' })]}
      />
    );
    expect(headline()).toHaveTextContent('Not fixed — the alert fired again after the change');
    expect(screen.getByTestId('remediation-loop-recheck-plan')).toBeInTheDocument();
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: ready }}
        resolutions={[withFollowup({ verdict: 'unclear', verdict_reason: 'alert_state_unread' })]}
      />
    );
    expect(headline()).toHaveTextContent('Couldn’t confirm the fix — the alert’s state could not be read');
    expect(headline()).not.toHaveTextContent('alert_state_unread');
  });

  it('Nubi ran nothing: "didn’t run it — see the reply in chat" with Try again, until the next action', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockResolvedValue(sentOk);
    const props = baseProps();
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: ready }} />);
    fireEvent.click(screen.getByTestId('remediation-loop-ask-option-2'));
    await screen.findByTestId('remediation-loop-preparing-change');
    expect(headline()).toHaveTextContent('Fixing: Live patch the Deployment');
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, conversation_busy: true } }} />);
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, conversation_busy: false } }} />);
    expect(headline()).toHaveTextContent('Nubi didn’t run it — see the reply in chat');
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready } }} />);
    expect(headline()).toHaveTextContent('Nubi didn’t run it');
    fireEvent.click(screen.getByTestId('remediation-loop-not-run-chat'));
    expect(props.onOpenChat).toHaveBeenCalledWith({ sessionId: 'event-fp1', draft: '', eventId: 'ev-1' });
    fireEvent.click(screen.getByTestId('remediation-loop-not-run-retry'));
    await waitFor(() => expect(apiAskNudgebee.aiGenerateInvestigate).toHaveBeenCalledTimes(2));
  });

  it('a pick that never sees the conversation busy ends after the grace period', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockResolvedValue(sentOk);
    jest.useFakeTimers();
    try {
      const props = baseProps();
      const { rerender } = render(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: ready }} />);
      fireEvent.click(screen.getByTestId('remediation-loop-ask-option-1'));
      await screen.findByTestId('remediation-loop-preparing-change');
      act(() => jest.advanceTimersByTime(30000));
      rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready } }} />);
      expect(screen.getByTestId('remediation-loop-preparing-change')).toBeInTheDocument();
      act(() => jest.advanceTimersByTime(31000));
      rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready } }} />);
      expect(headline()).toHaveTextContent('Nubi didn’t run it');
    } finally {
      jest.useRealTimers();
    }
  });

  it('resolved by Nubi: who, what fixed it, how we know, when; See conversation', () => {
    const won = withFollowup(
      { verdict: 'fixed', verdict_reason: 'cleared_after_change_and_held', verdict_at: '2026-10-22T20:25:00Z' },
      { created_at: '2026-10-22T20:13:00Z' }
    );
    const props = renderPanel(
      { ...v6, loop: { ...ready, relevance: { ...ready.relevance, alert_state: 'resolved', alert_state_at: '2026-10-22T20:26:00Z' } } },
      {
        nbStatus: 'RESOLVED',
        resolutions: [won],
        labels: { nb_resolved_by: '{"run_id":"run-1","verdict":"fixed","cause_key":null}' },
        alertStartedAt: '2026-10-22T19:56:00Z',
      }
    );
    expect(headline()).toHaveTextContent('✓ Fixed by Nubi · approved by Asha');
    const lines = screen.getAllByTestId('remediation-loop-where-line').map((n) => n.textContent);
    expect(lines[0]).toBe(`Live patch the Deployment — ${webApproval.command}`);
    expect(lines[1]).toMatch(/^The alert cleared after the change and was still clear at .+, 12 min after it ran$/);
    expect(lines[2]).toMatch(/^Resolved .+ · 30 min after the alert · attempt 1 of 1$/);
    fireEvent.click(screen.getByTestId('remediation-loop-see-conversation'));
    expect(props.onOpenChat).toHaveBeenCalled();
    expect(screen.queryByTestId('remediation-loop-continue-chat')).toBeNull();
  });

  it('resolved after a denied attempt: the time the person resolved it, and the attempt numbered as on the Timeline', () => {
    const won = withFollowup(
      { verdict: 'fixed', verdict_reason: 'cleared_after_change_and_held', verdict_at: '2026-10-22T20:25:00Z' },
      { created_at: '2026-10-22T20:13:00Z' }
    );
    renderPanel(
      { ...v6, loop: { ...ready, denied_approvals: [{ message_id: 'm0', denied_at: '2026-10-22T20:05:00Z', denied_by_name: 'Asha' }] } },
      {
        nbStatus: 'RESOLVED',
        resolutions: [won],
        labels: { nb_resolved_by: '{"run_id":"run-1","verdict":"fixed","confirmed_by_name":"Asha","confirmed_at":"2026-10-22T20:46:00Z"}' },
        alertStartedAt: '2026-10-22T19:56:00Z',
      }
    );
    const lines = screen.getAllByTestId('remediation-loop-where-line').map((n) => n.textContent);
    expect(lines[2]).toMatch(/^Resolved .+ by Asha · 50 min after the alert · attempt 2 of 2$/);
  });

  it('resolved by a person, or cleared on its own', () => {
    const manual = { ...run, data: { manual: true, command: 'helm rollback web 3', option: 1, plan_version: 'pv1', followup: { verdict: 'fixed' } } };
    const props = baseProps();
    const { rerender } = render(<RemediationLoopPanel {...props} nbStatus='RESOLVED' remediation={{ ...v6, loop: ready }} resolutions={[manual]} />);
    expect(headline()).toHaveTextContent('✓ Fixed by Asha');
    rerender(
      <RemediationLoopPanel
        {...props}
        nbStatus='RESOLVED'
        remediation={{ ...v6, loop: ready }}
        resolutions={[withFollowup({ verdict: 'cleared_before_change', verdict_at: '2026-10-22T20:20:00Z' })]}
      />
    );
    expect(headline()).toHaveTextContent('✓ Resolved — cleared on its own');
  });

  it('a request that ended without a fix is named from the read in one sentence, with more and Try again', () => {
    renderPanel({
      ...v6,
      loop: {
        ...ready,
        plan_status_reason: 'Nubi wrote no plan: the alert closed.\nBased on the user’s remediation request and the investigation, nothing applies.',
        last_request_mode: 'recheck',
        last_request_at: '2026-10-23T10:44:00Z',
      },
    });
    const text = screen.getByTestId('remediation-loop-request-no-plan-text');
    expect(text).toHaveTextContent(/^Re-check at .+ didn’t produce a fix — Nubi wrote no plan: the alert closed\.more$/);
    expect(text).not.toHaveTextContent('Based on');
    fireEvent.click(screen.getByTestId('remediation-loop-request-no-plan-text-more'));
    expect(text).toHaveTextContent('Based on the user’s remediation request');
    expect(screen.getByTestId('remediation-loop-request-no-plan-retry')).toHaveTextContent('Try again');
  });

  it('the no-fix line shows only while picking: not while a fix is checked, nor on a result', () => {
    const stale = { plan_status_reason: 'Nubi wrote no plan: older request.', last_request_mode: 'replan', last_request_at: '2026-10-22T19:13:00Z' };
    const checking = withFollowup(
      { claimed_at: '2026-10-22T20:14:00Z' },
      { data: { ...run.data, plan_version: 'pv1', option: 2, followup: { claimed_at: '2026-10-22T20:14:00Z' } } }
    );
    const { rerender } = render(<RemediationLoopPanel {...baseProps()} remediation={{ ...v6, loop: { ...ready, ...stale } }} />);
    expect(screen.getByTestId('remediation-loop-request-no-plan')).toBeInTheDocument();
    rerender(<RemediationLoopPanel {...baseProps()} remediation={{ ...v6, loop: { ...ready, ...stale } }} resolutions={[checking]} />);
    expect(headline()).toHaveTextContent('Fixing:');
    expect(screen.queryByTestId('remediation-loop-request-no-plan')).toBeNull();
    rerender(
      <RemediationLoopPanel
        {...baseProps()}
        remediation={{ ...v6, loop: { ...ready, ...stale } }}
        resolutions={[{ ...checking, data: { ...checking.data, followup: { verdict: 'not_fixed', verdict_at: '2026-10-22T20:30:00Z' } } }]}
      />
    );
    expect(screen.queryByTestId('remediation-loop-request-no-plan')).toBeNull();
  });

  it('a send refused as busy or interrupted reads in plain words on the option', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockResolvedValueOnce({ data: { errors: [{ message: 'This conversation is in progress.' }] } });
    renderPanel({ ...v6, loop: ready });
    fireEvent.click(screen.getByTestId('remediation-loop-ask-option-1'));
    expect(await screen.findByTestId('remediation-loop-option-error-1')).toHaveTextContent(
      'Nubi is still finishing its last reply — try again in a moment'
    );
  });

  it('duplicate: one line above the card, Open main event and Re-investigate this firing', () => {
    const props = renderPanel(
      { ...v6, loop: { ...ready, primary_event_id: 'abcd1234-1111', primary_fingerprint: 'fpmain' } },
      { nbStatus: 'DUPLICATE' }
    );
    expect(screen.getByTestId('remediation-loop-duplicate')).toHaveTextContent('Duplicate of abcd1234 — this alert’s remediation runs there');
    fireEvent.click(screen.getByTestId('remediation-loop-open-main'));
    expect(props.onOpenEvent).toHaveBeenCalledWith('abcd1234-1111');
    fireEvent.click(screen.getByTestId('remediation-loop-reinvestigate'));
    expect(props.onReinvestigate).toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('remediation-loop-continue-chat'));
    expect(props.onOpenChat).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: 'event-fpmain', eventId: 'abcd1234-1111' }));
  });

  it('more actions: the ⋮ menu opens the alert actions', async () => {
    const mute = jest.fn();
    renderPanel({ ...v6, loop: ready }, { moreActions: [{ id: 'remediation-loop-mute', label: 'Mute this alert…', onSelect: mute }, false] });
    fireEvent.click(screen.getByTestId('remediation-loop-more-actions'));
    fireEvent.click(await screen.findByText('Mute this alert…'));
    expect(mute).toHaveBeenCalled();
  });
});

describe('B. Plan card', () => {
  it('while picking: "Fix options", kind chip, confidence, go with N sent straight from the tab with "Asking Nubi…"', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockReturnValue(pending());
    const props = renderPanel({ ...v6, loop: ready });
    expect(screen.getByTestId('remediation-loop-options')).toHaveTextContent('Fix options');
    expect(screen.getByTestId('remediation-loop-option-1')).toHaveTextContent('Fix');
    expect(screen.getByTestId('remediation-loop-option-1')).toHaveTextContent('95% likely to resolve');
    expect(screen.queryByText(/Recommended/i)).toBeNull();
    fireEvent.click(screen.getByTestId('remediation-loop-ask-option-2'));
    expect(screen.getByTestId('remediation-loop-ask-option-2')).toHaveTextContent('Asking Nubi…');
    expect(screen.getByTestId('remediation-loop-ask-option-2')).toBeDisabled();
    expect(apiAskNudgebee.aiGenerateInvestigate).toHaveBeenCalledWith({
      account_id: 'acc-1',
      query: 'Go with option 2: Live patch the Deployment.',
      session_id: 'event-fp1',
      config: { event_id: 'ev-1' },
    });
    expect(props.onOpenChat).not.toHaveBeenCalled();
  });

  it('a slug title shows the option’s description instead', () => {
    renderPanel({ ...v6, actions: [{ action: 'set-image', title: 'Update deployment image to nginx:1.27-alpine', kind: 'fix' }], loop: ready });
    expect(screen.getByTestId('remediation-loop-option-1')).toHaveTextContent('1 · Update deployment image to nginx:1.27-alpine');
    expect(screen.getByTestId('remediation-loop-option-1')).not.toHaveTextContent('set-image');
  });

  it('busy: go with N is off and says why', () => {
    renderPanel({ ...v6, loop: { ...ready, conversation_busy: true } });
    expect(screen.getByTestId('remediation-loop-go-disabled-reason')).toHaveTextContent('Nubi is finishing its last reply');
    expect(screen.getByTestId('remediation-loop-ask-option-1')).toBeDisabled();
  });

  it('replan: sends the reasons and the note', async () => {
    apiKubernetes.askRemediationPlan.mockResolvedValue({});
    renderPanel({ ...v6, loop: ready });
    fireEvent.click(screen.getByTestId('remediation-loop-replan-open'));
    fireEvent.click(screen.getByRole('button', { name: 'Too risky' }));
    fireEvent.change(screen.getByTestId('remediation-loop-replan-note'), { target: { value: 'not before 22:00' } });
    fireEvent.click(screen.getByTestId('remediation-loop-replan-submit'));
    await waitFor(() =>
      expect(apiKubernetes.askRemediationPlan).toHaveBeenCalledWith({
        accountId: 'acc-1',
        eventId: 'ev-1',
        mode: 'replan',
        reasons: ['Too risky'],
        note: 'not before 22:00',
      })
    );
  });

  it('once an option is chosen it collapses to one line; opened, the chosen option carries its state and the others are muted', () => {
    renderPanel({ ...v6, loop: ready }, { resolutions: [withFollowup({ verdict: 'not_fixed', verdict_reason: 'still_firing' })] });
    const toggle = screen.getByTestId('remediation-loop-plan-toggle');
    expect(toggle).toHaveTextContent(`Fix options · from the investigation at ${clockOnly(v6.generated_at)} · Live patch the Deployment ▸`);
    expect(screen.queryByTestId('remediation-loop-chosen')).toBeNull();
    fireEvent.click(toggle);
    expect(screen.getByTestId('remediation-loop-chosen')).toHaveTextContent('Live patch the Deployment');
    expect(screen.getByTestId('remediation-loop-chosen')).toHaveTextContent('Not fixed');
    expect(screen.getByTestId('remediation-loop-not-chosen')).toHaveTextContent('Raise memory in the GitOps source · not chosen');
    expect(screen.queryByTestId('remediation-loop-ask-option-1')).toBeNull();
  });
});

describe('C. Timeline (vertical stepper)', () => {
  const rowsOf = () => Array.from(screen.getByTestId('remediation-loop-timeline-rows').children);
  const kindOf = (n) => n.getAttribute('data-testid');

  it('one order, oldest first: plan rows and attempts by time, steps oldest first, the latest attempt open and last', () => {
    const first = withFollowup(
      { claimed_at: '2026-10-22T20:06:00Z', verdict: 'not_fixed', verdict_reason: 'fired_again_after_change', verdict_at: '2026-10-22T20:09:00Z' },
      { id: 'r1', created_at: '2026-10-22T20:05:00Z', updated_at: '2026-10-22T20:05:10Z' }
    );
    const second = withFollowup(
      { claimed_at: '2026-10-22T20:14:00Z' },
      { id: 'r2', created_at: '2026-10-22T20:13:00Z', updated_at: '2026-10-22T20:13:05Z' }
    );
    renderPanel({ ...v6, generated_at: '2026-10-22T20:10:00Z', loop: ready }, { resolutions: [second, first] });
    const order = rowsOf()
      .map(kindOf)
      .filter((k) => k !== 'remediation-loop-day');
    expect(order).toEqual(['remediation-loop-activity-run', 'remediation-loop-timeline-plan', 'remediation-loop-activity-run']);
    const attempts = screen.getAllByTestId('remediation-loop-activity-run');
    expect(attempts[0]).toHaveTextContent('Attempt 1');
    expect(attempts[1]).toHaveTextContent('Attempt 2 · Live patch the Deployment');
    // The older attempt is collapsed; the latest is open, its live step last with a spinner.
    expect(within(attempts[0]).queryAllByRole('listitem')).toHaveLength(0);
    const steps = within(attempts[1])
      .getAllByRole('listitem')
      .map((li) => li.getAttribute('data-testid'));
    expect(steps).toEqual(['remediation-loop-step-approved', 'remediation-loop-step-ran', 'remediation-loop-step-checking']);
    expect(within(within(attempts[1]).getByTestId('remediation-loop-step-checking')).getByTestId('remediation-loop-step-live')).toBeInTheDocument();
    fireEvent.click(within(attempts[0]).getByTestId('remediation-loop-attempt-toggle'));
    expect(within(screen.getAllByTestId('remediation-loop-activity-run')[0]).getByTestId('remediation-loop-step-recheck')).toHaveTextContent(
      'Re-check: the alert fired again after the change'
    );
  });

  it('times are HH:MM; a day row appears only where the day changes', () => {
    const today = new Date();
    const at = (h, m) => new Date(today.getFullYear(), today.getMonth(), today.getDate(), h, m).toISOString();
    const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1, 23, 50).toISOString();
    const old = withFollowup(undefined, { id: 'r1', created_at: yesterday, updated_at: yesterday });
    const now = withFollowup({ claimed_at: at(0, 20) }, { id: 'r2', created_at: at(0, 10), updated_at: at(0, 11) });
    renderPanel({ ...v6, generated_at: yesterday, loop: ready }, { resolutions: [old, now] });
    const days = screen.getAllByTestId('remediation-loop-day').map((n) => n.textContent);
    expect(days[0]).not.toBe('Today');
    expect(days[days.length - 1]).toBe('Today');
    expect(days).toHaveLength(2);
    const ran = screen.getByTestId('remediation-loop-step-ran');
    expect(ran.firstChild.textContent).toMatch(/^\d{2}:\d{2}$/);
  });

  it('each row type has its icon on the rail; a non-zero exit is red', () => {
    const won = {
      ...run,
      data: {
        ...run.data,
        exit_code: 2,
        followup: {
          claimed_at: '2026-10-22T20:14:00Z',
          verdict: 'fixed',
          verdict_reason: 'cleared_after_change_and_held',
          verdict_at: '2026-10-22T20:20:00Z',
        },
      },
    };
    renderPanel(
      { ...v6, loop: ready },
      { nbStatus: 'RESOLVED', resolutions: [won], labels: { nb_resolved_by: '{"run_id":"run-1","verdict":"fixed"}' } }
    );
    const icon = (testId) => screen.getByTestId(testId).getAttribute('data-icon');
    expect(icon('remediation-loop-timeline-plan')).toBe('plan');
    expect(icon('remediation-loop-step-approved')).toBe('approved');
    expect(icon('remediation-loop-step-ran')).toBe('ran');
    expect(icon('remediation-loop-step-recheck')).toBe('fixed');
    expect(icon('remediation-loop-step-resolved')).toBe('resolved');
    expect(within(screen.getByTestId('remediation-loop-step-ran')).getByTestId('remediation-loop-rail-dot')).toHaveStyle({
      color: 'var(--ds-red-600)',
    });
  });

  it('a run shows a short step and its full command on one line that opens on click, with copy', () => {
    const cmd = 'kubectl set image deployment/demo-app web=nginx:1.27-alpine -n demo --record';
    renderPanel({ ...v6, loop: ready }, { resolutions: [{ ...run, data: { ...run.data, command: cmd } }] });
    const ran = screen.getByTestId('remediation-loop-step-ran');
    expect(ran).toHaveTextContent('Ran set image → nginx:1.27-alpine · exit 0');
    const line = within(ran).getByTestId('remediation-loop-command-line');
    expect(line).toHaveTextContent(cmd);
    expect(line).toHaveStyle({ whiteSpace: 'nowrap', textOverflow: 'ellipsis' });
    fireEvent.click(line);
    expect(within(ran).getByTestId('remediation-loop-command-open')).toHaveTextContent(cmd);
  });

  it('holding while live, held after; the check sentence is short; chips are sentence case', () => {
    const props = baseProps();
    const { rerender } = render(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: ready }}
        resolutions={[withFollowup({ claimed_at: '2026-10-22T20:14:00Z', hold_until: '2026-10-22T20:19:00Z' })]}
      />
    );
    expect(screen.getByTestId('remediation-loop-step-holding')).toHaveTextContent(/Holding until \d{2}:\d{2}$/);
    expect(screen.getByTestId('remediation-loop-step-holding').firstChild.textContent).toMatch(/^\d{2}:\d{2}$/);
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: ready }}
        resolutions={[
          withFollowup({
            claimed_at: '2026-10-22T20:14:00Z',
            hold_until: '2026-10-22T20:19:00Z',
            done_at: '2026-10-22T20:21:00Z',
            outcome: 'reported',
            summary: '**Status:** Fixed; the deployment image was restored. All replicas are ready.\n\nLong detail.',
          }),
        ]}
      />
    );
    expect(screen.getByTestId('remediation-loop-step-held')).toHaveTextContent(/Held until \d{2}:\d{2}$/);
    expect(screen.getByTestId('remediation-loop-step-nubi-check')).toHaveTextContent('Nubi’s check: Fixed — the deployment image was restored');
    expect(screen.getByTestId('remediation-loop-step-nubi-check')).not.toHaveTextContent('All replicas');
    expect(screen.queryByTestId('remediation-loop-step-live')).toBeNull();
    expect(screen.getByTestId('remediation-loop-activity-run')).toHaveTextContent('Checked by Nubi');
  });

  it('a plan row names how it came about and its changes as chips, using the action titles', () => {
    renderPanel(
      {
        ...v6,
        version: 2,
        generated_at: '2026-10-22T20:30:00Z',
        actions: [
          { action: 'scale_to_zero', title: 'Scale to 0 replicas', kind: 'mitigation' },
          { action: 'update_image', title: 'Update image', kind: 'fix' },
        ],
        revision: { kind: 'replan', note: 'use the repo', changes: ['Added: scale_to_zero, update_image', 'Removed: restore_image'] },
        loop: ready,
      },
      { resolutions: [{ ...run, created_at: '2026-10-22T20:13:00Z' }] }
    );
    expect(screen.getByTestId('remediation-loop-timeline-plan')).toHaveTextContent('Fix options updated (your note)');
    const chips = within(screen.getByTestId('remediation-loop-timeline-plan-changes'))
      .getAllByText(/./)
      .map((n) => n.textContent);
    expect(chips).toEqual(expect.arrayContaining(['+ Scale to 0 replicas', '+ Update image', '− Restore image']));
  });

  it('the attempt header names the option, else a short command summary', () => {
    const offPlan = {
      ...run,
      data: { ...run.data, plan_version: 'older', command: 'kubectl set image deployment/web web=nginx:1.27-alpine -n demo' },
    };
    renderPanel({ ...v6, loop: ready }, { resolutions: [offPlan] });
    expect(screen.getByTestId('remediation-loop-attempt-title')).toHaveTextContent('Attempt 1 · set image → nginx:1.27-alpine');
  });

  it('the attempt not yet recorded comes last: your pick, Nubi asking for approval, then approved and running', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockResolvedValue(sentOk);
    apiAskNudgebee.aiFollowupResponse.mockReturnValue(pending());
    const props = baseProps();
    const { rerender } = render(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: ready }}
        resolutions={[{ ...run, id: 'old', created_at: '2026-10-22T19:00:00Z', data: { ...run.data, option: undefined } }]}
      />
    );
    fireEvent.click(screen.getByTestId('remediation-loop-ask-option-1'));
    await screen.findByTestId('remediation-loop-step-picked');
    const runs = screen.getAllByTestId('remediation-loop-activity-run');
    expect(runs[runs.length - 1]).toHaveTextContent('Attempt 2');
    expect(screen.getByTestId('remediation-loop-step-picked')).toHaveTextContent('You picked option 1 — Raise memory in the GitOps source');
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: { ...ready, conversation_busy: true, pending_approvals: [webApproval] } }}
        resolutions={[{ ...run, id: 'old', created_at: '2026-10-22T19:00:00Z', data: { ...run.data, option: undefined } }]}
      />
    );
    expect(screen.getByTestId('remediation-loop-step-asked')).toHaveTextContent('Nubi asked for approval');
    fireEvent.click(screen.getByTestId('remediation-loop-approve'));
    expect(screen.getByTestId('remediation-loop-step-approved')).toHaveTextContent('Approved — running the change');
  });
});

describe('Round 11: text stays inside its column', () => {
  it('a sentence change ("Cause: …") on a plan row is wrapped text kept to two lines, not a chip', () => {
    const cause =
      'Cause: Deployment demo-app spec was updated with an image tag that does not exist on the registry, so every new pod fails to pull it and the rollout never completes.';
    renderPanel(
      { ...v6, version: 3, revision: { kind: 'replan', note: 'x', changes: ['Added: scale_to_zero', cause] }, loop: ready },
      { resolutions: [{ ...run, created_at: '2026-10-22T20:13:00Z' }] }
    );
    const note = screen.getByTestId('remediation-loop-timeline-plan-note');
    expect(note).toHaveTextContent('Cause: Deployment demo-app');
    expect(within(note).getByRole('button', { name: 'more' })).toBeInTheDocument();
    expect(within(screen.getByTestId('remediation-loop-timeline-plan-changes')).queryByText(/Cause:/)).toBeNull();
  });

  it('an option’s command block has no header bar: no language label, the copy button inline', () => {
    renderPanel({ ...v6, actions: [{ ...v6.actions[0], execute_command: 'gh pr create --repo org/app' }], loop: ready });
    const block = screen.getByTestId('remediation-loop-option-command-1');
    expect(block).toHaveTextContent('gh pr create --repo org/app');
    expect(block).not.toHaveTextContent(/bash/i);
  });
});

describe('Round 11: the alert came back after a fix', () => {
  // One event's story: attempt 1 at 12:48, the alert cleared at 12:50, Nubi's check said fixed, the alert fired again
  // at 13:08 (18 min after the fix), same cause.
  const T = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const attempt1 = {
    ...run,
    id: 'run-1',
    created_at: T('12:48'),
    updated_at: T('12:48'),
    data: {
      ...run.data,
      command: 'kubectl set image deployment/demo-app web=nginx:1.27-alpine -n demo',
      followup: {
        claimed_at: T('12:49'),
        done_at: T('12:55'),
        outcome: 'reported',
        summary: '**Status:** Fixed; the deployment image was restored.',
      },
    },
  };
  const story = {
    ...v6,
    version: 3,
    generated_at: T('12:40'),
    loop: {
      ...ready,
      firings: [
        { event_id: 'evt-main', starts_at: T('12:20'), ends_at: T('12:50'), status: 'RESOLVED', nb_status: 'OPEN' },
        { event_id: 'f00d2222', starts_at: T('13:08'), ends_at: null, status: 'FIRING', nb_status: 'OPEN' },
      ],
      refire_after_run: { run_id: 'run-1', fired_at: T('13:08'), minutes_after: 18, cause: 'same_cause' },
    },
  };

  it('the timeline shows the alert clearing, and the attempt ends with the re-fire, not "Fixed"', () => {
    renderPanel(story, { resolutions: [attempt1] });
    const attempt = screen.getByTestId('remediation-loop-activity-run');
    // Inside the attempt, in time order: ran, the alert cleared, Nubi's check, the alert fired again.
    expect(
      within(attempt)
        .getAllByRole('listitem')
        .map((li) => li.getAttribute('data-testid'))
    ).toEqual([
      'remediation-loop-step-approved',
      'remediation-loop-step-ran',
      'remediation-loop-step-alert-cleared',
      'remediation-loop-step-nubi-check',
      'remediation-loop-step-came-back',
    ]);
    expect(within(attempt).getByTestId('remediation-loop-step-alert-cleared')).toHaveTextContent(`${clockOnly(T('12:50'))}Alert cleared`);
    expect(attempt).toHaveTextContent('Came back');
    expect(attempt).not.toHaveTextContent('Checked by Nubi');
    const steps = within(attempt).getAllByRole('listitem');
    const last = steps[steps.length - 1];
    expect(last).toHaveAttribute('data-testid', 'remediation-loop-step-came-back');
    expect(last).toHaveTextContent(`Alert fired again at ${clockOnly(T('13:08'))} — 18 min after this fix`);
    expect(last).toHaveTextContent('Same cause is back (the fix was undone or didn’t stick)');
    // The chain's first firing reads "Alert fired"; the re-fire is told on the attempt, not again as its own row.
    expect(screen.getAllByTestId('remediation-loop-alert-fired').map((n) => n.textContent)).toEqual([`${clockOnly(T('12:20'))}Alert fired`]);
  });

  it('the top card says the alert came back and offers Re-investigate, then Re-check plan; the plan is marked as older', async () => {
    apiKubernetes.askRemediationPlan.mockResolvedValue({});
    const props = baseProps();
    const onReinvestigate = jest.fn();
    const { rerender } = render(<RemediationLoopPanel {...props} onReinvestigate={onReinvestigate} remediation={story} resolutions={[attempt1]} />);
    expect(headline()).toHaveTextContent('The alert came back after attempt 1');
    expect(headline()).not.toHaveTextContent('Pick a fix');
    expect(screen.getByTestId('remediation-loop-where-detail')).toHaveTextContent(
      `Fired again at ${clockOnly(T('13:08'))}, 18 min after the fix · Same cause is back`
    );
    expect(screen.getByTestId('remediation-loop-plan-toggle')).toHaveTextContent('written before the alert came back');
    expect(screen.getByTestId('remediation-loop-came-back-recheck-secondary')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('remediation-loop-came-back-reinvestigate'));
    expect(onReinvestigate).toHaveBeenCalled();
    expect(screen.getByTestId('remediation-loop-came-back-reinvestigate')).toHaveTextContent('Re-investigating…');
    rerender(
      <RemediationLoopPanel {...props} onReinvestigate={onReinvestigate} reinvestigateDisabled remediation={story} resolutions={[attempt1]} />
    );
    rerender(
      <RemediationLoopPanel {...props} onReinvestigate={onReinvestigate} reinvestigateDisabled={false} remediation={story} resolutions={[attempt1]} />
    );
    expect(screen.queryByTestId('remediation-loop-came-back-reinvestigate')).toBeNull();
    fireEvent.click(screen.getByTestId('remediation-loop-came-back-recheck'));
    await waitFor(() => expect(apiKubernetes.askRemediationPlan).toHaveBeenCalledWith({ accountId: 'acc-1', eventId: 'ev-1', mode: 'recheck' }));
  });

  it('a plan written after the re-fire says so, and the card moves on', () => {
    renderPanel({ ...story, version: 4, generated_at: T('13:20'), revision: { kind: 'auto' } }, { resolutions: [attempt1] });
    expect(screen.getByTestId('remediation-loop-timeline-plan')).toHaveTextContent('Fix options updated (after the alert came back)');
    expect(headline()).not.toHaveTextContent('came back');
  });

  it('a recorded shape: repeats while firing are one row, clears at the same time are one row, re-fire repeats are "still firing"', () => {
    // The main event and two duplicates while it was still firing (all ending 07:20Z), the re-fire at 07:38Z, then
    // repeat notifications of the same still-firing alert every ~10 min.
    const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
    const firings = [
      { event_id: 'evt-main', starts_at: Z('06:53'), ends_at: Z('07:20'), status: 'RESOLVED' },
      { event_id: 'evt-repeat', starts_at: Z('07:04'), ends_at: Z('07:20'), status: 'RESOLVED' },
      { event_id: 'dup2', starts_at: Z('07:14'), ends_at: Z('07:20'), status: 'RESOLVED' },
      { event_id: 'refire', starts_at: Z('07:38'), ends_at: null, status: 'FIRING' },
      ...['07:50', '08:00', '08:10', '08:21', '08:31', '08:41', '08:51'].map((t, i) => ({
        event_id: `r${i}`,
        starts_at: Z(t),
        ends_at: null,
        status: 'FIRING',
      })),
    ];
    const fix = {
      ...run,
      id: 'run-1',
      created_at: Z('07:18'),
      updated_at: Z('07:18'),
      data: {
        ...run.data,
        followup: { claimed_at: Z('07:19'), done_at: Z('07:25'), outcome: 'reported', summary: '**Status:** Fixed; the image was restored.' },
      },
    };
    renderPanel(
      {
        ...v6,
        version: 3,
        generated_at: Z('08:55'),
        loop: { ...ready, firings, refire_after_run: { run_id: 'run-1', fired_at: Z('07:38'), minutes_after: 21, cause: 'same_cause' } },
      },
      { resolutions: [fix] }
    );
    const fired = screen.getAllByTestId('remediation-loop-alert-fired');
    expect(fired).toHaveLength(1);
    expect(fired[0]).toHaveTextContent(`Alert fired · still firing, 3 alerts ${clockOnly(Z('06:53'))}–${clockOnly(Z('07:14'))}`);
    // One clear, inside attempt 1.
    expect(screen.getAllByTestId('remediation-loop-step-alert-cleared')).toHaveLength(1);
    expect(screen.queryByTestId('remediation-loop-alert-cleared')).toBeNull();
    expect(screen.getByTestId('remediation-loop-step-came-back')).toHaveTextContent('21 min after this fix');
    expect(screen.getByTestId('remediation-loop-alert-still')).toHaveTextContent(`Still firing since ${clockOnly(Z('07:38'))} · 8 alerts`);
    expect(screen.queryByTestId('remediation-loop-alert-more')).toBeNull();
    // Order: fired → attempt 1 → still firing → plan v3.
    const order = Array.from(screen.getByTestId('remediation-loop-timeline-rows').children)
      .map((n) => n.getAttribute('data-testid'))
      .filter((k) => k !== 'remediation-loop-day');
    expect(order).toEqual([
      'remediation-loop-alert-fired',
      'remediation-loop-activity-run',
      'remediation-loop-alert-still',
      'remediation-loop-timeline-plan',
    ]);
  });
});

describe('Round 13: deny, and the old panel never comes back', () => {
  const T = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const v3 = { ...v6, version: 3, plan_version: 'pv3', generated_at: T('13:20') };

  it('a denied approval is an attempt on the timeline: "Option 1 denied by <name> · nothing ran"', () => {
    renderPanel(
      {
        ...v3,
        loop: {
          ...ready,
          denied_approvals: [
            {
              message_id: 'm9',
              tool: 'kubectl',
              command: 'kubectl scale deployment/web --replicas=0 -n demo',
              option: 1,
              plan_version: 'pv3',
              denied_at: T('13:24'),
              denied_by: '6f1c2b9e-0000-4000-8000-000000000001',
              denied_by_name: 'Jane Doe',
            },
          ],
        },
      },
      { resolutions: [{ ...run, created_at: T('12:48'), data: { ...run.data, plan_version: 'pv1' } }] }
    );
    const attempts = screen.getAllByTestId('remediation-loop-activity-run');
    const denied = attempts[attempts.length - 1];
    expect(denied).toHaveTextContent('Attempt 2 · Raise memory in the GitOps source');
    expect(denied).toHaveTextContent('Denied');
    expect(within(denied).getByTestId('remediation-loop-step-denied')).toHaveTextContent('Option 1 denied by Jane Doe · nothing ran');
    expect(denied).not.toHaveTextContent('6f1c2b9e');
    // The options are pickable again.
    expect(screen.getByTestId('remediation-loop-ask-option-1')).toBeEnabled();
    expect(headline()).not.toHaveTextContent('Approve');
  });

  it('an approval closed unanswered shows when it was asked, by whom, and why it closed (#40734)', () => {
    renderPanel(
      {
        ...v3,
        loop: {
          ...ready,
          denied_approvals: [
            {
              message_id: 'm8',
              command: 'kubectl scale deployment/web --replicas=0 -n demo',
              option: 1,
              plan_version: 'pv3',
              reason: 'superseded',
              asked_at: T('13:21'),
              asked_by_name: 'Jane Doe',
              denied_at: T('13:40'),
            },
          ],
        },
      },
      { resolutions: [{ ...run, created_at: T('12:48'), data: { ...run.data, plan_version: 'pv1' } }] }
    );
    const attempts = screen.getAllByTestId('remediation-loop-activity-run');
    const closed = attempts[attempts.length - 1];
    expect(closed).toHaveTextContent('Superseded');
    expect(within(closed).getByTestId('remediation-loop-step-asked')).toHaveTextContent('Option 1 asked for approval by Jane Doe');
    expect(within(closed).getByTestId('remediation-loop-step-closed')).toHaveTextContent(
      'Closed when the alert cleared and fired again · nothing ran'
    );
    expect(closed).not.toHaveTextContent('denied by');
  });

  it('a denial with only the user id names nobody', () => {
    renderPanel({
      ...v3,
      loop: {
        ...ready,
        denied_approvals: [
          {
            message_id: 'm9',
            command: 'kubectl scale deployment/web --replicas=0',
            option: 1,
            plan_version: 'pv3',
            denied_at: T('13:24'),
            denied_by: '6f1c2b9e-0000-4000-8000-000000000001',
          },
        ],
      },
    });
    expect(screen.getByTestId('remediation-loop-step-denied')).toHaveTextContent('Option 1 denied · nothing ran');
    expect(screen.getByTestId('remediation-loop-step-denied')).not.toHaveTextContent('denied by');
    expect(screen.getByTestId('remediation-loop-activity')).not.toHaveTextContent('6f1c2b9e');
  });

  it('Deny on the tab: the approval leaves the card at once and the options are pickable again', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockResolvedValue(sentOk);
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    const props = baseProps();
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={{ ...v3, loop: ready }} />);
    fireEvent.click(screen.getByTestId('remediation-loop-ask-option-1'));
    await screen.findByTestId('remediation-loop-preparing-change');
    const waiting = { ...v3, loop: { ...ready, conversation_busy: true, pending_approvals: [webApproval] } };
    rerender(<RemediationLoopPanel {...props} remediation={waiting} />);
    fireEvent.click(screen.getByTestId('remediation-loop-deny'));
    await waitFor(() => expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalledWith(expect.objectContaining({ query: 'no' })));
    // The read still lists the approval: the card does not say "Approve" for it.
    rerender(<RemediationLoopPanel {...props} remediation={waiting} />);
    expect(headline()).not.toHaveTextContent('Approve');
    expect(screen.queryByTestId('remediation-loop-preparing-change')).toBeNull();
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v3, loop: ready }} />);
    expect(screen.getByTestId('remediation-loop-ask-option-1')).toBeEnabled();
  });

  it('an option command has its copy button beside it, not over it', () => {
    renderPanel({ ...v6, actions: [{ ...v6.actions[0], execute_command: 'gh pr create --repo org/app --title x' }], loop: ready });
    const block = screen.getByTestId('remediation-loop-option-command-1');
    expect(within(block).getAllByRole('button')).toHaveLength(1);
  });
});

describe('Round 14: Fixing ends on the run, and "came back" per attempt', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;

  it('approve → the run row appears while the conversation is still busy → the card leaves "Running the change"', async () => {
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    const props = baseProps();
    const busyWithApproval = { ...v6, loop: { ...ready, conversation_busy: true, pending_approvals: [webApproval] } };
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={busyWithApproval} />);
    fireEvent.click(screen.getByTestId('remediation-loop-approve'));
    expect(screen.getByTestId('remediation-loop-running-change')).toBeInTheDocument();
    await waitFor(() => expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalled());
    // Busy with something else, no approval listed, the run row recorded.
    const busy = { ...v6, loop: { ...ready, conversation_busy: true } };
    rerender(
      <RemediationLoopPanel {...props} remediation={busy} resolutions={[withFollowup(undefined, { created_at: new Date().toISOString() })]} />
    );
    expect(screen.queryByTestId('remediation-loop-running-change')).toBeNull();
    expect(where()).toHaveTextContent('Checking the change');
    // Then resolved: the card says so, whatever the conversation is doing.
    rerender(
      <RemediationLoopPanel
        {...props}
        nbStatus='RESOLVED'
        remediation={busy}
        resolutions={[withFollowup({ verdict: 'fixed', verdict_at: new Date().toISOString() }, { created_at: new Date().toISOString() })]}
      />
    );
    expect(headline()).toHaveTextContent('✓ Fixed by Nubi');
  });

  it('attempt 1 keeps "Came back" after a later attempt ran; numbers stay 1, 2 (denied), 3', () => {
    const firings = [
      { event_id: 'evt-main', starts_at: Z('06:53'), ends_at: Z('07:20') },
      { event_id: 'refire', starts_at: Z('07:38'), ends_at: Z('09:32') },
    ];
    const first = {
      ...run,
      id: 'run-1',
      created_at: Z('07:18'),
      updated_at: Z('07:18'),
      data: { ...run.data, plan_version: 'pv1', followup: { done_at: Z('07:25'), outcome: 'reported', summary: '**Status:** Fixed; restored.' } },
    };
    const third = {
      ...run,
      id: 'run-7',
      created_at: Z('09:27'),
      updated_at: Z('09:27'),
      data: {
        ...run.data,
        plan_version: 'pv3',
        followup: { verdict: 'fixed', verdict_reason: 'cleared_after_change_and_held', verdict_at: Z('09:32') },
      },
    };
    renderPanel(
      {
        ...v6,
        version: 3,
        plan_version: 'pv3',
        generated_at: Z('08:55'),
        loop: {
          ...ready,
          firings,
          // The server's re-fire record describes only the newest run.
          refire_after_run: { run_id: 'run-7', fired_at: null, minutes_after: null, cause: 'unknown' },
          denied_approvals: [
            {
              message_id: 'm9',
              command: 'kubectl set image deployment/web nginx=nginx:1.27-alpine -n demo',
              option: 1,
              plan_version: 'pv3',
              denied_at: Z('08:54'),
            },
          ],
        },
      },
      { resolutions: [third, first] }
    );
    const attempts = screen.getAllByTestId('remediation-loop-activity-run');
    expect(attempts.map((a) => within(a).getByTestId('remediation-loop-attempt-title').textContent.split(' · ')[0])).toEqual([
      'Attempt 1',
      'Attempt 2',
      'Attempt 3',
    ]);
    expect(attempts[0]).toHaveTextContent('Came back');
    expect(attempts[1]).toHaveTextContent('Denied');
    expect(attempts[2]).toHaveTextContent('Likely fixed');
    expect(attempts[2]).not.toHaveTextContent('Came back');
    fireEvent.click(within(attempts[0]).getByTestId('remediation-loop-attempt-toggle'));
    expect(screen.getByTestId('remediation-loop-step-came-back')).toHaveTextContent(
      `Alert fired again at ${clockOnly(Z('07:38'))} — 20 min after this fix`
    );
  });
});

describe('Preparing ends when the new plan lands', () => {
  it('a newer plan version ends "preparing other fixes" even while the read still says preparing', async () => {
    apiKubernetes.askRemediationPlan.mockResolvedValue({});
    const props = baseProps();
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: ready }} />);
    fireEvent.click(screen.getByTestId('remediation-loop-replan-open'));
    fireEvent.click(screen.getByRole('button', { name: 'Too risky' }));
    fireEvent.click(screen.getByTestId('remediation-loop-replan-submit'));
    await waitFor(() => expect(headline()).toHaveTextContent('Nubi is preparing other fixes from your note'));
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, plan_status: 'preparing' } }} />);
    expect(headline()).toHaveTextContent('Nubi is preparing other fixes from your note');
    rerender(
      <RemediationLoopPanel {...props} remediation={{ ...v6, version: 7, plan_version: 'pv7', loop: { ...ready, plan_status: 'preparing' } }} />
    );
    expect(headline()).toHaveTextContent('Fix options ·');
  });

  it('a plan written after the newest request ends preparing on any tab', () => {
    renderPanel({
      ...v6,
      generated_at: '2026-10-22T20:12:00Z',
      loop: { ...ready, plan_status: 'preparing', last_request_at: '2026-10-22T20:10:00Z' },
    });
    expect(headline()).toHaveTextContent('Fix options ·');
  });
});

describe('Approve: from the click until the run is recorded', () => {
  it('switches to "Fixing … Running the change" at once, polls, then follows the run', async () => {
    apiAskNudgebee.aiGenerateInvestigate.mockResolvedValue(sentOk);
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    jest.useFakeTimers();
    try {
      const props = baseProps();
      const { rerender } = render(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: ready }} />);
      fireEvent.click(screen.getByTestId('remediation-loop-ask-option-2'));
      await screen.findByTestId('remediation-loop-preparing-change');
      const waiting = { ...v6, loop: { ...ready, conversation_busy: true, pending_approvals: [webApproval] } };
      rerender(<RemediationLoopPanel {...props} remediation={waiting} />);
      fireEvent.click(screen.getByTestId('remediation-loop-approve'));
      expect(headline()).toHaveTextContent('Fixing: Live patch the Deployment');
      expect(screen.getByTestId('remediation-loop-running-change')).toHaveTextContent('Running the change');
      expect(screen.queryByTestId('remediation-loop-approve')).toBeNull();
      // The read still lists the approval for a moment: the card stays on Fixing.
      rerender(<RemediationLoopPanel {...props} remediation={waiting} />);
      expect(headline()).toHaveTextContent('Fixing: Live patch the Deployment');
      // The panel does not poll on its own: it tells the page it is waiting, and the page keeps reading.
      props.onReload.mockClear();
      act(() => jest.advanceTimersByTime(10000));
      expect(props.onReload).not.toHaveBeenCalled();
      expect(props.onWaitingChange).toHaveBeenLastCalledWith(true);
      // The run row lands: the card follows it.
      rerender(
        <RemediationLoopPanel
          {...props}
          remediation={{ ...v6, loop: { ...ready, conversation_busy: true } }}
          resolutions={[withFollowup(undefined)]}
        />
      );
      expect(where()).toHaveTextContent('Checking the change');
    } finally {
      jest.useRealTimers();
    }
  });

  it('no run and the conversation goes idle: Nubi didn’t run it', async () => {
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    const props = baseProps();
    const { rerender } = render(
      <RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, conversation_busy: true, pending_approvals: [webApproval] } }} />
    );
    fireEvent.click(screen.getByTestId('remediation-loop-approve'));
    await waitFor(() => expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalled());
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, conversation_busy: true } }} />);
    expect(headline()).toHaveTextContent('Fixing');
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, conversation_busy: false } }} />);
    expect(headline()).toHaveTextContent('Nubi didn’t run it — see the reply in chat');
  });

  it('the panel never opens the chat by itself', async () => {
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    const props = baseProps();
    const { rerender } = render(
      <RemediationLoopPanel {...props} remediation={{ ...v6, loop: { ...ready, conversation_busy: true, pending_approvals: [webApproval] } }} />
    );
    fireEvent.click(screen.getByTestId('remediation-loop-approve'));
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...v6, loop: { ...ready, conversation_busy: false } }}
        resolutions={[withFollowup(undefined)]}
      />
    );
    rerender(
      <RemediationLoopPanel {...props} nbStatus='RESOLVED' remediation={{ ...v6, loop: ready }} resolutions={[withFollowup({ verdict: 'fixed' })]} />
    );
    expect(props.onOpenChat).not.toHaveBeenCalled();
  });
});

describe('Layout', () => {
  it('timeline and actions side by side (actions first in the page order, so they stack on top when narrow); one column when nothing has happened', () => {
    const { unmount } = render(
      <RemediationLoopPanel {...baseProps()} remediation={{ ...v6, loop: ready }} resolutions={[withFollowup(undefined)]} />
    );
    const columns = screen.getByTestId('remediation-loop-columns');
    const [first, second] = Array.from(columns.children);
    expect(first).toBe(screen.getByTestId('remediation-loop-actions-column'));
    expect(second).toBe(screen.getByTestId('remediation-loop-timeline-column'));
    expect(within(first).getByTestId('remediation-loop-where')).toBeInTheDocument();
    expect(within(first).getByTestId('remediation-loop-options')).toBeInTheDocument();
    expect(within(second).getByText('Timeline')).toBeInTheDocument();
    unmount();
    render(<RemediationLoopPanel {...baseProps()} remediation={{ actions: null, loop: { plan_status: 'none' } }} />);
    expect(screen.queryByTestId('remediation-loop-columns')).toBeNull();
    expect(screen.queryByTestId('remediation-loop-timeline-column')).toBeNull();
    expect(screen.getByTestId('remediation-loop-actions-column')).toBeInTheDocument();
  });
});

describe('Round 8: hardening and one preparing state', () => {
  const hardening = [
    { target: 'deploy/db', change: 'Alert when memory passes 80% of the limit', reason: 'catches the next squeeze before an OOM' },
    { change: 'Pin the image digest' },
  ];

  it('optional hardening sits under the options, apart from the fix', () => {
    renderPanel({ ...v6, optional_hardening: hardening, loop: ready });
    const card = screen.getByTestId('remediation-loop-options');
    expect(within(card).getByTestId('remediation-optional-hardening')).toHaveTextContent(
      'Optional hardening — not required to resolve this incident'
    );
    expect(within(card).getByTestId('remediation-optional-hardening')).toHaveTextContent(
      'deploy/db: Alert when memory passes 80% of the limit — catches the next squeeze'
    );
  });

  it('collapsed, the plan line counts the ideas; resolved, they read "Prevent it next time" under the summary', () => {
    const won = withFollowup({ verdict: 'fixed', verdict_at: '2026-10-22T20:25:00Z' });
    renderPanel({ ...v6, optional_hardening: hardening, loop: ready }, { nbStatus: 'RESOLVED', resolutions: [won] });
    expect(screen.getByTestId('remediation-loop-plan-toggle')).toHaveTextContent(
      `Fix options · from the investigation at ${clockOnly(v6.generated_at)} · Live patch the Deployment · 2 hardening ideas ▸`
    );
    const top = where();
    expect(within(top).getByTestId('remediation-optional-hardening')).toHaveTextContent('Prevent it next time');
    expect(within(top).getByTestId('remediation-optional-hardening')).toHaveTextContent('Pin the image digest');
  });

  it('replan submitted: the form closes, the top card says which plan is being written from the note, the plan is dimmed', async () => {
    apiKubernetes.askRemediationPlan.mockResolvedValue({});
    renderPanel({ ...v6, loop: ready });
    fireEvent.click(screen.getByTestId('remediation-loop-replan-open'));
    fireEvent.click(screen.getByRole('button', { name: 'Too risky' }));
    fireEvent.change(screen.getByTestId('remediation-loop-replan-note'), { target: { value: 'Use the GitOps repo,\n not a live patch' } });
    fireEvent.click(screen.getByTestId('remediation-loop-replan-submit'));
    await waitFor(() => expect(screen.queryByTestId('remediation-loop-replan')).toBeNull());
    expect(headline()).toHaveTextContent('Nubi is preparing other fixes from your note');
    expect(screen.getByTestId('remediation-loop-replan-note-quote')).toHaveTextContent('“Use the GitOps repo, not a live patch”');
    expect(screen.getByTestId('remediation-loop-plan-replaced')).toHaveTextContent('being replaced');
    expect(screen.queryByTestId('remediation-loop-replan-open')).toBeNull();
    expect(screen.queryByTestId('remediation-loop-ask-option-1')).toBeNull();
    expect(screen.queryByText(/preparing new fix options/)).toBeNull();
    expect(screen.getAllByText(/preparing other fixes/)).toHaveLength(1);
  });

  it('re-check: one preparing state, in the top card only', async () => {
    apiKubernetes.askRemediationPlan.mockResolvedValue({});
    renderPanel({ ...v6, loop: { ...ready, relevance: { ...ready.relevance, stale: true, stale_reason: 'written more than 24 h ago' } } });
    fireEvent.click(screen.getByTestId('remediation-loop-recheck'));
    await waitFor(() => expect(headline()).toHaveTextContent('Nubi is re-checking the fix'));
    expect(screen.getAllByText(/re-checking/)).toHaveLength(1);
    expect(screen.getByTestId('remediation-loop-plan-replaced')).toBeInTheDocument();
  });
});

describe('Round 9', () => {
  const done = (followup, over = {}) => withFollowup({ claimed_at: '2026-10-22T20:14:00Z', ...followup }, over);

  it('needs_human: "Needs you" and why', () => {
    renderPanel(
      { ...v6, loop: ready },
      { resolutions: [done({ done_at: '2026-10-22T20:55:00Z', outcome: 'needs_human', reason: 'attempt_limit' })] }
    );
    expect(screen.getByTestId('remediation-loop-activity-run')).toHaveTextContent('Needs you');
    expect(screen.getByTestId('remediation-loop-step-needs-human')).toHaveTextContent(
      'Nubi needs a person: several steps were tried for this alert in the last 24 hours'
    );
    expect(headline()).toHaveTextContent('Nubi needs a person');
  });

  it('one card: the state headline, then the plan with what each option runs, Details, and "Use this fix"', () => {
    renderPanel({
      ...v6,
      actions: [
        { ...v6.actions[0], execute_command: 'gh pr create --repo org/app' },
        { ...v6.actions[1], execute_command: 'kubectl set resources deploy/db --limits=memory=256Mi' },
      ],
      loop: ready,
    });
    const card = where();
    expect(within(card).getByTestId('remediation-loop-status')).toHaveTextContent('Fix options · from the investigation at');
    expect(within(card).getByTestId('remediation-loop-options')).toHaveTextContent('Fix options');
    expect(within(card).getByTestId('remediation-loop-option-command-2')).toHaveTextContent('kubectl set resources deploy/db --limits=memory=256Mi');
    expect(screen.getByTestId('remediation-loop-option-2')).not.toHaveTextContent('Trade-off');
    fireEvent.click(screen.getByTestId('remediation-loop-option-details-2'));
    expect(screen.getByTestId('remediation-loop-option-2')).toHaveTextContent('Trade-off: Argo CD puts 100Mi back');
    expect(screen.getByTestId('remediation-loop-option-2')).toHaveTextContent('kubectl set resources deploy/db --limits=memory=100Mi');
    expect(screen.getByTestId('remediation-loop-ask-option-1')).toHaveTextContent('Use this fix');
    // The replan link sits beside the plan title, not at the bottom.
    expect(within(card).getByTestId('remediation-loop-replan-open')).toBeInTheDocument();
  });

  it('an approval needs the whole card: the plan folds to one line', () => {
    renderPanel({ ...v6, loop: { ...ready, pending_approvals: [webApproval] } });
    expect(screen.getByTestId('remediation-loop-plan-toggle')).toHaveTextContent(
      `Fix options · from the investigation at ${clockOnly(v6.generated_at)} ▸`
    );
    expect(screen.queryByTestId('remediation-loop-option-1')).toBeNull();
  });

  it('confidence shows only when it tells the options apart, and never the default 50', () => {
    const same = { ...v6, actions: v6.actions.map((a) => ({ ...a, confidence: 50 })), loop: ready };
    const { unmount } = render(<RemediationLoopPanel {...baseProps()} remediation={same} />);
    expect(screen.queryByText(/likely to resolve/)).toBeNull();
    unmount();
    render(
      <RemediationLoopPanel {...baseProps()} remediation={{ ...v6, actions: v6.actions.map((a) => ({ ...a, confidence: 80 })), loop: ready }} />
    );
    expect(screen.queryByText(/likely to resolve/)).toBeNull();
  });

  it('long root cause and done-when are kept to two lines with "more"', () => {
    const long =
      'The memory limit was lowered in the GitOps source below what PostgreSQL needs for its shared buffers, so it restarts during checkpoints every few minutes.';
    renderPanel({ ...v6, root_cause: long, goal: 'Short goal', loop: ready });
    expect(within(screen.getByTestId('remediation-loop-root-cause')).getByRole('button', { name: 'more' })).toBeInTheDocument();
    expect(within(screen.getByTestId('remediation-loop-goal')).queryByRole('button', { name: 'more' })).toBeNull();
  });
});

describe('answerApproval', () => {
  it('sends nothing and says to answer in chat when the approval has no conversation_id', async () => {
    await expect(answerApproval({ accountId: 'acc-1', approval: { ...webApproval, conversation_id: '' }, approve: true })).rejects.toThrow(
      'Answer it in chat.'
    );
    expect(apiAskNudgebee.aiFollowupResponse).not.toHaveBeenCalled();
  });

  it('shows "Answer it in chat" on the card instead of sending', async () => {
    renderPanel({ ...plan, loop: { ...ready, pending_approvals: [{ ...webApproval, conversation_id: '' }] } });
    fireEvent.click(screen.getByTestId('remediation-loop-approve'));
    expect(await screen.findByText('Answer it in chat.')).toBeInTheDocument();
    expect(apiAskNudgebee.aiFollowupResponse).not.toHaveBeenCalled();
  });
});

describe('RemediationResolutionCard', () => {
  it('shows cause, fix, proof and who fixed it once the loop resolved the event', () => {
    const resolved = {
      ...run,
      data: { ...run.data, followup: { verdict: 'fixed', verdict_at: '2026-10-22T20:30:00Z', verdict_reason: '0 OOMKilled for 5 min' } },
    };
    render(
      <RemediationResolutionCard
        labels={{ nb_resolved_by: JSON.stringify({ run_id: 'run-1', verdict: 'fixed', cause_key: { what: 'oom', how: 'x', against: 'y' } }) }}
        nbStatus='RESOLVED'
        remediation={plan}
        resolutions={[resolved]}
      />
    );
    const card = screen.getByTestId('remediation-resolution-card');
    expect(card).toHaveTextContent('Resolved by a fix');
    expect(card).toHaveTextContent(plan.root_cause);
    expect(card).toHaveTextContent(webApproval.command);
    expect(card).toHaveTextContent('0 OOMKilled for 5 min');
    expect(card).toHaveTextContent('Asha');
  });

  it('says the alert cleared before the change', () => {
    render(
      <RemediationResolutionCard
        labels={{ nb_resolved_by: { run_id: 'run-1', verdict: 'cleared_before_change' } }}
        nbStatus='RESOLVED'
        resolutions={[run]}
      />
    );
    expect(screen.getByTestId('remediation-resolution-card')).toHaveTextContent('cleared before the change');
  });

  it('labels a re-fire, and renders nothing without loop labels (loop off)', () => {
    const { container, rerender } = render(<RemediationResolutionCard labels={{}} nbStatus='RESOLVED' resolutions={[]} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<RemediationResolutionCard labels={{ nb_refire: { kind: 'unknown', previous_run_id: 'run-1' } }} nbStatus='OPEN' resolutions={[]} />);
    expect(screen.getByTestId('remediation-refire-label')).toHaveTextContent('Fired again');
  });
});

describe('Round 16: a refresh after the plan, and the timeline from the first firing', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const plan7 = { ...v6, version: 7, plan_version: 'pv7', generated_at: Z('10:00') };
  const refreshed = { at: Z('10:30') };

  it('an approval on a plan older than the investigation: the line, Approve anyway / Deny and re-check plan, then the re-check', async () => {
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    const props = { ...baseProps(), onWaitingChange: jest.fn() };
    const waiting = { ...plan7, loop: { ...ready, pending_approvals: [webApproval], investigation_after_plan: refreshed, parked_recheck: true } };
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={waiting} />);
    expect(screen.getByTestId('remediation-loop-approval-refreshed')).toHaveTextContent(
      `The investigation was refreshed at ${clockOnly(refreshed.at)}, after these fixes`
    );
    expect(screen.getByTestId('remediation-loop-approve')).toHaveTextContent('Approve anyway');
    expect(screen.getByTestId('remediation-loop-deny')).toHaveTextContent('Deny and re-check the fix');
    fireEvent.click(screen.getByTestId('remediation-loop-deny'));
    await waitFor(() => expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalledWith(expect.objectContaining({ query: 'no' })));
    // The planner's next tick starts it: until the read says preparing, the tab says it will.
    rerender(
      <RemediationLoopPanel {...props} remediation={{ ...plan7, loop: { ...ready, investigation_after_plan: refreshed, parked_recheck: true } }} />
    );
    await waitFor(() => expect(headline()).toHaveTextContent('Nubi will re-check the fix'));
    expect(props.onWaitingChange).toHaveBeenLastCalledWith(true);
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={{ ...plan7, loop: { ...ready, plan_status: 'preparing', last_request_mode: 'auto', investigation_after_plan: refreshed } }}
      />
    );
    await waitFor(() => expect(headline()).toHaveTextContent('Nubi is re-checking the fix'));
    // The re-checked plan lands.
    rerender(<RemediationLoopPanel {...props} remediation={{ ...v6, version: 8, plan_version: 'pv8', generated_at: Z('10:40'), loop: ready }} />);
    await waitFor(() => expect(headline()).not.toHaveTextContent('re-check'));
  });

  it('a plain approval keeps Approve / Deny and no refresh line', () => {
    renderPanel({ ...plan7, loop: { ...ready, pending_approvals: [webApproval] } });
    expect(screen.queryByTestId('remediation-loop-approval-refreshed')).toBeNull();
    expect(screen.getByTestId('remediation-loop-approve')).toHaveTextContent(/^Approve$/);
    expect(screen.getByTestId('remediation-loop-deny')).toHaveTextContent(/^Deny$/);
  });

  it('the investigation newer than the plan and nothing running: the plan stays pickable, with Re-check plan', () => {
    apiKubernetes.askRemediationPlan.mockReturnValue(pending());
    renderPanel({
      ...plan7,
      loop: {
        ...ready,
        investigation_after_plan: refreshed,
        last_request_mode: 'auto',
        last_request_at: Z('10:31'),
        plan_status_reason: 'conversation stayed busy 1h0m0s',
      },
    });
    expect(screen.queryByTestId('remediation-loop-plan-replaced')).toBeNull();
    expect(screen.getByTestId('remediation-loop-refreshed-after-plan')).toHaveTextContent('The investigation was refreshed after these fixes');
    expect(screen.getByTestId('remediation-loop-ask-option-1')).toHaveTextContent('Use this fix anyway');
    expect(screen.getByTestId('remediation-loop-ask-option-1')).toBeEnabled();
    expect(screen.queryByTestId('remediation-loop-request-no-plan')).toBeNull();
    fireEvent.click(screen.getByTestId('remediation-loop-refreshed-recheck'));
    expect(apiKubernetes.askRemediationPlan).toHaveBeenCalledWith({ accountId: 'acc-1', eventId: 'ev-1', mode: 'recheck' });
  });

  it.each([
    ['preparing', { plan_status: 'preparing', last_request_mode: 'auto' }],
    ['parked', { parked_recheck: true }],
  ])('the investigation newer than the plan and a re-check %s: the plan is being re-checked', (_, over) => {
    renderPanel({ ...plan7, loop: { ...ready, investigation_after_plan: refreshed, ...over } });
    expect(screen.queryByTestId('remediation-loop-refreshed-after-plan')).toBeNull();
    expect(screen.getByText(/being re-checked/)).toBeInTheDocument();
  });

  it('"after the alert came back" only when a change ran before the re-fire', () => {
    const firings = [
      { event_id: 'e1', starts_at: Z('09:00'), ends_at: Z('09:20'), status: 'RESOLVED', nb_status: 'OPEN' },
      { event_id: 'e2', starts_at: Z('09:40'), ends_at: null, status: 'FIRING', nb_status: 'OPEN' },
    ];
    renderPanel({ ...plan7, loop: { ...ready, firings } });
    expect(screen.getByTestId('remediation-loop-timeline-plan')).toHaveTextContent('Fix options updated');
    expect(screen.getByTestId('remediation-loop-timeline-plan')).not.toHaveTextContent('after the alert came back');
  });

  it('no plan yet, but the alert has fired: the timeline shows its firings', () => {
    renderPanel({
      actions: null,
      loop: { plan_status: 'none', firings: [{ event_id: 'e1', starts_at: Z('09:00'), ends_at: null, status: 'FIRING', nb_status: 'OPEN' }] },
    });
    expect(headline()).toHaveTextContent('No fix was prepared from the investigation yet');
    expect(screen.getByTestId('remediation-loop-timeline-column')).toBeInTheDocument();
    expect(screen.getByTestId('remediation-loop-alert-fired')).toHaveTextContent('Alert fired');
  });
});

describe('Round 16 add-on: nothing to change, re-check label', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const firings = [
    { event_id: 'e1', starts_at: Z('08:00'), ends_at: Z('08:20'), status: 'RESOLVED', nb_status: 'OPEN' },
    { event_id: 'e2', starts_at: Z('09:00'), ends_at: Z('09:20'), status: 'RESOLVED', nb_status: 'OPEN' },
  ];

  it('"found nothing to change" does not repeat the root cause, and the timeline shows the firings', () => {
    renderPanel({
      version: 2,
      plan_version: 'pv2',
      generated_at: Z('10:00'),
      actions: [],
      summary: 'Closed on its own at 2026-10-22 19:20 UTC.',
      root_cause: 'The alert closed on its own at 2026-10-22 19:20 UTC.',
      loop: { ...ready, firings },
    });
    expect(headline()).toHaveTextContent('Nubi found nothing to change');
    expect(where()).not.toHaveTextContent('Closed on its own at 2026-10-22 19:20 UTC.');
    expect(screen.getAllByText(/closed on its own at 2026-10-22 19:20 UTC/i)).toHaveLength(1);
    expect(screen.getByTestId('remediation-loop-timeline-column')).toBeInTheDocument();
  });

  it('a plan written for the person’s Re-check says so, with its time', () => {
    renderPanel({
      ...v6,
      version: 2,
      plan_version: 'pv2',
      generated_at: Z('10:05'),
      loop: { ...ready, firings, last_request_mode: 'recheck', last_request_at: Z('10:01') },
    });
    expect(screen.getByTestId('remediation-loop-timeline-plan')).toHaveTextContent('Fix options updated (re-check)');
  });
});

describe('Round 17: code fixes, PRs and changes a person makes', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const codeFix = {
    action: 'Cap MaxOutputSize in fetch worker',
    kind: 'fix',
    delivery: 'source',
    execute_command: '',
    required_change: {
      where: 'code',
      target: 'worker/fetch.go',
      change: 'Set a bounded MaxOutputSize on FetchOptions',
      repo: 'example-org/worker',
      reason: 'Unbounded output buffering exhausts the container memory',
    },
  };
  const codePlan = {
    version: 3,
    plan_version: 'pv-code',
    generated_at: Z('10:00'),
    root_cause: 'The fetch worker buffers command output without a limit. It ran out of memory.',
    actions: [codeFix],
    loop: { ...ready, firings: [{ event_id: 'e1', starts_at: Z('09:00'), ends_at: Z('09:30'), status: 'RESOLVED', nb_status: 'OPEN' }] },
  };
  const sourcePlan = {
    version: 1,
    plan_version: 'pv-src',
    generated_at: Z('10:00'),
    root_cause: 'The app appends to an in-memory cache without eviction.',
    actions: [
      {
        action: 'Bound the cache in the app',
        kind: 'fix',
        delivery: 'source',
        execute_command: '',
        required_change: { where: 'deployment_source', target: 'deployment/leaky-app', change: 'Add an LRU bound to the cache' },
      },
      {
        action: 'Raise the memory limit',
        kind: 'mitigation',
        execute_command: 'kubectl set resources deploy/leaky-app --limits=memory=1Gi',
      },
    ],
    loop: ready,
  };
  const prRow = (over = {}) => ({
    id: 'pr-1',
    type: 'PullRequest',
    status: 'InProgress',
    type_reference_id: 'https://github.com/example-org/worker/pull/571',
    created_at: Z('10:06'),
    updated_at: Z('10:06'),
    pr_lifecycle_state: 'created',
    data: { pr_url: 'https://github.com/example-org/worker/pull/571', pr_number: 0, repo_url: 'https://github.com/example-org/worker' },
    ...over,
  });

  it('a code fix with a repo: labelled Code fix, shows the change, offers Raise a PR and never "Use this fix"', () => {
    renderPanel(codePlan);
    expect(headline()).toHaveTextContent('Cause is in code — fix needs a change');
    const row = screen.getByTestId('remediation-loop-option-1');
    expect(row).toHaveTextContent('Code fix');
    const change = screen.getByTestId('remediation-loop-option-change-1');
    expect(change).toHaveTextContent('worker/fetch.go');
    expect(change).toHaveTextContent('repo example-org/worker');
    expect(change).toHaveTextContent('Set a bounded MaxOutputSize on FetchOptions');
    expect(change).toHaveTextContent('Why: Unbounded output buffering exhausts the container memory');
    expect(screen.queryByTestId('remediation-loop-ask-option-1')).toBeNull();
    expect(screen.queryByTestId('remediation-loop-option-command-1')).toBeNull();
    expect(screen.getByTestId('remediation-loop-raise-pr-1')).toHaveTextContent('Raise a PR');
  });

  it('Raise a PR sends one message into the alert chat, waits like "Go with N", and the PR becomes that option’s attempt', async () => {
    let resolveSend;
    apiAskNudgebee.aiGenerateInvestigate.mockReturnValue(
      new Promise((r) => {
        resolveSend = r;
      })
    );
    const props = baseProps();
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={codePlan} />);
    fireEvent.click(screen.getByTestId('remediation-loop-raise-pr-1'));
    expect(screen.getByTestId('remediation-loop-raise-pr-1')).toHaveTextContent('Asking Nubi…');
    expect(apiAskNudgebee.aiGenerateInvestigate).toHaveBeenCalledWith(
      expect.objectContaining({
        session_id: 'event-fp1',
        query:
          'Raise a PR for option 1 of plan v3: Set a bounded MaxOutputSize on FetchOptions in worker/fetch.go (repo example-org/worker). Root cause: The fetch worker buffers command output without a limit.',
      })
    );
    await act(async () => resolveSend(sentOk));
    expect(headline()).toHaveTextContent('Raising a PR: Cap MaxOutputSize in fetch worker');
    // The PR row names no option: it is the code fix picked here.
    rerender(<RemediationLoopPanel {...props} remediation={codePlan} resolutions={[prRow({ created_at: new Date().toISOString() })]} />);
    expect(headline()).toHaveTextContent('PR open: Cap MaxOutputSize in fetch worker');
    expect(screen.getByTestId('remediation-loop-attempt-title')).toHaveTextContent('Cap MaxOutputSize in fetch worker');
    expect(screen.getByTestId('remediation-loop-step-pr')).toHaveTextContent('PR raised · #571 ↗ · open');
    expect(screen.getByTestId('remediation-loop-activity-run')).toHaveTextContent('PR open');
    // Merged: no automatic verdict, the person marks it fixed once deployed.
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Success' });
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={codePlan}
        resolutions={[prRow({ created_at: new Date().toISOString(), pr_lifecycle_state: 'merged', status: 'Success' })]}
      />
    );
    expect(headline()).toHaveTextContent('PR merged — mark fixed after it’s deployed');
    fireEvent.click(screen.getByTestId('remediation-loop-pr-mark-fixed'));
    await waitFor(() =>
      expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledWith('acc-1', '', 'ev-1', undefined, 'manual', undefined, codeFix.action, {
        planVersion: 'pv-code',
      })
    );
  });

  it('a PR row with no option and no pick here stands as "PR raised"', () => {
    renderPanel(codePlan, { resolutions: [prRow()] });
    expect(screen.getByTestId('remediation-loop-attempt-title')).toHaveTextContent('Attempt 1 · PR raised');
    expect(screen.getByTestId('remediation-loop-step-pr')).toHaveTextContent('PR raised · #571 ↗ · open');
  });

  it('a change outside code, or with no repo: make it yourself, then mark it done; the mitigation still buys time', async () => {
    apiKubernetes.executeRemediationCommand.mockResolvedValue({ status: 'Success' });
    renderPanel(sourcePlan);
    expect(headline()).toHaveTextContent('Fix options · from the investigation at');
    const first = screen.getByTestId('remediation-loop-option-1');
    expect(first).toHaveTextContent('Change in the deployment source');
    expect(screen.getByTestId('remediation-loop-option-manual-1')).toHaveTextContent('Make this change yourself, then mark it done.');
    expect(screen.queryByTestId('remediation-loop-raise-pr-1')).toBeNull();
    expect(screen.queryByTestId('remediation-loop-ask-option-1')).toBeNull();
    const second = screen.getByTestId('remediation-loop-option-2');
    expect(second).toHaveTextContent('Buys time');
    expect(screen.getByTestId('remediation-loop-ask-option-2')).toHaveTextContent('Use this fix');
    fireEvent.click(screen.getByTestId('remediation-loop-mark-done-1'));
    await waitFor(() =>
      expect(apiKubernetes.executeRemediationCommand).toHaveBeenCalledWith(
        'acc-1',
        '',
        'ev-1',
        undefined,
        'manual',
        undefined,
        'Bound the cache in the app',
        { planVersion: 'pv-src' }
      )
    );
  });

  it('a code fix with no repo is a change to make, with no PR button', () => {
    renderPanel({ ...codePlan, actions: [{ ...codeFix, required_change: { ...codeFix.required_change, repo: '' } }] });
    expect(screen.queryByTestId('remediation-loop-raise-pr-1')).toBeNull();
    expect(screen.getByTestId('remediation-loop-mark-done-1')).toBeInTheDocument();
    expect(headline()).toHaveTextContent('Cause is in code — fix needs a change');
  });

  it('No plan yet says why and what fired, shows the firings, and no duplicate banner without primary_event_id', () => {
    renderPanel(
      { actions: null, loop: { plan_status: 'none', firings: [{ event_id: 'e1', starts_at: Z('17:23'), ends_at: null, status: 'FIRING' }] } },
      { nbStatus: 'DUPLICATE', alertTitle: 'log-agent was OOMKilled' }
    );
    expect(headline()).toHaveTextContent('No fix was prepared from the investigation yet');
    expect(where()).toHaveTextContent(`log-agent was OOMKilled · firing since ${clockOnly(Z('17:23'))}`);
    expect(screen.getByTestId('remediation-loop-ask-plan')).toHaveTextContent('Prepare the fix');
    expect(screen.queryByTestId('remediation-loop-duplicate')).toBeNull();
    expect(screen.getByTestId('remediation-loop-timeline-column')).toBeInTheDocument();
  });

  it('No plan yet after a request names the request’s end', () => {
    renderPanel({ actions: null, loop: { plan_status: 'none', last_request_at: Z('10:00'), plan_status_reason: 'the planner timed out' } });
    expect(headline()).toHaveTextContent('No fix was prepared: the planner timed out');
    expect(screen.getByTestId('remediation-loop-ask-plan')).toHaveTextContent('Try again');
  });

  it('a summary that repeats its own first sentence says it once', () => {
    renderPanel({
      version: 2,
      plan_version: 'pv2',
      generated_at: Z('10:00'),
      actions: [],
      summary: 'Closed on its own at 2026-10-22 19:20 UTC. The alert closed on its own at 2026-10-22 19:20 UTC. The cause is in code.',
      loop: ready,
    });
    expect(where()).toHaveTextContent('Closed on its own at 2026-10-22 19:20 UTC. The cause is in code.');
    expect(where()).not.toHaveTextContent('The alert closed on its own');
  });
});

describe('Round 17: a PR attempt follows pr_lifecycle_state', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const plan = { ...v6, version: 3, plan_version: 'pv3', generated_at: Z('10:00'), loop: ready };
  const pr = (over) => ({
    id: 'pr-1',
    type: 'PullRequest',
    status: 'InProgress',
    type_reference_id: 'https://github.com/example-org/app/pull/123',
    created_at: Z('10:05'),
    updated_at: Z('10:05'),
    pr_lifecycle_state: null,
    last_pr_check_at: null,
    data: { pr_url: 'https://github.com/example-org/app/pull/123', pr_number: 0, plan_version: 'pv3', option: 1 },
    ...over,
  });
  const step = () => screen.getByTestId('remediation-loop-step-pr');

  it.each([
    ['created', 'InProgress', 'open', 'PR open'],
    ['needs_followup', 'InProgress', 'open', 'PR open'],
    ['addressing', 'InProgress', 'open', 'PR open'],
    ['stale', 'InProgress', 'went stale (no activity 3 days)', 'PR went stale (no activity 3 days)'],
    ['closed', 'Failed', 'closed', 'The PR was closed without merging'],
    ['unresolvable', 'InProgress', 'state unknown', 'PR state unknown'],
  ])('%s reads "%s"', (state, status, text, head) => {
    renderPanel(plan, { resolutions: [pr({ pr_lifecycle_state: state, status })] });
    expect(step()).toHaveTextContent(`PR raised · #123 ↗ · ${text}`);
    expect(headline()).toHaveTextContent(head);
  });

  it('merged: the merge time comes from last_pr_check_at, and the person marks it fixed', () => {
    renderPanel(plan, { resolutions: [pr({ pr_lifecycle_state: 'merged', status: 'Success', last_pr_check_at: Z('11:40') })] });
    expect(step()).toHaveTextContent('PR raised · #123 ↗ · merged');
    expect(headline()).toHaveTextContent('PR merged — mark fixed after it’s deployed');
    expect(where()).toHaveTextContent(`merged ${clockOnly(Z('11:40'))}`);
    expect(screen.getByTestId('remediation-loop-step-pr-merged')).toHaveTextContent(clockOnly(Z('11:40')));
    expect(screen.getByTestId('remediation-loop-pr-mark-fixed')).toBeInTheDocument();
  });

  it('with no state, the row status decides: in progress is open, Success merged, Failed closed', () => {
    const { unmount } = render(<RemediationLoopPanel {...baseProps()} remediation={plan} resolutions={[pr({})]} />);
    expect(step()).toHaveTextContent('· open');
    unmount();
    const second = render(<RemediationLoopPanel {...baseProps()} remediation={plan} resolutions={[pr({ status: 'Success' })]} />);
    expect(step()).toHaveTextContent('· merged');
    second.unmount();
    render(<RemediationLoopPanel {...baseProps()} remediation={plan} resolutions={[pr({ status: 'Failed' })]} />);
    expect(step()).toHaveTextContent('· closed');
  });
});

describe('Deny and re-check plan: the re-check runs whether or not one was parked', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const plan = { ...v6, version: 7, plan_version: 'pv7', generated_at: Z('10:00') };
  const refreshed = { at: Z('10:30') };
  const waitingLoop = (over) => ({ ...ready, pending_approvals: [webApproval], investigation_after_plan: refreshed, ...over });

  it('nothing parked: after the deny the tab asks for the re-check itself', async () => {
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    apiKubernetes.askRemediationPlan.mockResolvedValue({});
    renderPanel({ ...plan, loop: waitingLoop({}) });
    fireEvent.click(screen.getByTestId('remediation-loop-deny'));
    await waitFor(() => expect(apiKubernetes.askRemediationPlan).toHaveBeenCalledWith({ accountId: 'acc-1', eventId: 'ev-1', mode: 'recheck' }));
    expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalledWith(expect.objectContaining({ query: 'no' }));
  });

  it('a re-check parked behind the approval: the deny alone, no request from the tab', async () => {
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    renderPanel({ ...plan, loop: waitingLoop({ parked_recheck: true }) });
    fireEvent.click(screen.getByTestId('remediation-loop-deny'));
    await waitFor(() => expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalled());
    await act(async () => {});
    expect(apiKubernetes.askRemediationPlan).not.toHaveBeenCalled();
  });

  it('a plain Deny (investigation not newer) asks for nothing', async () => {
    apiAskNudgebee.aiFollowupResponse.mockResolvedValue({});
    renderPanel({ ...plan, loop: { ...ready, pending_approvals: [webApproval] } });
    fireEvent.click(screen.getByTestId('remediation-loop-deny'));
    await waitFor(() => expect(apiAskNudgebee.aiFollowupResponse).toHaveBeenCalled());
    await act(async () => {});
    expect(apiKubernetes.askRemediationPlan).not.toHaveBeenCalled();
  });

  it('while an approval waits, an older request’s "didn’t produce a plan" line is not shown', () => {
    renderPanel({
      ...plan,
      loop: waitingLoop({ last_request_mode: 'auto', last_request_at: Z('09:00'), plan_status_reason: 'conversation stayed busy 1h0m0s' }),
    });
    expect(screen.getByTestId('remediation-loop-approve')).toBeInTheDocument();
    expect(screen.queryByTestId('remediation-loop-request-no-plan')).toBeNull();
  });
});

describe('Timeline when the read lists only the newest firings', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const at = (min) => new Date(Date.parse(Z('05:23')) + min * 60000).toISOString();
  // The newest 20 firings, from 05:23 on, all still firing; the alert first fired at 04:52 (not listed).
  const firings = Array.from({ length: 20 }, (_, i) => ({
    event_id: `f${i}`,
    starts_at: at(i * 10),
    ends_at: null,
    status: 'FIRING',
    nb_status: 'OPEN',
  }));
  const plan = { ...v6, version: 1, plan_version: 'pv1', generated_at: Z('05:08'), loop: { ...ready, firings } };

  it('the first firing leads at its own time, before the plan; the listed ones read as firing on', () => {
    renderPanel(plan, { alertStartedAt: Z('04:52') });
    // A day heading shows once the fixture's day is not today; the order of the entries is what is tested.
    const rows = within(screen.getByTestId('remediation-loop-timeline-rows'))
      .getAllByRole('listitem')
      .map((li) => li.getAttribute('data-testid'))
      .filter((id) => id && id !== 'remediation-loop-day');
    expect(rows[0]).toBe('remediation-loop-alert-fired');
    expect(rows.indexOf('remediation-loop-alert-fired')).toBeLessThan(rows.indexOf('remediation-loop-timeline-plan'));
    expect(rows.indexOf('remediation-loop-timeline-plan')).toBeLessThan(rows.indexOf('remediation-loop-alert-still'));
    const fired = screen.getByTestId('remediation-loop-alert-fired');
    expect(fired).toHaveTextContent(`${clockOnly(Z('04:52'))}Alert fired`);
    expect(fired).toHaveTextContent('Earlier firings are not listed here');
    expect(screen.getByTestId('remediation-loop-alert-still')).toHaveTextContent(`Firing · 20 alerts ${clockOnly(at(0))}–${clockOnly(at(190))}`);
  });

  it('when the list starts with the alert’s first firing, it reads as before', () => {
    renderPanel(plan, { alertStartedAt: at(0) });
    expect(screen.getAllByTestId('remediation-loop-alert-fired')[0]).toHaveTextContent('Alert fired · still firing, 20 alerts');
    expect(screen.queryByText('Earlier firings are not listed here')).toBeNull();
  });
});

describe('A fixed attempt, then the event resolves', () => {
  const fixed = withFollowup(
    { claimed_at: '2026-10-22T20:14:00Z', verdict: 'fixed', verdict_reason: 'cleared_after_change_and_held', verdict_at: '2026-10-22T20:20:00Z' },
    { option: 2 }
  );
  const fixedRow = { ...fixed, data: { ...fixed.data, option: 2, plan_version: 'pv1' } };
  const res = { ...plan, version: 1, generated_at: '2026-10-22T20:00:00Z', loop: ready };

  it('reads "Likely fixed" with Resolve and the caveat, stops reading by itself, and nothing resolves it without a person', () => {
    render(<RemediationLoopPanel {...baseProps()} remediation={res} resolutions={[fixedRow]} nbStatus='OPEN' />);
    expect(headline()).toHaveTextContent('Likely fixed: Live patch the Deployment');
    expect(where()).toHaveTextContent('The alert cleared after the change and stayed clear.');
    expect(where()).toHaveTextContent('Based on the alert’s own firings: Nubi did not check that the alert’s monitoring is healthy.');
    expect(screen.getByTestId('remediation-loop-resolve')).toHaveTextContent('Resolve');
    expect(loopNeedsPoll({ loop: res.loop, res, rows: [fixedRow], nbStatus: 'OPEN' })).toBe(false);
    expect(apiKubernetes.confirmRemediationResolve).not.toHaveBeenCalled();
  });

  it('cleared before the change reads as a suggestion too', () => {
    const cleared = {
      ...fixedRow,
      data: { ...fixedRow.data, followup: { ...fixedRow.data.followup, verdict: 'cleared_before_change', verdict_reason: 'cleared_before_change' } },
    };
    render(<RemediationLoopPanel {...baseProps()} remediation={res} resolutions={[cleared]} nbStatus='OPEN' />);
    expect(headline()).toHaveTextContent('Cleared before the change');
    expect(screen.getByTestId('remediation-loop-resolve')).toBeInTheDocument();
  });

  it('Resolve resolves the event in the person’s name, then the event reads "Fixed by Nubi" once the row is read again', async () => {
    apiKubernetes.confirmRemediationResolve.mockResolvedValue({ events: [{ event_id: 'ev-1', prev_status: 'OPEN', new_status: 'RESOLVED' }] });
    const props = { ...baseProps(), onResolved: jest.fn() };
    const { rerender } = render(<RemediationLoopPanel {...props} remediation={res} resolutions={[fixedRow]} nbStatus='OPEN' />);
    await act(async () => fireEvent.click(screen.getByTestId('remediation-loop-resolve')));
    expect(apiKubernetes.confirmRemediationResolve).toHaveBeenCalledWith({ accountId: 'acc-1', eventId: 'ev-1', runId: 'run-1' });
    expect(props.onResolved).toHaveBeenCalledTimes(1);
    expect(props.onReload).toHaveBeenCalled();
    rerender(
      <RemediationLoopPanel
        {...props}
        remediation={res}
        resolutions={[fixedRow]}
        nbStatus='RESOLVED'
        labels={{ nb_resolved_by: JSON.stringify({ run_id: fixedRow.id, verdict: 'fixed', confirmed_by: 'u-1', confirmed_by_name: 'Asha' }) }}
      />
    );
    expect(headline()).toHaveTextContent('✓ Fixed by Nubi');
    expect(loopNeedsPoll({ loop: res.loop, res, rows: [fixedRow], nbStatus: 'RESOLVED' })).toBe(false);
  });

  it('a refused resolve says why, and keeps the button', async () => {
    apiKubernetes.confirmRemediationResolve.mockRejectedValue(new Error('this run has no likely-fixed verdict for the event'));
    render(<RemediationLoopPanel {...baseProps()} remediation={res} resolutions={[fixedRow]} nbStatus='OPEN' />);
    await act(async () => fireEvent.click(screen.getByTestId('remediation-loop-resolve')));
    expect(screen.getByTestId('remediation-loop-resolve-error')).toHaveTextContent('this run has no likely-fixed verdict for the event');
    expect(screen.getByTestId('remediation-loop-resolve')).toBeInTheDocument();
  });

  it('a change approved in the chat outside the plan, after the fix, is not taken for the attempt', () => {
    const outside = {
      ...run,
      id: 'run-2',
      created_at: '2026-10-22T20:30:00Z',
      data: {
        source: 'chat',
        tool: 'kubectl',
        command: 'kubectl get pods',
        approved_by: 'asha',
        slot: 'execute',
        followup: { done_at: '2026-10-22T20:31:00Z', outcome: 'settled', reason: 'not_a_plan_step' },
      },
    };
    render(<RemediationLoopPanel {...baseProps()} remediation={res} resolutions={[fixedRow, outside]} nbStatus='OPEN' />);
    expect(headline()).toHaveTextContent('Likely fixed: Live patch the Deployment');
  });

  it('No plan yet: firing since the start of the episode, not its newest repeat', () => {
    const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
    renderPanel(
      {
        actions: null,
        loop: {
          plan_status: 'none',
          firings: [
            { event_id: 'a', starts_at: Z('19:59'), ends_at: null, status: 'FIRING' },
            { event_id: 'b', starts_at: Z('20:10'), ends_at: null, status: 'FIRING' },
          ],
        },
      },
      { alertTitle: 'web was OOMKilled' }
    );
    expect(where()).toHaveTextContent(`web was OOMKilled · firing since ${clockOnly(Z('19:59'))}`);
  });
});

describe('Round 18: the tab is the investigation’s next steps, made runnable', () => {
  const Z = (hhmm) => `2026-10-23T${hhmm}:00Z`;
  const noPlan = (loop) => ({ actions: null, loop: { plan_status: 'none', ...loop } });

  it('investigation still running: says the fix comes from it, with its step, and offers nothing to press', () => {
    renderPanel(noPlan({}), { investigation: { state: 'running', stage: 'Step 2 of 4 · Investigating: collecting and reading the evidence' } });
    expect(headline()).toHaveTextContent('Nubi turns the investigation’s next steps into a fix it can run');
    expect(where()).toHaveTextContent('Investigation step 2 of 4 · Investigating: collecting and reading the evidence');
    expect(screen.queryByTestId('remediation-loop-ask-plan')).toBeNull();
  });

  it('investigation complete and the fix preparing: started HH:MM and Watch in chat, no button', () => {
    renderPanel(noPlan({ plan_status: 'preparing', preparing_since: Z('10:02') }), { investigation: { state: 'complete', stage: '' } });
    expect(headline()).toHaveTextContent('Preparing the fix from the investigation');
    expect(where()).toHaveTextContent('started');
    expect(screen.getByTestId('remediation-loop-watch-chat')).toBeInTheDocument();
    expect(screen.queryByTestId('remediation-loop-ask-plan')).toBeNull();
  });

  it('investigation complete, the last request ended without a fix: the reason and a secondary Try again', () => {
    apiKubernetes.askRemediationPlan.mockReturnValue(pending());
    renderPanel(noPlan({ last_request_mode: 'auto', last_request_at: Z('10:00'), plan_status_reason: 'conversation stayed busy 1h0m0s' }), {
      investigation: { state: 'complete', stage: '' },
    });
    expect(headline()).toHaveTextContent('No fix was prepared: conversation stayed busy 1h0m0s');
    fireEvent.click(screen.getByTestId('remediation-loop-ask-plan'));
    expect(apiKubernetes.askRemediationPlan).toHaveBeenCalledWith({ accountId: 'acc-1', eventId: 'ev-1', mode: 'plan' });
  });

  it('investigation complete and never asked: "No fix was prepared from the investigation yet" with Prepare the fix', () => {
    renderPanel(noPlan({}), { investigation: { state: 'complete', stage: '' } });
    expect(headline()).toHaveTextContent('No fix was prepared from the investigation yet');
    expect(screen.getByTestId('remediation-loop-ask-plan')).toHaveTextContent('Prepare the fix');
    expect(screen.queryByText(/No plan yet/)).toBeNull();
  });

  it('names the options by where they came from; the version is in Details; other fixes, not a replan', () => {
    renderPanel({ ...v6, version: 3, plan_version: 'abcdef0123456789', generated_at: Z('16:40'), revision: { kind: 'recheck' }, loop: ready });
    expect(headline()).toHaveTextContent(`Fix options · re-check at ${clockOnly(Z('16:40'))}`);
    expect(screen.queryByText(/Plan v3/)).toBeNull();
    fireEvent.click(screen.getByTestId('remediation-loop-plan-details-toggle'));
    expect(screen.getByTestId('remediation-loop-plan-details')).toHaveTextContent('plan version 3, hash abcdef012345');
    expect(screen.getByTestId('remediation-loop-replan-open')).toHaveTextContent('None of these · ask Nubi for other fixes');
    expect(screen.getByTestId('remediation-loop-ask-option-1')).toHaveTextContent('Use this fix');
  });

  it('after a note, the options say so', () => {
    renderPanel({ ...v6, generated_at: Z('16:52'), revision: { kind: 'replan', note: 'too risky' }, loop: ready });
    expect(headline()).toHaveTextContent(`Fix options · after your note at ${clockOnly(Z('16:52'))}`);
  });
});
