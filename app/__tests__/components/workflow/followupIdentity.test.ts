import { followupIdentity } from '@components/workflow/components/AiGenerateWorkflowModal';

/**
 * The Generate-with-AI modal stops polling as soon as it displays a question (clearPolling), so
 * the identity below is the only thing standing between "the backend recorded my answer" and a
 * permanently stranded modal. #35393 items 1 and 2 are both this bug:
 *
 *   - the user clicks an answer, the modal re-renders the SAME question and stops polling, so the
 *     click reads as having done nothing (reported as "clicking the row doesn't submit" — it does
 *     submit; the UI just never acknowledges it), and
 *   - the user clicks again, submitting the identical answer a second time (the duplicate followup
 *     rows in #33190).
 */
describe('followupIdentity', () => {
  const question = {
    messageId: 'msg-1',
    planText: 'Which ticketing system should be used?',
    followupData: { type: 'clarification', options: ['Jira-test', 'ServiceNow-test'] },
  };

  it('is unchanged when only the followup row was touched by recording the answer', () => {
    // Same row, same question — the backend has bumped updated_at to store the user's answer but
    // has not asked anything new. This MUST compare equal or the modal strands itself.
    expect(followupIdentity({ ...question })).toBe(followupIdentity({ ...question }));
  });

  it('changes when the backend asks a different question on the same row', () => {
    // What the updated_at key was originally added for: config approval reuses one followup row
    // with different content. Content keying covers it without the false positive.
    const configApproval = { ...question, planText: 'Approve creating config slack_channel?' };
    expect(followupIdentity(configApproval)).not.toBe(followupIdentity(question));
  });

  it('changes when only the options change', () => {
    // Same prompt text, different choices — still a different question to answer.
    const differentOptions = { ...question, followupData: { type: 'clarification', options: ['Jira-test'] } };
    expect(followupIdentity(differentOptions)).not.toBe(followupIdentity(question));
  });

  it('changes when the backend moves to a new followup row', () => {
    expect(followupIdentity({ ...question, messageId: 'msg-2' })).not.toBe(followupIdentity(question));
  });

  it('is stable across calls so a repeated poll of one question never renders twice', () => {
    const first = followupIdentity(question);
    for (let i = 0; i < 10; i += 1) {
      expect(followupIdentity(question)).toBe(first);
    }
  });

  it('handles missing fields without collapsing distinct questions together', () => {
    expect(followupIdentity({})).toBe(followupIdentity({}));
    expect(followupIdentity({ planText: 'a' })).not.toBe(followupIdentity({ planText: 'b' }));
    // An absent followupData must not read the same as one that is explicitly null-ish but present
    // with content.
    expect(followupIdentity({ messageId: 'm' })).not.toBe(followupIdentity({ messageId: 'm', followupData: { a: 1 } }));
  });
});
