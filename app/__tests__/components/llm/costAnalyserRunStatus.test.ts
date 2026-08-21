import { toRunStatus } from '@components/llm/cost-analyser/adapt';

// The inputs below are the full `llm_conversation_status` Postgres enum, which
// conversations, messages and agents all carry. The Cost Analyser's Status
// column is this mapping's only consumer, so a wrong bucket here is invisible
// until someone compares it against the chat view.
describe('toRunStatus', () => {
  it('reports a running conversation as in progress, not awaiting approval', () => {
    // The reported bug: IN_PROGRESS matched the "awaiting" bucket, so a run that
    // was still executing showed up as blocked on a user.
    expect(toRunStatus('IN_PROGRESS')).toBe('in-progress');
    expect(toRunStatus('PENDING')).toBe('in-progress');
  });

  it('reserves awaiting-approval for the states actually blocked on a human or client', () => {
    expect(toRunStatus('WAITING')).toBe('awaiting-approval');
    expect(toRunStatus('WAITING_FOR_CLIENT_TOOL')).toBe('awaiting-approval');
  });

  it('treats a stopped run as cancelled rather than completed', () => {
    expect(toRunStatus('KILLED')).toBe('cancelled');
    expect(toRunStatus('TERMINATED')).toBe('cancelled');
    expect(toRunStatus('TERMINATING')).toBe('cancelled');
  });

  it('maps the terminal states', () => {
    expect(toRunStatus('COMPLETED')).toBe('completed');
    expect(toRunStatus('FAILED')).toBe('failed');
  });

  it('falls back to completed for an empty or unrecognised status', () => {
    expect(toRunStatus('')).toBe('completed');
    expect(toRunStatus(undefined)).toBe('completed');
  });
});
