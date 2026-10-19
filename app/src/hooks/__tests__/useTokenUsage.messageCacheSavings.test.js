import { renderHook, act } from '@testing-library/react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { useTokenUsage } from '@hooks/useTokenUsage';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { getConversationUsageMetrics: jest.fn() },
}));

describe('useTokenUsage message cache savings', () => {
  it('keeps the per-message cache savings the backend sends', async () => {
    const savings = { tokens_saved: 900, actual_cost_usd: 1.5, estimated_cost_without_cache_usd: 3.1, cost_savings_usd: 1.6 };
    apiAskNudgebee.getConversationUsageMetrics.mockResolvedValue({
      conversation: { messages: [{ message_id: 'm1', message_input_tokens: 1000, message_cache_savings: savings }] },
    });

    const { result } = renderHook(() => useTokenUsage('account-1'));
    await act(async () => {
      await result.current.fetchTokenUsage('session-1');
    });

    expect(result.current.messageTokenData.m1.message_cache_savings).toEqual(savings);
  });
});
