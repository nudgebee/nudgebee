import { renderHook, act, waitFor } from '@testing-library/react';
import { useLLMInvestigationControl } from '@hooks/useLLMInvestigationControl';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listModels: jest.fn(),
    getLlmConversation: jest.fn(),
    aiStopInvestigate: jest.fn(),
    aiGenerateInvestigate: jest.fn(),
    getModelConfig: jest.fn(),
  },
}));

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: {
    aiGenerateWorkflow: jest.fn(),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('src/utils/common', () => ({
  parseHttpResponseBodyMessage: jest.fn((e) => String(e)),
  safeJSONParse: jest.fn((val) => {
    try {
      return JSON.parse(val);
    } catch {
      return null;
    }
  }),
}));

jest.mock('@components1/workflow/utils', () => ({
  buildWorkflowConversationMessages: jest.fn(() => [{ type: 'response', text: 'Workflow result' }]),
}));

jest.mock('@lib/auth', () => ({
  getUserSession: jest.fn(() => ({ user: { name: 'Test User' } })),
}));

jest.mock('uuid', () => ({ v4: jest.fn(() => 'test-session-id') }));

import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';

const mockListModels = apiAskNudgebee.listModels;
const mockGetConversation = apiAskNudgebee.getLlmConversation;
const mockStopInvestigate = apiAskNudgebee.aiStopInvestigate;
const mockGenerateInvestigate = apiAskNudgebee.aiGenerateInvestigate;
const mockGetModelConfig = apiAskNudgebee.getModelConfig;

describe('useLLMInvestigationControl', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockListModels.mockResolvedValue({ data: { models: [], default: null } });
    mockGetModelConfig.mockResolvedValue({ data: null });
  });

  it('initialises with empty state', () => {
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    expect(result.current.messages).toEqual([]);
    expect(result.current.conversationStatus).toBe('');
    expect(result.current.isProcessing).toBe(false);
    expect(result.current.isLoading).toBe(false);
    expect(result.current.allowStop).toBe(false);
  });

  it('does not fetch models when accountId is falsy', () => {
    renderHook(() => useLLMInvestigationControl(''));
    expect(mockListModels).not.toHaveBeenCalled();
  });

  it('fetches available models on mount', async () => {
    mockListModels.mockResolvedValue({
      data: { models: [{ provider: 'anthropic', model: 'claude-3' }], default: 'claude-3' },
    });
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    await waitFor(() => expect(result.current.availableModels).toHaveLength(1));
    expect(result.current.defaultModel).toBe('claude-3');
  });

  it('resetInvestigationState clears all state', async () => {
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));

    act(() => {
      result.current.setIsProcessing(true);
      result.current.setAllowStop(true);
    });

    act(() => result.current.resetInvestigationState());

    expect(result.current.isProcessing).toBe(false);
    expect(result.current.allowStop).toBe(false);
    expect(result.current.messages).toEqual([]);
    expect(result.current.conversationStatus).toBe('');
  });

  it('startInvestigation does nothing when text is empty', async () => {
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    await act(async () => {
      await result.current.startInvestigation({ text: '' });
    });
    expect(mockGenerateInvestigate).not.toHaveBeenCalled();
  });

  it('startInvestigation calls aiGenerateInvestigate in investigate mode', async () => {
    mockGenerateInvestigate.mockResolvedValue({
      data: {
        data: {
          ai_execute_investigation: {
            data: { query: 'What is wrong?', session_id: 'sess-1' },
          },
        },
      },
    });
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    await act(async () => {
      await result.current.startInvestigation({ text: 'What is wrong?', apiMode: 'investigate' });
    });
    expect(mockGenerateInvestigate).toHaveBeenCalledWith(expect.objectContaining({ account_id: 'acc-1', query: 'What is wrong?' }));
  });

  it('sets conversationStatus to IN_PROGRESS after starting investigation', async () => {
    mockGenerateInvestigate.mockResolvedValue({
      data: {
        data: {
          ai_execute_investigation: {
            data: { query: 'Check pods', session_id: 'sess-1' },
          },
        },
      },
    });
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    await act(async () => {
      await result.current.startInvestigation({ text: 'Check pods' });
    });
    expect(result.current.conversationStatus).toBe('IN_PROGRESS');
  });

  it('stopInvestigation does nothing when allowStop is false', async () => {
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    await act(async () => {
      await result.current.stopInvestigation('conv-1', 'IN_PROGRESS', jest.fn());
    });
    expect(mockStopInvestigate).not.toHaveBeenCalled();
  });

  it('stopInvestigation calls aiStopInvestigate when allowStop is true', async () => {
    mockStopInvestigate.mockResolvedValue({
      data: { data: { ai_cancel_investigation: { data: { status: 'terminated' } } } },
    });
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    act(() => result.current.setAllowStop(true));

    await act(async () => {
      await result.current.stopInvestigation('conv-1', 'IN_PROGRESS', jest.fn());
    });
    expect(mockStopInvestigate).toHaveBeenCalledWith(expect.objectContaining({ accountId: 'acc-1', conversationId: 'conv-1' }));
    expect(snackbar.success).toHaveBeenCalledWith('Investigation terminated successfully');
  });

  it('fetchConversation sets conversationStatus from response', async () => {
    mockGetConversation.mockResolvedValue({
      data: {
        data: {
          llm_conversations: [
            {
              id: 'conv-1',
              title: 'Test Chat',
              status: 'COMPLETED',
              llm_conversation_messages: [],
            },
          ],
        },
        errors: [],
      },
    });
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    await act(async () => {
      await result.current.fetchConversation('sess-1', null, 'direct', false);
    });
    expect(result.current.conversationStatus).toBe('COMPLETED');
    expect(result.current.conversationTitle).toBe('Test Chat');
  });

  it('checkConversationExists returns { exists: false } when sessionId is empty', async () => {
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    let res;
    await act(async () => {
      res = await result.current.checkConversationExists('');
    });
    expect(res).toEqual({ exists: false });
    expect(mockGetConversation).not.toHaveBeenCalled();
  });

  it('checkConversationExists returns { exists: true } when conversations found', async () => {
    mockGetConversation.mockResolvedValue({
      data: {
        data: { llm_conversations: [{ id: 'conv-1' }] },
        errors: [],
      },
    });
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    let res;
    await act(async () => {
      res = await result.current.checkConversationExists('sess-1');
    });
    expect(res.exists).toBe(true);
  });

  it('setSelectedModel updates selectedModel', () => {
    const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
    const model = { provider: 'anthropic', model: 'claude-3-5-sonnet' };
    act(() => result.current.setSelectedModel(model));
    expect(result.current.selectedModel).toEqual(model);
  });
});
