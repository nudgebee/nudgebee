import React from 'react';
import { render, screen } from '@testing-library/react';
import { ConversationTokenUsage, MessageTokenUsage } from '../common/TokenUsageDisplay';

jest.mock('@ui/Tooltip', () => {
  const Tooltip = ({ title, children }) => (
    <div>
      {title}
      {children}
    </div>
  );
  return { __esModule: true, default: Tooltip };
});

describe('thinking tokens', () => {
  it('shows a Thinking column in the message Model Usage and Agent Usage tables', () => {
    const messageData = {
      message_input_tokens: 5700000,
      message_output_tokens: 8700,
      message_cached_input_tokens: 5300000,
      message_thinking_tokens: 235000,
      message_cost_usd: 1.61,
      agents: [
        {
          agent_name: 'k8s_orchestrator',
          input_tokens: 5700000,
          output_tokens: 8700,
          cached_input_tokens: 5300000,
          thinking_tokens: 235000,
          cost_usd: 1.61,
          requests: 40,
          model_name: { String: 'gemini-3.7-flash', Valid: true },
        },
      ],
    };
    render(<MessageTokenUsage messageData={messageData} />);

    // One header per table, carrying the billing explanation.
    const headers = screen.getAllByText('Thinking');
    expect(headers).toHaveLength(2);
    headers.forEach((h) => expect(h).toHaveAttribute('title', expect.stringMatching(/output rate/)));
    // Model row + agent row.
    expect(screen.getAllByText('235.0K')).toHaveLength(2);
    expect(screen.getByText(/Thinking:\s*235\.0K/)).toBeInTheDocument();
  });

  it('shows a Thinking column in the conversation Model Usage table', () => {
    const data = {
      total_input_tokens: 2000,
      total_output_tokens: 200,
      total_thinking_tokens: 1500,
      total_requests: 2,
      total_cost_usd: 0.01,
      model_usage: [{ model_name: 'gemini-3.7-flash', requests: 2, input_tokens: 2000, output_tokens: 200, thinking_tokens: 1500, cost_usd: 0.01 }],
    };
    render(<ConversationTokenUsage tokenUsageData={data} />);

    expect(screen.getByText('Thinking')).toBeInTheDocument();
    expect(screen.getByText('1.5K')).toBeInTheDocument();
    expect(screen.getByText(/Thinking:\s*1\.5K/)).toBeInTheDocument();
  });
});
