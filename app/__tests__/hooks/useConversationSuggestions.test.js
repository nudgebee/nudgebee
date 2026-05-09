import { renderHook, act } from '@testing-library/react';
import { useConversationSuggestions } from '@hooks/useConversationSuggestions';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    getConversationSuggestions: jest.fn(),
  },
}));

import apiAskNudgebee from '@api1/ask-nudgebee';
const mockGetSuggestions = apiAskNudgebee.getConversationSuggestions;

const mockSuggestions = ['How do I fix this?', 'Show me the logs', 'What caused this?'];

describe('useConversationSuggestions', () => {
  beforeEach(() => jest.clearAllMocks());

  it('initialises with empty suggestions', () => {
    const { result } = renderHook(() => useConversationSuggestions('acc-1'));
    expect(result.current.suggestions).toEqual([]);
  });

  it('fetchSuggestions does nothing when accountId is missing', async () => {
    const { result } = renderHook(() => useConversationSuggestions(''));
    await act(async () => {
      await result.current.fetchSuggestions('conv-1', 'msg-1');
    });
    expect(mockGetSuggestions).not.toHaveBeenCalled();
  });

  it('fetchSuggestions does nothing when conversationId is missing', async () => {
    const { result } = renderHook(() => useConversationSuggestions('acc-1'));
    await act(async () => {
      await result.current.fetchSuggestions('', 'msg-1');
    });
    expect(mockGetSuggestions).not.toHaveBeenCalled();
  });

  it('fetchSuggestions does nothing when lastMessageId is missing', async () => {
    const { result } = renderHook(() => useConversationSuggestions('acc-1'));
    await act(async () => {
      await result.current.fetchSuggestions('conv-1', '');
    });
    expect(mockGetSuggestions).not.toHaveBeenCalled();
  });

  it('fetches and sets suggestions from API', async () => {
    mockGetSuggestions.mockResolvedValue({
      data: {
        data: {
          ai_get_conversation_suggestions: {
            data: { suggestions: mockSuggestions },
          },
        },
      },
    });
    const { result } = renderHook(() => useConversationSuggestions('acc-1'));
    await act(async () => {
      await result.current.fetchSuggestions('conv-1', 'msg-1');
    });
    expect(result.current.suggestions).toEqual(mockSuggestions);
  });

  it('calls API with correct parameters', async () => {
    mockGetSuggestions.mockResolvedValue({ data: { data: { ai_get_conversation_suggestions: { data: { suggestions: [] } } } } });
    const { result } = renderHook(() => useConversationSuggestions('acc-1'));
    await act(async () => {
      await result.current.fetchSuggestions('conv-42', 'msg-99');
    });
    expect(mockGetSuggestions).toHaveBeenCalledWith({
      account_id: 'acc-1',
      conversation_id: 'conv-42',
      message_id: 'msg-99',
      user_id: '',
    });
  });

  it('clearSuggestions resets suggestions to empty array', async () => {
    mockGetSuggestions.mockResolvedValue({
      data: { data: { ai_get_conversation_suggestions: { data: { suggestions: mockSuggestions } } } },
    });
    const { result } = renderHook(() => useConversationSuggestions('acc-1'));
    await act(async () => {
      await result.current.fetchSuggestions('conv-1', 'msg-1');
    });
    expect(result.current.suggestions).toHaveLength(3);

    act(() => result.current.clearSuggestions());
    expect(result.current.suggestions).toEqual([]);
  });

  it('does not throw when API call fails', async () => {
    mockGetSuggestions.mockRejectedValue(new Error('Network error'));
    const { result } = renderHook(() => useConversationSuggestions('acc-1'));
    await expect(
      act(async () => {
        await result.current.fetchSuggestions('conv-1', 'msg-1');
      })
    ).resolves.not.toThrow();
    expect(result.current.suggestions).toEqual([]);
  });
});
