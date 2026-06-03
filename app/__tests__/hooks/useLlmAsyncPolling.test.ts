import { renderHook, act } from '@testing-library/react';
import { useLlmAsyncPolling, extractQueryResultFromConversation } from '@hooks/useLlmAsyncPolling';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    getLlmConversation: jest.fn(),
  },
}));

import apiAskNudgebee from '@api1/ask-nudgebee';
const mockGetConversation = apiAskNudgebee.getLlmConversation as jest.Mock;

describe('extractQueryResultFromConversation', () => {
  it('returns null when conversation has no messages', () => {
    expect(extractQueryResultFromConversation({})).toBeNull();
    expect(extractQueryResultFromConversation({ llm_conversation_messages: [] })).toBeNull();
  });

  it('returns agent response when found', () => {
    const conversation = {
      id: 'conv-1',
      llm_conversation_messages: [{ llm_conversation_agents: [{ response: 'Agent answer' }] }],
    };
    const result = extractQueryResultFromConversation(conversation);
    expect(result?.response).toBe('Agent answer');
    expect(result?.conversationId).toBe('conv-1');
  });

  it('falls back to last message response when no agent response', () => {
    const conversation = {
      id: 'conv-2',
      llm_conversation_messages: [{ llm_conversation_agents: [], response: 'Direct response' }],
    };
    const result = extractQueryResultFromConversation(conversation);
    expect(result?.response).toBe('Direct response');
  });
});

describe('useLlmAsyncPolling', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('initialises with isPolling=false', () => {
    const { result } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1' }));
    expect(result.current.isPolling).toBe(false);
  });

  it('startPolling sets isPolling=true', async () => {
    mockGetConversation.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1' }));
    await act(async () => {
      result.current.startPolling('session-1', jest.fn());
    });
    expect(result.current.isPolling).toBe(true);
  });

  it('stopPolling sets isPolling=false', async () => {
    mockGetConversation.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1' }));
    act(() => result.current.startPolling('session-1', jest.fn()));
    act(() => result.current.stopPolling());
    expect(result.current.isPolling).toBe(false);
  });

  it('calls onComplete callback when terminal status COMPLETED is received', async () => {
    const onComplete = jest.fn();
    mockGetConversation.mockResolvedValue({
      data: { data: { llm_conversations: [{ status: 'COMPLETED', id: 'conv-1' }] } },
    });
    const { result } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1' }));

    await act(async () => {
      result.current.startPolling('session-1', onComplete);
      await Promise.resolve();
    });

    expect(onComplete).toHaveBeenCalledWith({ status: 'COMPLETED', id: 'conv-1' });
    expect(result.current.isPolling).toBe(false);
  });

  it('calls onComplete with FAILED status when terminal FAILED received', async () => {
    const onComplete = jest.fn();
    mockGetConversation.mockResolvedValue({
      data: { data: { llm_conversations: [{ status: 'FAILED' }] } },
    });
    const { result } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1' }));

    await act(async () => {
      result.current.startPolling('session-1', onComplete);
      await Promise.resolve();
    });

    expect(onComplete).toHaveBeenCalledWith({ status: 'FAILED' });
  });

  it('continues polling when status is not terminal', async () => {
    mockGetConversation.mockResolvedValue({
      data: { data: { llm_conversations: [{ status: 'IN_PROGRESS' }] } },
    });
    const { result } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1', pollInterval: 1000 }));

    await act(async () => {
      result.current.startPolling('session-1', jest.fn());
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.isPolling).toBe(true);
  });

  it('stops polling and calls onComplete with FAILED on API error', async () => {
    const onComplete = jest.fn();
    mockGetConversation.mockRejectedValue(new Error('Network error'));
    const { result } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1' }));

    await act(async () => {
      result.current.startPolling('session-1', onComplete);
      await Promise.resolve();
    });

    expect(result.current.isPolling).toBe(false);
    expect(onComplete).toHaveBeenCalledWith({ status: 'FAILED' });
  });

  it('cleans up interval on unmount', () => {
    const clearIntervalSpy = jest.spyOn(global, 'clearInterval');
    mockGetConversation.mockReturnValue(new Promise(() => {}));
    const { result, unmount } = renderHook(() => useLlmAsyncPolling({ accountId: 'acc-1' }));
    act(() => result.current.startPolling('session-1', jest.fn()));
    unmount();
    expect(clearIntervalSpy).toHaveBeenCalled();
    clearIntervalSpy.mockRestore();
  });
});
