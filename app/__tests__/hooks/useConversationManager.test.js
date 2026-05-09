import { renderHook, act } from '@testing-library/react';
import { useConversationManager } from '@hooks/useConversationManager';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    saveConversation: jest.fn(),
    deleteSavedConversation: jest.fn(),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';

const mockSave = apiAskNudgebee.saveConversation;
const mockDelete = apiAskNudgebee.deleteSavedConversation;

describe('useConversationManager', () => {
  beforeEach(() => jest.clearAllMocks());

  it('initialises with correct default state', () => {
    const { result } = renderHook(() => useConversationManager());
    expect(result.current.rawConversations).toEqual([]);
    expect(result.current.likedConversations).toEqual([]);
    expect(result.current.selectedConversation).toBeNull();
    expect(result.current.activeFilter).toBe('All');
    expect(result.current.savingStates).toEqual({});
  });

  it('setRawConversations updates conversations array', () => {
    const { result } = renderHook(() => useConversationManager());
    const convos = [{ id: 'c1', title: 'Chat 1' }];
    act(() => result.current.setRawConversations(convos));
    expect(result.current.rawConversations).toEqual(convos);
  });

  it('setRawConversations accepts an updater function', () => {
    const { result } = renderHook(() => useConversationManager());
    act(() => result.current.setRawConversations([{ id: 'c1' }]));
    act(() => result.current.setRawConversations((prev) => [...prev, { id: 'c2' }]));
    expect(result.current.rawConversations).toHaveLength(2);
  });

  it('setLikedConversations updates liked ids', () => {
    const { result } = renderHook(() => useConversationManager());
    act(() => result.current.setLikedConversations(['conv-1', 'conv-2']));
    expect(result.current.likedConversations).toEqual(['conv-1', 'conv-2']);
  });

  it('setActiveFilter updates the active filter', () => {
    const { result } = renderHook(() => useConversationManager());
    act(() => result.current.setActiveFilter('Saved'));
    expect(result.current.activeFilter).toBe('Saved');
  });

  it('setSelectedConversation updates selected conversation', () => {
    const { result } = renderHook(() => useConversationManager());
    const convo = { id: 'c1', title: 'Test' };
    act(() => result.current.setSelectedConversation(convo));
    expect(result.current.selectedConversation).toEqual(convo);
  });

  it('handleLike (save) adds id to likedConversations on success', async () => {
    mockSave.mockResolvedValue({ data: { data: { ai_save_conversation: { data: { success: true } } } } });
    const { result } = renderHook(() => useConversationManager());

    await act(async () => {
      await result.current.handleLike('conv-1', false, null);
    });
    expect(result.current.likedConversations).toContain('conv-1');
    expect(snackbar.success).toHaveBeenCalledWith('Conversation saved successfully');
  });

  it('handleLike (save) shows error when success=false', async () => {
    mockSave.mockResolvedValue({ data: { data: { ai_save_conversation: { data: { success: false } } } } });
    const { result } = renderHook(() => useConversationManager());

    await act(async () => {
      await result.current.handleLike('conv-1', false, null);
    });
    expect(result.current.likedConversations).not.toContain('conv-1');
    expect(snackbar.error).toHaveBeenCalledWith('Failed to save conversation');
  });

  it('handleLike (unsave) removes id from likedConversations on success', async () => {
    mockDelete.mockResolvedValue({ data: { data: { ai_delete_saved_conversation: { data: { success: true } } } } });
    const { result } = renderHook(() => useConversationManager());
    act(() => result.current.setLikedConversations(['conv-1']));

    await act(async () => {
      await result.current.handleLike('conv-1', true, null);
    });
    expect(result.current.likedConversations).not.toContain('conv-1');
    expect(snackbar.success).toHaveBeenCalledWith('Conversation unsaved successfully');
  });

  it('handleLike (unsave) rolls back on API failure', async () => {
    mockDelete.mockResolvedValue({ data: { data: { ai_delete_saved_conversation: { data: { success: false } } } } });
    const { result } = renderHook(() => useConversationManager());
    act(() => result.current.setLikedConversations(['conv-1']));

    await act(async () => {
      await result.current.handleLike('conv-1', true, null);
    });
    expect(result.current.likedConversations).toContain('conv-1');
    expect(snackbar.error).toHaveBeenCalledWith('Failed to unsave the conversation');
  });

  it('handleLike uses currentSelectedId as fallback when sessionId is null', async () => {
    mockSave.mockResolvedValue({ data: { data: { ai_save_conversation: { data: { success: true } } } } });
    const { result } = renderHook(() => useConversationManager());

    await act(async () => {
      await result.current.handleLike(null, false, 'fallback-id');
    });
    expect(mockSave).toHaveBeenCalledWith({ conversation_id: 'fallback-id' });
  });

  it('prevents double-click (in-flight deduplication)', async () => {
    let resolveFirst;
    mockSave.mockReturnValueOnce(
      new Promise((res) => {
        resolveFirst = res;
      })
    );
    const { result } = renderHook(() => useConversationManager());

    act(() => {
      result.current.handleLike('conv-1', false, null);
      result.current.handleLike('conv-1', false, null); // duplicate click
    });

    resolveFirst({ data: { data: { ai_save_conversation: { data: { success: true } } } } });
    await act(async () => {
      await Promise.resolve();
    });

    // API should only be called once despite two rapid clicks
    expect(mockSave).toHaveBeenCalledTimes(1);
  });
});
