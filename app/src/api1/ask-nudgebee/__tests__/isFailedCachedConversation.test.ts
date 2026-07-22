import { isFailedCachedConversation } from '../index';

const conv = (agents: Array<{ status: string }>) => [{ llm_conversation_messages: [{ llm_conversation_agents: agents }] }];

describe('isFailedCachedConversation', () => {
  it('is true when the last turn produced only failed agents (fail/error variants)', () => {
    expect(isFailedCachedConversation(conv([{ status: 'fail' }]))).toBe(true);
    expect(isFailedCachedConversation(conv([{ status: 'FAIL' }]))).toBe(true);
    expect(isFailedCachedConversation(conv([{ status: 'failed' }]))).toBe(true);
    expect(isFailedCachedConversation(conv([{ status: 'error' }]))).toBe(true);
  });

  it('is false when any agent on the last turn succeeded (keep caching success)', () => {
    expect(isFailedCachedConversation(conv([{ status: 'success' }]))).toBe(false);
    expect(isFailedCachedConversation(conv([{ status: 'fail' }, { status: 'success' }]))).toBe(false);
  });

  it('is false for in-progress, agentless, empty, or missing data', () => {
    expect(isFailedCachedConversation(conv([{ status: 'running' }]))).toBe(false);
    expect(isFailedCachedConversation(conv([]))).toBe(false);
    expect(isFailedCachedConversation([])).toBe(false);
    expect(isFailedCachedConversation(undefined as any)).toBe(false);
    expect(isFailedCachedConversation(conv([{ status: true as any }]))).toBe(false);
    expect(isFailedCachedConversation(conv([{ status: null as any }]))).toBe(false);
  });

  it('only inspects the latest turn, not earlier failed ones', () => {
    const multi = [
      {
        llm_conversation_messages: [{ llm_conversation_agents: [{ status: 'fail' }] }, { llm_conversation_agents: [{ status: 'success' }] }],
      },
    ];
    expect(isFailedCachedConversation(multi)).toBe(false);
  });
});
