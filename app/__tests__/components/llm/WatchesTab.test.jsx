import { render, screen, waitFor } from '@testing-library/react';
import api from '@api1/ask-nudgebee';
import WatchesTab, { describeWatchTarget, describeWatchOutcome, buildHoverDetails } from '@components/llm/WatchesTab';

// #38901: the panel described the poller, not the task — UUID headline,
// "1 polls", red "1 failed" on a healthy retry.

jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { listWatchesByConversation: jest.fn(), cancelWatch: jest.fn() } }));
jest.mock('@hooks/useTenantBranding', () => ({
  useWatchFeatureEnabled: () => true,
  useBrandingConfig: () => ({ isWhiteLabel: false, loading: false }),
}));
jest.mock('@shared/snackbarService', () => ({ snackbar: jest.fn() }));

describe('describeWatchTarget', () => {
  it('shows the command the backend says this polls', () => {
    expect(describeWatchTarget({ watch_target: 'gh run view 35580493474 --repo nudgebee/nudgebee-infra' })).toBe(
      'gh run view 35580493474 --repo nudgebee/nudgebee…'
    );
    expect(describeWatchTarget({ watch_target: 'kubectl rollout status deploy/shipping -n demo' })).toBe(
      'kubectl rollout status deploy/shipping -n demo'
    );
  });

  it('truncates rather than overflowing the row', () => {
    const out = describeWatchTarget({ watch_target: 'x'.repeat(200) });
    expect(out).toHaveLength(49);
    expect(out.endsWith('…')).toBe(true);
  });

  // watch_target is empty for a sql source, and absent on an older backend.
  it('falls back to the condition, then to a noun', () => {
    expect(describeWatchTarget({ watch_target: '', predicate_expr: 'successfully rolled out' })).toBe('for “successfully rolled out”');
    expect(describeWatchTarget({ source_kind: 'sql', watch_target: '' })).toBe('a database query');
    expect(describeWatchTarget({ source_kind: 'tool' })).toBe('a command');
    expect(describeWatchTarget(undefined)).toBe('a command');
  });

  it('ignores a whitespace-only target', () => {
    expect(describeWatchTarget({ watch_target: '   ', predicate_expr: 'done' })).toBe('for “done”');
  });
});

describe('describeWatchOutcome for a timer', () => {
  it('reads reaching the time as the end, not giving up', () => {
    expect(describeWatchOutcome({ status: 'EXPIRED', source_kind: 'timer' })).toBe('waited until the set time');
  });
});

describe('describeWatchOutcome', () => {
  it('says what a completed watch matched', () => {
    expect(describeWatchOutcome({ status: 'COMPLETED', predicate_expr: 'completed' })).toBe('matched “completed”');
  });

  it('distinguishes a timeout from a broken check', () => {
    expect(describeWatchOutcome({ status: 'EXPIRED' })).toBe('gave up before the condition was met');
    expect(describeWatchOutcome({ status: 'FAILED' })).toBe('the check could not run');
  });

  // The pill already reads "Cancelled"; returning a word here rendered
  // "Cancelled · cancelled · 3m ago".
  it('stays silent when the pill already says it', () => {
    expect(describeWatchOutcome({ status: 'CANCELLED' })).toBe('');
    expect(describeWatchOutcome({ status: 'PENDING' })).toBe('');
    expect(describeWatchOutcome({ status: 'ACTIVE' })).toBe('');
  });
});

describe('buildHoverDetails', () => {
  it('pluralizes a single check — the bug that started this', () => {
    const out = buildHoverDetails({ id: 'abc', poll_count: 1, poll_interval_sec: 30, failure_count: 0 });
    expect(out).toContain('1 check so far, every 30s');
    expect(out).not.toContain('1 checks');
  });

  it('labels transient failures as retried rather than broken', () => {
    expect(buildHoverDetails({ id: 'abc', poll_count: 3, poll_interval_sec: 30, failure_count: 1 })).toContain('1 transient failure (retried)');
  });
});

// #42461: NudgeBee's own wait (a remediation follow-up's) is listed, but only the follow-up ends it — no Cancel.
describe('WatchesTab cancel', () => {
  it("offers Cancel on a person's live watch and not on NudgeBee's own", async () => {
    const live = {
      status: 'ACTIVE',
      source_kind: 'tool',
      predicate_kind: 'regex',
      predicate_expr: '3/3',
      predicate_negate: false,
      poll_interval_sec: 60,
      max_duration_sec: 600,
      poll_count: 1,
      failure_count: 0,
      next_poll_at: '2026-10-27T10:00:00Z',
      expires_at: '2026-10-27T10:10:00Z',
      created_at: '2026-10-27T10:00:00Z',
      updated_at: '2026-10-27T10:00:00Z',
    };
    api.listWatchesByConversation.mockResolvedValue([
      { ...live, id: 'person-watch', watch_target: 'kubectl get deploy api', origin: '' },
      { ...live, id: 'nudgebee-watch', watch_target: 'kubectl rollout status deploy/api', origin: 'remediation' },
    ]);
    render(<WatchesTab conversationId='conversation-1' />);
    await waitFor(() => expect(screen.getByTestId('watch-cancel-btn-person-watch')).toBeInTheDocument());
    expect(screen.queryByTestId('watch-cancel-btn-nudgebee-watch')).not.toBeInTheDocument();
  });
});

describe('WatchesTab ended timer', () => {
  it("shows the timer's outcome without the stock expired-before-condition result", async () => {
    api.listWatchesByConversation.mockResolvedValue([
      {
        id: 'nudgebee-timer',
        status: 'EXPIRED',
        source_kind: 'timer',
        watch_target: 'waiting until 10:26 UTC: confirm the alert stays clear',
        predicate_kind: 'never',
        poll_interval_sec: 900,
        max_duration_sec: 900,
        poll_count: 1,
        failure_count: 0,
        final_result: 'watch expired before condition was met',
        origin: 'remediation',
        created_at: '2026-10-27T10:00:00Z',
        updated_at: '2026-10-27T10:15:00Z',
      },
    ]);
    render(<WatchesTab conversationId='conversation-1' />);
    await waitFor(() => expect(screen.getByText(/waited until the set time/)).toBeInTheDocument());
    expect(screen.queryByText('watch expired before condition was met')).not.toBeInTheDocument();
  });
});
