import { awaitingFollowup, describeResolution, followupPoll, latestFollowup, readableStatusMessage, verifiedSuccess } from '../resolutionStatus';

// A failed agent task was stored as the agent's whole response; the reason is the `msg` inside it.
describe('readableStatusMessage', () => {
  it('reads the reason out of a stored agent response', () => {
    expect(
      readableStatusMessage(
        '{"msg": "rightsizing_resource: StatefulSet rightsizing is not supported", "success": false, "task_processing_duration": 0}'
      )
    ).toBe('rightsizing_resource: StatefulSet rightsizing is not supported');
  });

  it('leaves plain text, JSON without a reason, and empty values unchanged', () => {
    expect(readableStatusMessage('Ran from the remediation panel, exit code 1')).toBe('Ran from the remediation panel, exit code 1');
    expect(readableStatusMessage('{"success": false}')).toBe('{"success": false}');
    expect(readableStatusMessage('{"msg": {"code": 1}}')).toBe('{"msg": {"code": 1}}');
    expect(readableStatusMessage('')).toBe('');
    expect(readableStatusMessage(undefined)).toBeUndefined();
  });
});

// A remediation run is "Done" only when the agent reported exit 0. A run whose result never came back,
// or came back without an exit code, is said as such and never counted as a fix that worked.
describe('remediation run outcomes', () => {
  const run = (status, data = {}) => ({ type: 'CommandExecution', status, data, resolver_type: 'User' });

  it.each([
    [run('Success', { exit_code_reported: true }), true],
    [run('Success', JSON.stringify({ exit_code_reported: true })), true],
    [run('Success'), false],
    [run('Success', { exit_code_reported: true, manual: true }), false],
    [run('Unknown', { exit_code_reported: true }), false],
  ])('verifiedSuccess(%j) is %s', (r, want) => expect(verifiedSuccess(r)).toBe(want));

  it.each([
    [run('Success', { exit_code_reported: true }), { label: 'Done', tone: 'success' }],
    [run('Success', { exit_code_reported: false }), { label: 'Outcome unverified', tone: 'warning' }],
    [run('Success', { manual: true }), { label: 'Done', detail: 'reported done, not verified' }],
    [run('Unknown'), { label: 'Outcome unknown', tone: 'warning', isSlow: false }],
    [run('InProgress'), { label: 'Running' }],
    [run('Declined'), { label: 'Declined', tone: 'neutral', isSlow: false }],
    // Other resolution types are left as they were.
    [{ type: 'DeploymentChange', status: 'Success', data: {} }, { label: 'Done' }],
  ])('describeResolution(%j)', (r, want) => expect(describeResolution(r)).toMatchObject(want));
});

// NudgeBee's own check after a run (#40734), and when the page polls for it: only while one is due or
// checking, not for minutes after any run.
describe('follow-up checks', () => {
  const now = Date.parse('2026-10-20T10:00:00Z');
  const run = (over = {}) => ({ id: 'r', type: 'CommandExecution', status: 'Success', created_at: '2026-10-20T09:59:00Z', data: {}, ...over });
  const checked = (followup) => [run({ data: { followup } })];

  it('takes the newest run that has a follow-up', () => {
    const rows = [
      run({ id: 'r1', created_at: '2026-10-14T10:00:00', data: { followup: { done_at: 'x' } } }),
      run({ id: 'r2', created_at: '2026-10-14T11:00:00', data: { followup: { session: 's2' } } }),
      run({ id: 'r3', created_at: '2026-10-14T12:00:00' }),
      { id: 'p1', type: 'PullRequest', created_at: '2026-10-14T13:00:00', data: { followup: {} } },
    ];
    expect(latestFollowup(rows)).toMatchObject({ runId: 'r2', session: 's2' });
    expect(latestFollowup([])).toBeNull();
  });

  it.each([
    ['a run settled a minute ago with no check', [run()], true],
    ['a check already started', checked({ session: 's' }), false],
    ['the run still in flight', [run({ status: 'InProgress' })], false],
    ['a declined step', [run({ status: 'Declined' })], false],
    ['a run that never ran', [run({ data: { not_sent: true } })], false],
    ['a run settled before the wait', [run({ created_at: '2026-10-20T09:50:00Z' })], false],
    ['no runs', [], false],
  ])('awaitingFollowup: %s', (_, rows, want) => expect(awaitingFollowup(rows, now)).toBe(want));

  it.each([
    ['no check yet, while one is awaited', [run()], { live: true, wakeAt: null }],
    ['a check running', checked({ claimed_at: '2026-10-20T09:59:30Z' }), { live: true, wakeAt: null }],
    [
      'a re-check due later',
      checked({ claimed_at: 'x', recheck_at: '2026-10-20T10:20:00Z' }),
      { live: false, wakeAt: Date.parse('2026-10-20T10:20:00Z') },
    ],
    ['a finished check', checked({ done_at: 'x' }), { live: false, wakeAt: null }],
    ['a check claimed past the server give-up', checked({ claimed_at: '2026-10-20T07:00:00Z' }), { live: false, wakeAt: null }],
  ])('followupPoll: %s', (_, rows, want) => expect(followupPoll(rows, now)).toEqual(want));
});
