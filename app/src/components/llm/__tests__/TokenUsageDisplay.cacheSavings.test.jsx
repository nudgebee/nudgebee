import React from 'react';
import { render, screen } from '@testing-library/react';
import { MessageTokenUsage } from '../common/TokenUsageDisplay';

jest.mock('@ui/Tooltip', () => {
  const Tooltip = ({ title, children }) => (
    <div>
      {title}
      {children}
    </div>
  );
  return { __esModule: true, default: Tooltip };
});

const baseMessage = {
  message_input_tokens: 1000000,
  message_output_tokens: 10000,
  message_cached_input_tokens: 900000,
  message_cache_hit_rate_percentage: 60,
  message_cost_usd: 1.5,
  agents: [
    {
      agent_name: 'k8s_orchestrator',
      input_tokens: 1000000,
      output_tokens: 10000,
      cached_input_tokens: 900000,
      cost_usd: 1.5,
      requests: 3,
      model_name: { String: 'claude-sonnet-5', Valid: true },
    },
  ],
};

describe('message cache savings', () => {
  it('shows the savings the backend priced per call, cache writes included', () => {
    const messageData = {
      ...baseMessage,
      message_cache_savings: {
        tokens_saved: 900000,
        cache_hit_rate_percentage: 60,
        actual_cost_usd: 1.5,
        estimated_cost_without_cache_usd: 3.1,
        cost_savings_usd: 1.6,
      },
    };
    render(<MessageTokenUsage messageData={messageData} />);

    expect(screen.getByText('Cost without Cache:')).toBeInTheDocument();
    expect(screen.getByText('$3.10')).toBeInTheDocument();
    expect(screen.getByText('$1.60')).toBeInTheDocument();
  });

  it('shows no savings block when the backend sent none', () => {
    render(<MessageTokenUsage messageData={baseMessage} />);
    expect(screen.queryByText('Cost without Cache:')).not.toBeInTheDocument();
  });
});
