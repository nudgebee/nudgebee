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
  // Hook constructs a fetcher per instance. Route fetcher.fetch() through the
  // same `getLlmConversation` mock so tests can drive responses with the
  // existing mockGetConversation.mockResolvedValue API.
  createConversationFetcher: jest.fn(() => ({
    fetch: jest.fn((args) => fetcherDelegate(args)),
    reset: jest.fn(),
  })),
}));

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: {
    aiGenerateWorkflow: jest.fn(),
  },
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
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

jest.mock('@components/workflow/utils', () => ({
  buildWorkflowConversationMessages: jest.fn(() => [{ type: 'response', text: 'Workflow result' }]),
}));

jest.mock('@lib/auth', () => ({
  getUserSession: jest.fn(() => ({ user: { name: 'Test User' } })),
}));

jest.mock('uuid', () => ({ v4: jest.fn(() => 'test-session-id') }));

import apiAskNudgebee from '@api1/ask-nudgebee';
import { toast as snackbar } from '@ui/Toast';

// The mock's createConversationFetcher closes over this delegate so tests can
// drive the fetcher's responses by calling mockGetConversation.mockResolvedValue.
const fetcherDelegate = (args) => apiAskNudgebee.getLlmConversation(args);

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

  // Reconciling optimistic question placeholders against server-confirmed
  // questions. The placeholder's created_at is a client clock value while the
  // confirmed question's created_at is a server clock value, so they must not
  // be compared directly — a browser clock running ahead of the server used to
  // leave the placeholder behind as a duplicate question (the suggestion-click
  // bug). Reconciliation is by occurrence count per text, immune to clock skew.
  describe('optimistic question reconciliation', () => {
    const confirmedConversation = (message, createdAt) => ({
      data: {
        data: {
          llm_conversations: [
            {
              id: 'conv-1',
              title: 'Chat',
              status: 'IN_PROGRESS',
              llm_conversation_messages: [{ id: 'm-1', message, created_at: createdAt, user: { display_name: 'Alice' } }],
            },
          ],
        },
        errors: [],
      },
    });

    it('removes the optimistic placeholder even when the server timestamp predates the client one', async () => {
      // Server confirms the question one minute BEFORE the client-stamped
      // optimistic placeholder (browser clock ahead of server).
      mockGetConversation.mockResolvedValue(confirmedConversation('Show me logs', '2026-06-09T05:29:00.000Z'));
      const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));

      act(() => {
        result.current.setMessages((prev) => [
          ...prev,
          { id: 'optimistic-1', text: 'Show me logs', type: 'question', isOptimistic: true, created_at: '2026-06-09T05:30:00.000Z' },
        ]);
      });

      await act(async () => {
        await result.current.fetchConversation('sess-1', null, 'poll', false);
      });

      const questions = result.current.messages.filter((m) => m.type === 'question' && m.text === 'Show me logs');
      expect(questions).toHaveLength(1);
      expect(result.current.messages.some((m) => m.isOptimistic)).toBe(false);
    });

    it('keeps the optimistic placeholder until its own confirmation arrives', async () => {
      // Server has not yet persisted the new question — placeholder must remain.
      mockGetConversation.mockResolvedValue({
        data: { data: { llm_conversations: [{ id: 'conv-1', title: 'Chat', status: 'IN_PROGRESS', llm_conversation_messages: [] }] }, errors: [] },
      });
      const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));

      act(() => {
        result.current.setMessages((prev) => [
          ...prev,
          { id: 'optimistic-1', text: 'Pending question', type: 'question', isOptimistic: true, created_at: '2026-06-09T05:30:00.000Z' },
        ]);
      });

      await act(async () => {
        await result.current.fetchConversation('sess-1', null, 'poll', false);
      });

      const pending = result.current.messages.filter((m) => m.isOptimistic && m.text === 'Pending question');
      expect(pending).toHaveLength(1);
    });
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

  // Mutual-exclusivity: the wire format must never carry both blanket and
  // tier picks at once. The reducer enforces this by clearing the other
  // slot whenever one is written.
  describe('blanket vs per-tier mutual exclusivity', () => {
    it('setSelectedModel clears any previously-set tier picks', () => {
      const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
      const tierPicks = {
        reasoning: { provider: 'googleai', model: 'gemini-2.5-pro' },
        retrieval: { provider: 'openai', model: 'gpt-4o-mini' },
      };
      act(() => result.current.setSelectedTierModels(tierPicks));
      expect(result.current.selectedTierModels).toEqual(tierPicks);
      expect(result.current.selectedModel).toBeNull();

      const blanket = { provider: 'anthropic', model: 'claude-opus-4-7' };
      act(() => result.current.setSelectedModel(blanket));

      expect(result.current.selectedModel).toEqual(blanket);
      expect(result.current.selectedTierModels).toBeNull();
    });

    it('setSelectedTierModels clears any previously-set blanket model', () => {
      const { result } = renderHook(() => useLLMInvestigationControl('acc-1'));
      const blanket = { provider: 'anthropic', model: 'claude-opus-4-7' };
      act(() => result.current.setSelectedModel(blanket));
      expect(result.current.selectedModel).toEqual(blanket);
      expect(result.current.selectedTierModels).toBeNull();

      const tierPicks = { summary: { provider: 'openai', model: 'gpt-4o-mini' } };
      act(() => result.current.setSelectedTierModels(tierPicks));

      expect(result.current.selectedTierModels).toEqual(tierPicks);
      expect(result.current.selectedModel).toBeNull();
    });
  });
});
