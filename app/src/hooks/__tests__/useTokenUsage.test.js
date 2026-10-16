import { renderHook, act } from '@testing-library/react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { useTokenUsage } from '@hooks/useTokenUsage';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { getConversationUsageMetrics: jest.fn() },
}));

describe('useTokenUsage', () => {
  it('keeps thinking token totals for the conversation and each message', async () => {
    apiAskNudgebee.getConversationUsageMetrics.mockResolvedValue({
      conversation: {
        total_input_tokens: 2000,
        total_output_tokens: 200,
        total_thinking_tokens: 1500,
        messages: [
          {
            message_id: 'm1',
            message_input_tokens: 2000,
            message_output_tokens: 200,
            message_thinking_tokens: 1500,
            agents: [{ agent_name: 'k8s_orchestrator', thinking_tokens: 1500 }],
          },
        ],
      },
    });

    const { result } = renderHook(() => useTokenUsage('account-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });

    expect(result.current.tokenUsageData.total_thinking_tokens).toBe(1500);
    expect(result.current.messageTokenData.m1.message_thinking_tokens).toBe(1500);
  });
});
