import { buildAskNudgebeeHref, getLlmAccountId, getLlmSessionId } from '../llmChat';

describe('llmChat', () => {
  describe('getLlmSessionId', () => {
    it('reads the session from an object or a JSON-string output', () => {
      expect(getLlmSessionId({ type: 'llm.nubi', output: { session_id: 's1' } })).toBe('s1');
      expect(getLlmSessionId({ type: 'llm.investigate', output: JSON.stringify({ session_id: 's2' }) })).toBe('s2');
    });

    it('returns null for other tasks and unusable output', () => {
      expect(getLlmSessionId({ type: 'k8s.cli', output: { session_id: 's1' } })).toBeNull();
      expect(getLlmSessionId({ type: 'llm.nubi', output: 'not json' })).toBeNull();
      expect(getLlmSessionId({ type: 'llm.nubi', output: {} })).toBeNull();
      expect(getLlmSessionId(null)).toBeNull();
    });
  });

  describe('getLlmAccountId', () => {
    it('uses the account the task reports, since a step can target another account', () => {
      expect(getLlmAccountId({ type: 'llm.nubi', output: { session_id: 's1', account_id: 'acc-b' } }, 'acc-a')).toBe('acc-b');
      expect(getLlmAccountId({ type: 'llm.investigate', output: JSON.stringify({ account_id: 'acc-b' }) }, 'acc-a')).toBe('acc-b');
    });

    it("falls back to the workflow's account when the task reports none", () => {
      expect(getLlmAccountId({ type: 'llm.summary', output: { session_id: 's1' } }, 'acc-a')).toBe('acc-a');
      expect(getLlmAccountId({ type: 'llm.nubi', output: { account_id: '' } }, 'acc-a')).toBe('acc-a');
      expect(getLlmAccountId({ type: 'llm.nubi', output: 'not json' }, 'acc-a')).toBe('acc-a');
      expect(getLlmAccountId(undefined, 'acc-a')).toBe('acc-a');
    });
  });

  it('links to the conversation in the account it ran against', () => {
    const task = { type: 'llm.nubi', output: { session_id: 'wf__1__2', account_id: 'acc-b' } };
    expect(buildAskNudgebeeHref(getLlmAccountId(task, 'acc-a'), getLlmSessionId(task) as string)).toBe(
      '/ask-nudgebee?accountId=acc-b&session_id=wf__1__2'
    );
  });
});
