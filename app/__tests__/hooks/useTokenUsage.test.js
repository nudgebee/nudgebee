import { renderHook, act } from '@testing-library/react';
import { useTokenUsage } from '@hooks/useTokenUsage';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    getConversationUsageMetrics: jest.fn(),
  },
}));

import apiAskNudgebee from '@api1/ask-nudgebee';
const mockGetMetrics = apiAskNudgebee.getConversationUsageMetrics;

const mockConversationData = {
  total_input_tokens: 1000,
  total_output_tokens: 500,
  total_cached_input_tokens: 200,
  total_cost_usd: 0.05,
  model_usage: [],
  total_requests: 3,
  successful_requests: 3,
  failed_requests: 0,
  total_tool_calls: 2,
  successful_tool_calls: 2,
  messages: [
    {
      message_id: 'msg-1',
      message_input_tokens: 300,
      message_output_tokens: 150,
      message_cached_input_tokens: 50,
      message_cost_usd: 0.02,
      agents: [
        {
          agent_id: 'agent-1',
          input_tokens: 300,
          output_tokens: 150,
          cached_input_tokens: 50,
          cost_usd: 0.02,
          agent_name: 'k8s_debug',
        },
      ],
    },
  ],
};

describe('useTokenUsage', () => {
  beforeEach(() => jest.clearAllMocks());

  it('initialises with null conversation and empty message/agent maps', () => {
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    expect(result.current.tokenUsageData).toBeNull();
    expect(result.current.messageTokenData).toEqual({});
    expect(result.current.agentTokenData).toEqual({});
  });

  it('fetchTokenUsage does nothing when accountId is missing', async () => {
    const { result } = renderHook(() => useTokenUsage(''));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });
    expect(mockGetMetrics).not.toHaveBeenCalled();
  });

  it('fetchTokenUsage does nothing when sessionId is missing', async () => {
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('');
    });
    expect(mockGetMetrics).not.toHaveBeenCalled();
  });

  it('fetches and populates tokenUsageData', async () => {
    mockGetMetrics.mockResolvedValue(mockConversationData);
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });
    expect(result.current.tokenUsageData).not.toBeNull();
    expect(result.current.tokenUsageData.total_input_tokens).toBe(1000);
    expect(result.current.tokenUsageData.total_output_tokens).toBe(500);
    expect(result.current.tokenUsageData.total_cost_usd).toBe(0.05);
  });

  it('populates messageTokenData keyed by message_id', async () => {
    mockGetMetrics.mockResolvedValue(mockConversationData);
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });
    expect(result.current.messageTokenData['msg-1']).toBeDefined();
    expect(result.current.messageTokenData['msg-1'].message_input_tokens).toBe(300);
  });

  it('populates agentTokenData keyed by agent_id', async () => {
    mockGetMetrics.mockResolvedValue(mockConversationData);
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });
    expect(result.current.agentTokenData['agent-1']).toBeDefined();
    expect(result.current.agentTokenData['agent-1'].agent_name).toBe('k8s_debug');
  });

  it('resetTokenMetrics clears all metrics', async () => {
    mockGetMetrics.mockResolvedValue(mockConversationData);
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });
    expect(result.current.tokenUsageData).not.toBeNull();

    act(() => result.current.resetTokenMetrics());
    expect(result.current.tokenUsageData).toBeNull();
    expect(result.current.messageTokenData).toEqual({});
    expect(result.current.agentTokenData).toEqual({});
  });

  it('getAgentTokenDataForMessage returns agent data by tool_id', async () => {
    mockGetMetrics.mockResolvedValue(mockConversationData);
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });
    const agentData = result.current.getAgentTokenDataForMessage({ tool_id: 'agent-1' });
    expect(agentData).toBeDefined();
    expect(agentData.agent_name).toBe('k8s_debug');
  });

  it('getAgentTokenDataForMessage returns null when no matching data', () => {
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    const agentData = result.current.getAgentTokenDataForMessage({ tool_id: 'nonexistent' });
    expect(agentData).toBeNull();
  });

  it('does not throw when API call fails', async () => {
    mockGetMetrics.mockRejectedValue(new Error('API error'));
    const { result } = renderHook(() => useTokenUsage('acc-1'));
    await expect(
      act(async () => {
        await result.current.fetchTokenUsage('session-1');
      })
    ).resolves.not.toThrow();
  });
});
