import {
  REASON_TEXT,
  attemptsOf,
  chosenOption,
  durationText,
  loopNeedsPoll,
  loopOf,
  optionDescription,
  optionTitle,
  pendingFor,
  phrase,
  plainReason,
  refireLabel,
  resolutionSummary,
  runPhase,
  winningRun,
  isPlanStepRun,
  confirmedByName,
  answerText,
  timelineOf,
  prRequestText,
  PR_REPLY_ASK,
  optionMode,
  optionRepoOf,
  repoChangeOf,
  automationOf,
  automationHref,
  optionOfCommand,
} from '../remediationLoop';

// Fixtures shaped exactly like the contracts: the C7 read (plan fields top level + loop), C1 run rows, C3 pull
// request rows, the C4 verdict on a run row, and the C5 / C6 event labels.
const plan = {
  root_cause: 'Memory limit 100Mi is below shared_buffers 128MB',
  summary: '',
  plan_version: 'pv1',
  actions: [
    { action: 'Raise memory to 256Mi in the GitOps source', kind: 'fix', confidence: 95 },
    { action: 'Live patch the Deployment to 256Mi', kind: 'mitigation', trade_off: 'Argo CD puts 100Mi back', rollback_command: 'kubectl set ...' },
  ],
};
const loopReady = { plan_status: 'ready', relevance: { alert_state: 'firing', firings_since_plan: 0, runs_since_plan: 0, stale: false } };
const c1 = (over = {}) => ({
  id: 'run-1',
  type: 'CommandExecution',
  resolver_type: 'User',
  resolver_display_name: 'Asha',
  type_reference_id: 'kubectl set resources deploy/db --limits=memory=256Mi',
  status: 'Success',
  created_at: '2026-10-22T20:12:00Z',
  data: {
    source: 'chat',
    tool: 'kubectl',
    command: 'kubectl set resources deploy/db --limits=memory=256Mi',
    approval_key: 'kubectl:scope-1:7f9c1a2e-0000-4000-8000-000000000001',
    approved_by: 'asha@example.com',
    plan_version: 'pv1',
    option: 2,
    exit_code: 0,
    exit_code_reported: true,
    slot: 'execute',
    ...over,
  },
});

describe('loop read (C7)', () => {
  it('is null with the loop off: the response is the plan alone', () => {
    expect(loopOf(plan)).toBeNull();
    expect(loopOf(null)).toBeNull();
    expect(loopOf({ ...plan, loop: loopReady })).toBe(loopReady);
  });

  it('splits pending approvals by where they are answered', () => {
    const loop = {
      pending_approvals: [
        { message_id: 'm1', agent_id: 'a1', tool: 'kubectl', command: 'kubectl delete pod x', created_at: '2026-10-22T20:00:00Z', origin: 'web' },
        { message_id: 'm2', agent_id: 'a2', tool: 'gh', command: 'gh workflow run ci', created_at: '2026-10-22T20:01:00Z', origin: 'slack' },
      ],
    };
    const { web, slack } = pendingFor(loop);
    expect(web.map((p) => p.message_id)).toEqual(['m1']);
    expect(slack.map((p) => p.message_id)).toEqual(['m2']);
  });
});

describe('C1 run records', () => {
  it('finds the option under way on the plan shown, and when it is done', () => {
    expect(chosenOption(plan, [c1()], 'OPEN')).toMatchObject({ option: 2, done: false });
    expect(chosenOption(plan, [c1({ followup: { verdict: 'fixed', verdict_at: '2026-10-22T20:30:00Z' } })], 'OPEN').done).toBe(true);
    expect(chosenOption(plan, [c1()], 'RESOLVED').done).toBe(true);
  });

  it('ignores rollbacks, runs on another plan version and runs with no option', () => {
    expect(chosenOption(plan, [c1({ slot: 'rollback' })], 'OPEN')).toBeNull();
    expect(chosenOption(plan, [c1({ plan_version: 'pv0' })], 'OPEN')).toBeNull();
    expect(chosenOption(plan, [c1({ option: undefined })], 'OPEN')).toBeNull();
  });

  it('counts a pull request raised for an option (C3)', () => {
    const pr = {
      id: 'pr-1',
      type: 'PullRequest',
      status: 'InProgress',
      created_at: '2026-10-22T20:15:00Z',
      type_reference_id: 'https://example.com/pr/1',
      data: { plan_version: 'pv1', option: 1 },
    };
    expect(chosenOption(plan, [pr], 'OPEN')).toMatchObject({ option: 1 });
  });
});

describe('Raise a PR request', () => {
  it('asks for the PR link first, in the same words as the server', () => {
    const action = { action: 'x', required_change: { where: 'code', target: 't', change: 'c', repo: 'example-org/web' } };
    expect(prRequestText({ index: 1, res: { version: 2 }, action })).toBe(
      `Raise a PR for option 1 of plan v2: c in t (repo example-org/web). ${PR_REPLY_ASK}`
    );
  });
});

describe('how an option is carried out (llm-server pickMode and optionRepos)', () => {
  const change = (where, repo = '') => ({ action: 'x', required_change: { where, target: 't', change: 'c', repo } });
  it('a change always made in a repository is a PR; one made elsewhere only when the plan names a repository', () => {
    expect(repoChangeOf(change('code'))).toBe(true);
    expect(repoChangeOf(change('deployment_source'))).toBe(true);
    expect(repoChangeOf(change('infrastructure'))).toBe(false);
    expect(repoChangeOf(change('infrastructure', 'org/infra'))).toBe(true);
    expect(repoChangeOf(change('dependency', 'org/lib'))).toBe(true);
    expect(repoChangeOf(change('process', 'org/infra'))).toBe(false);
  });
  it('one click only for the workload’s own repository', () => {
    const loop = {
      option_repos: [
        { option: 1, repo: 'https://github.com/org/web', known: 'workload' },
        { option: 2, repo: 'org/infra', known: 'integration' },
      ],
    };
    expect(optionMode(change('code', 'org/web'), optionRepoOf(loop, 1))).toBe('pr');
    expect(optionMode(change('deployment_source', 'org/infra'), optionRepoOf(loop, 2))).toBe('pr_pick');
    expect(optionMode(change('code'), optionRepoOf(loop, 3))).toBe('pr_pick');
    expect(optionMode(change('process'), optionRepoOf(loop, 3))).toBe('manual');
    expect(optionMode({ action: 'x', execute_command: 'kubectl get pods' })).toBe('run');
    expect(optionRepoOf({}, 1)).toEqual({ repo: '', known: '' });
  });
});

describe('options, runs and attempts', () => {
  it('names an option in words: a slug title gives way to its description', () => {
    expect(optionTitle({ action: 'Roll back to revision 1' })).toBe('Roll back to revision 1');
    expect(optionTitle({ action: 'set-image', title: 'Update deployment image to nginx:1.27-alpine' })).toBe(
      'Update deployment image to nginx:1.27-alpine'
    );
    expect(optionDescription({ action: 'set-image', title: 'Update deployment image' })).toBe('');
    expect(optionTitle({ action: 'rollback-deployment' })).toBe('Rollback deployment');
    expect(phrase('Roll back a Deployment')).toBe('roll back a Deployment');
    expect(phrase('AWS scale-out')).toBe('AWS scale-out');
  });

  it('reads where a run is from its record and follow-up (C4)', () => {
    expect(runPhase(c1())).toMatchObject({ key: 'checking' });
    expect(runPhase({ ...c1(), status: 'InProgress' })).toMatchObject({ key: 'running' });
    expect(runPhase(c1({ followup: { recheck_at: '2026-10-22T20:20:00Z' } }))).toMatchObject({ key: 'verifying', until: '' });
    expect(runPhase(c1({ followup: { recheck_at: 'x', hold_until: '2026-10-22T20:23:00Z' } }))).toMatchObject({ until: '2026-10-22T20:23:00Z' });
    expect(runPhase(c1({ followup: { verdict: 'not_fixed', verdict_reason: 'fired_again_after_change' } }))).toMatchObject({
      key: 'not_fixed',
      reason: 'the alert fired again after the change',
    });
  });

  it('writes reasons as sentences, never as codes', () => {
    Object.keys(REASON_TEXT).forEach((code) => expect(plainReason(code)).not.toMatch(/_/));
    expect(plainReason('written more than 24 h ago')).toBe('written more than 24 h ago');
    expect(plainReason('weird_code')).toBe('');
  });

  it('numbers attempts from the first, newest first, with the plan as a dated line', () => {
    const rows = [
      c1({ followup: { verdict: 'not_fixed', verdict_reason: 'still_firing' } }),
      { ...c1({ followup: { verdict: 'fixed', verdict_reason: 'some_new_code' } }), id: 'run-2', created_at: '2026-10-22T20:40:00Z' },
      { id: 'w1', type: 'WorkflowExecution', status: 'Success', created_at: '2026-10-22T20:50:00Z' },
      { ...c1(), id: 'd1', status: 'Declined' },
    ];
    const entries = attemptsOf({ ...plan, version: 2, generated_at: '2026-10-22T20:30:00Z' }, rows);
    expect(entries.map((e) => (e.kind === 'plan' ? e.title : `${e.k}:${e.title}:${e.result?.text}`))).toEqual([
      '3:Automation run:Checking',
      '2:Live patch the Deployment to 256Mi:Likely fixed',
      'Fix options prepared',
      '1:Live patch the Deployment to 256Mi:Not fixed',
    ]);
    // An unknown reason code is never shown.
    expect(entries[1].reason).toBe('');
    expect(entries[3].reason).toBe('The alert is still firing after the change');
  });

  it('finds the winning run from nb_resolved_by, else the newest fixed run, and says durations plainly', () => {
    const won = { ...c1({ followup: { verdict: 'fixed' } }), id: 'run-9' };
    expect(winningRun([c1(), won], { nb_resolved_by: '{"run_id":"run-9"}' }).id).toBe('run-9');
    expect(winningRun([c1(), won], {}).id).toBe('run-9');
    expect(durationText('2026-10-22T20:00:00Z', '2026-10-22T20:12:00Z')).toBe('12 min');
    expect(durationText('2026-10-22T20:00:00Z', '2026-10-22T23:05:00Z')).toBe('3 h 5 min');
  });
});

describe('polling', () => {
  const soon = Date.parse('2026-10-22T20:20:00Z');
  it('polls only while something moves without the person, and never with the loop off', () => {
    expect(loopNeedsPoll({ loop: null, res: plan, rows: [], nbStatus: 'OPEN' })).toBe(false);
    expect(loopNeedsPoll({ loop: { plan_status: 'preparing' }, res: {}, rows: [], nbStatus: 'OPEN' })).toBe(true);
    expect(loopNeedsPoll({ loop: { ...loopReady, conversation_busy: true }, res: plan, rows: [], nbStatus: 'OPEN' })).toBe(true);
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [], nbStatus: 'OPEN' })).toBe(false);
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [c1()], nbStatus: 'OPEN', now: soon })).toBe(true);
  });

  it('a fixed attempt keeps polling until the event itself is resolved', () => {
    const fixed = c1({ data: { ...c1().data, followup: { verdict: 'fixed', verdict_at: '2026-10-22T20:20:00Z' } } });
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [fixed], nbStatus: 'OPEN', now: soon })).toBe(true);
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [fixed], nbStatus: 'RESOLVED', now: soon })).toBe(false);
  });

  it('an attempt whose check ended, failed or not, stops the polling: it waits on the person', () => {
    const failed = { ...c1({ followup: { verdict: 'not_fixed', done_at: '2026-10-22T20:30:00Z' } }), status: 'Failed' };
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [failed], nbStatus: 'OPEN', now: soon })).toBe(false);
    const unclear = c1({ followup: { verdict: 'unclear', done_at: '2026-10-22T20:30:00Z' } });
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [unclear], nbStatus: 'OPEN', now: soon })).toBe(false);
  });

  it('polls while the attempt runs or its check is under way, and not for a run the check never picked up', () => {
    const later = Date.parse('2026-10-22T23:00:00Z');
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [{ ...c1(), status: 'InProgress' }], nbStatus: 'OPEN', now: later })).toBe(true);
    const checking = c1({ followup: { claimed_at: '2026-10-22T20:13:00Z', hold_until: '2026-10-22T23:30:00Z' } });
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [checking], nbStatus: 'OPEN', now: later })).toBe(true);
    expect(loopNeedsPoll({ loop: loopReady, res: plan, rows: [{ ...c1(), status: 'Failed' }], nbStatus: 'OPEN', now: later })).toBe(false);
  });
});

describe('event labels (C5, C6)', () => {
  it('reads the re-fire label: "fix did not hold" only for the same cause', () => {
    expect(refireLabel({ nb_refire: JSON.stringify({ kind: 'fix_did_not_hold', previous_run_id: 'run-1' }) })).toMatchObject({
      text: 'Fix did not hold',
    });
    expect(refireLabel({ nb_refire: { kind: 'unknown', previous_run_id: 'run-1' } })).toMatchObject({ text: 'Fired again' });
    expect(refireLabel({})).toBeNull();
  });

  it('summarises a resolve by the loop from nb_resolved_by and its run', () => {
    const run = c1({ followup: { verdict: 'fixed', verdict_at: '2026-10-22T20:30:00Z', verdict_reason: '0 OOMKilled for 5 min' } });
    const labels = {
      nb_resolved_by: JSON.stringify({
        run_id: 'run-1',
        verdict: 'fixed',
        cause_key: { what: 'oom', how: 'limit below buffers', against: 'demo/db' },
      }),
    };
    expect(resolutionSummary({ labels, nbStatus: 'RESOLVED', res: plan, rows: [run] })).toEqual({
      clearedBeforeChange: false,
      resolvedAt: '2026-10-22T20:30:00Z',
      cause: plan.root_cause,
      fix: 'kubectl set resources deploy/db --limits=memory=256Mi',
      fixedBy: 'Asha',
      proof: '0 OOMKilled for 5 min',
    });
    expect(resolutionSummary({ labels, nbStatus: 'RESOLVED', res: null, rows: [] }).cause).toBe('oom · limit below buffers · demo/db');
    expect(
      resolutionSummary({ labels: { nb_resolved_by: { run_id: 'run-1', verdict: 'cleared_before_change' } }, nbStatus: 'RESOLVED', rows: [run] })
        .clearedBeforeChange
    ).toBe(true);
  });

  it('reads the labels as the JSON strings T3 writes, and shows nothing for a malformed value', () => {
    const labels = {
      nb_resolved_by: '{"run_id":"run-1","verdict":"cleared_before_change","cause_key":null}',
      nb_refire: '{"kind":"fix_did_not_hold","previous_run_id":"run-1"}',
    };
    expect(resolutionSummary({ labels, nbStatus: 'RESOLVED', res: null, rows: [c1()] })).toMatchObject({ clearedBeforeChange: true, cause: '' });
    expect(refireLabel(labels)).toMatchObject({ text: 'Fix did not hold', previousRunId: 'run-1' });
    const broken = { nb_resolved_by: '{"run_id":"run-1",', nb_refire: 'fix_did_not_hold' };
    expect(resolutionSummary({ labels: broken, nbStatus: 'RESOLVED', rows: [c1()] })).toBeNull();
    expect(refireLabel(broken)).toBeNull();
    expect(refireLabel({ nb_refire: '"just a string"' })).toBeNull();
  });

  it('says nothing unless the event is resolved by the loop', () => {
    expect(resolutionSummary({ labels: {}, nbStatus: 'RESOLVED', rows: [] })).toBeNull();
    expect(resolutionSummary({ labels: { nb_resolved_by: { run_id: 'r' } }, nbStatus: 'OPEN', rows: [] })).toBeNull();
  });
});

describe('isPlanStepRun and confirmedByName', () => {
  it('a chat write with no plan option or step is not a plan step; every other run is', () => {
    expect(isPlanStepRun({ type: 'CommandExecution', data: { source: 'chat', command: 'kubectl delete pod x' } })).toBe(false);
    expect(isPlanStepRun({ type: 'CommandExecution', data: { source: 'chat', option: 1 } })).toBe(true);
    expect(isPlanStepRun({ type: 'CommandExecution', data: { source: 'chat', step: '0:apply' } })).toBe(true);
    expect(isPlanStepRun({ type: 'CommandExecution', data: { source: 'web' } })).toBe(true);
    expect(isPlanStepRun({ type: 'PullRequest', data: {} })).toBe(true);
  });
  it('names the person who confirmed the resolve', () => {
    expect(confirmedByName({ nb_resolved_by: JSON.stringify({ run_id: 'r', confirmed_by_name: 'Asha' }) })).toBe('Asha');
    expect(confirmedByName({ nb_resolved_by: JSON.stringify({ run_id: 'r' }) })).toBe('');
    expect(confirmedByName({})).toBe('');
  });
});

describe('answerText', () => {
  it('names Slack and quotes the note; the tab and chat add nothing', () => {
    expect(answerText('slack', ' after the freeze ')).toBe(' in Slack: “after the freeze”');
    expect(answerText('web', 'ok')).toBe(': “ok”');
    expect(answerText('slack', '')).toBe(' in Slack');
    expect(answerText(undefined, undefined)).toBe('');
  });
});

describe('timelineOf: an approved run says what was recorded with its approval', () => {
  const res = { plan_version: 'pv1', actions: [{ action: 'Restart the API', execute_command: 'kubectl rollout restart deploy/api -n shop' }] };
  const row = (data) => ({
    id: 'r1',
    type: 'CommandExecution',
    resolver_type: 'User',
    resolver_display_name: 'Asha',
    status: 'Success',
    created_at: '2026-10-24T10:00:00Z',
    data: { command: 'kubectl rollout restart deploy/api -n shop', option: 1, plan_version: 'pv1', step: '0:apply', ...data },
  });
  const approvedText = (data) =>
    timelineOf({ res, rows: [row(data)], loop: {} })
      .flatMap((it) => it.steps || [])
      .find((st) => st.key === 'approved')?.text;
  it('the note, and Slack when it was approved there', () => {
    expect(approvedText({ approval_note: 'safe now', approved_from: 'slack' })).toBe('Approved by Asha in Slack: “safe now”');
    expect(approvedText({ approval_note: 'safe now', approved_from: 'web' })).toBe('Approved by Asha: “safe now”');
    expect(approvedText({})).toBe('Approved by Asha');
  });
  it('a change a person made carries no approval note', () => {
    expect(approvedText({ manual: true, approval_note: 'x' })).toBe('Made by Asha');
  });
});

describe("an option that runs the account's own automation (#42459)", () => {
  const action = {
    action: 'Restart nginx',
    kind: 'mitigation',
    automation: { id: 'wf-restart', name: 'Restart VM service', version: 'v-7', inputs: { service: 'nginx', host: 'vm-1', retries: 2 } },
  };
  it('is run by the assistant after an approval, and names the automation and its inputs', () => {
    expect(optionMode(action)).toBe('run');
    expect(automationOf(action)).toEqual({
      id: 'wf-restart',
      name: 'Restart VM service',
      inputs: [
        ['host', 'vm-1'],
        ['retries', '2'],
        ['service', 'nginx'],
      ],
    });
    expect(automationOf({ action: 'x', execute_command: 'kubectl get pods' })).toBeNull();
    expect(automationHref('acct 1', 'wf-restart')).toBe('/automation/wf-restart?accountId=acct%201');
    // An approval's command names its option: the command, or automation:<id>.
    const res = { actions: [{ action: 'Scale', execute_command: 'kubectl scale deploy/api --replicas=3' }, action] };
    expect(optionOfCommand(res, 'automation:wf-restart')).toBe(2);
    expect(optionOfCommand(res, 'kubectl scale deploy/api --replicas=3')).toBe(1);
    expect(optionOfCommand(res, 'automation:wf-other')).toBe(0);
  });
  it("shows the run's state on the timeline with a link to the run", () => {
    const row = (status, data = {}) => ({
      id: 'r1',
      type: 'CommandExecution',
      status,
      status_message: status === 'Failed' ? 'Automation "Restart VM service" ended failed, stopped at step 2 of 3.' : '',
      created_at: '2026-10-27T10:00:00Z',
      updated_at: '2026-10-27T10:20:00Z',
      data: {
        command: 'automation:wf-restart',
        kind: 'automation',
        name: 'Restart VM service',
        url: 'https://example.com/run',
        option: 1,
        step: '0:apply',
        ...data,
      },
    });
    const ran = (status) =>
      timelineOf({ res: { plan_version: 'pv1', actions: [action] }, rows: [row(status)], loop: {} })
        .flatMap((it) => it.steps || [])
        .find((st) => st.key === 'ran');
    expect(ran('InProgress')).toMatchObject({ text: 'Running: automation Restart VM service', href: 'https://example.com/run', live: true });
    expect(ran('Success')).toMatchObject({ text: 'Completed: automation Restart VM service', detail: '' });
    expect(ran('Failed')).toMatchObject({ tone: 'critical', detail: 'Automation "Restart VM service" ended failed, stopped at step 2 of 3.' });
  });
});
