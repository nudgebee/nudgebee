import { describePendingApproval } from '../approvalOptions';

describe('describePendingApproval', () => {
  it('uses the rendered message and names no other channel for in_app', () => {
    expect(describePendingApproval({ message: ' Approve restart of pod web-1? ', approval_type: 'in_app' }, undefined)).toEqual({
      message: 'Approve restart of pod web-1?',
      respondHint: 'Respond below',
    });
  });

  it('treats a missing approval_type as in_app', () => {
    expect(describePendingApproval({ message: 'Go?' }, undefined).respondHint).toBe('Respond below');
  });

  it('names the IM provider the request went to', () => {
    expect(describePendingApproval({ approval_type: 'instant_message', im_provider: 'slack' }, undefined).respondHint).toBe(
      'Respond below or in Slack'
    );
    expect(describePendingApproval({ approval_type: 'instant_message', im_provider: 'ms_teams' }, undefined).respondHint).toBe(
      'Respond below or in Microsoft Teams'
    );
  });

  it('points email approvals at the email', () => {
    expect(describePendingApproval({ approval_type: 'email' }, undefined).respondHint).toBe('Respond below or from the approval email');
  });

  it('falls back to the configured params when the run input is not known yet', () => {
    expect(describePendingApproval(undefined, { message: 'Scale web?', approval_type: 'email' })).toEqual({
      message: 'Scale web?',
      respondHint: 'Respond below or from the approval email',
    });
  });

  it('falls back per field when the rendered input has only a message', () => {
    expect(describePendingApproval({ message: 'Go?' }, { approval_type: 'instant_message', im_provider: 'slack' })).toEqual({
      message: 'Go?',
      respondHint: 'Respond below or in Slack',
    });
  });
});
