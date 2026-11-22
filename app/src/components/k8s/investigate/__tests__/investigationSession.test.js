import { alertSessionOf, investigationIsAsking, investigationRunOf, investigationWaitingReason } from '../investigationSession';

describe('the alert conversation', () => {
  it('is event-<fingerprint>, the one session for the investigation and the remediation', () => {
    expect(alertSessionOf('fp1')).toBe('event-fp1');
    expect(alertSessionOf(' fp1 ')).toBe('event-fp1');
    expect(alertSessionOf('')).toBe('');
    expect(alertSessionOf(undefined)).toBe('');
  });
});

describe('investigationIsAsking', () => {
  const stage = (status) => ({ message_type: 'generation', status, message_config: JSON.stringify({ investigation_run_id: 'run-1' }) });
  const remediation = (status) => ({ message_type: 'generation', status, message_config: JSON.stringify({ read_only_run: false }) });
  const approval = { message_type: 'followup', status: 'WAITING', message_config: JSON.stringify({ followupType: 'tool_confirmation' }) };

  it('reads the run a turn is tagged with', () => {
    expect(investigationRunOf(JSON.stringify({ investigation_run_id: 'r' }))).toBe('r');
    expect(investigationRunOf({ investigation_run_id: 'r' })).toBe('r');
    expect(investigationRunOf('{not json')).toBe('');
    expect(investigationRunOf(null)).toBe('');
  });

  it('is the investigation asking when its own stage waits', () => {
    expect(investigationIsAsking([stage('COMPLETED'), stage('WAITING'), approval])).toBe(true);
  });

  it('is not the investigation when a remediation approval waits in the same conversation', () => {
    expect(investigationIsAsking([stage('COMPLETED'), remediation('WAITING'), approval])).toBe(false);
  });

  it('keeps the old behaviour with the loop off (no turn is tagged)', () => {
    expect(investigationIsAsking([remediation('COMPLETED'), remediation('WAITING'), approval])).toBe(true);
    expect(investigationIsAsking([])).toBe(true);
  });
});

describe('investigationWaitingReason', () => {
  it('says why a re-investigation waits', () => {
    expect(investigationWaitingReason({ status: 'IN_PROGRESS', status_reason: 'waiting on an approval' })).toMatch(/approval is waiting/);
    expect(investigationWaitingReason({ status: 'in_progress', status_reason: "waiting for the current turn in the alert's conversation" })).toMatch(
      /Another turn/
    );
  });

  it('is empty when it is not waiting on the conversation', () => {
    expect(investigationWaitingReason({ status: 'IN_PROGRESS', status_reason: 'code analysis paused awaiting approval' })).toBe('');
    expect(investigationWaitingReason({ status: 'FAILED', status_reason: 'waiting on an approval' })).toBe('');
    expect(investigationWaitingReason(null)).toBe('');
  });
});
