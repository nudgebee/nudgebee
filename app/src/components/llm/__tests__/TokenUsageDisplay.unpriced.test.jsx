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

const conversation = (overrides = {}) => ({
  total_input_tokens: 3000,
  total_output_tokens: 300,
  total_requests: 3,
  total_cost_usd: 0,
  total_unpriced_requests: 0,
  model_usage: [
    { model_name: 'new-model', model_provider: 'custom', requests: 2, input_tokens: 2000, output_tokens: 200, cost_usd: 0, unpriced_requests: 2 },
    { model_name: 'free-model', model_provider: 'custom', requests: 1, input_tokens: 1000, output_tokens: 100, cost_usd: 0, unpriced_requests: 0 },
  ],
  ...overrides,
});

describe('cost of models with no price set', () => {
  it('says "No price" for a model with no pricing entry, and $0.00 for a free one', () => {
    render(<ConversationTokenUsage tokenUsageData={conversation({ total_unpriced_requests: 2 })} />);

    expect(screen.getByText('No price')).toBeInTheDocument();
    expect(screen.getAllByText('$0.00').length).toBeGreaterThan(0);
    expect(screen.getByText(/Set a price in Admin/)).toBeInTheDocument();
    expect(screen.getByText(/Excludes 2 unpriced/)).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /Model Pricing/ });
    expect(link).toHaveAttribute('href', '/user-management?budgetsTab=pricing#ai-tools/budgets-limits');
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('shows no hint when every model is priced', () => {
    const data = conversation({
      model_usage: [{ model_name: 'free-model', requests: 1, input_tokens: 1000, output_tokens: 100, cost_usd: 0, unpriced_requests: 0 }],
    });
    render(<ConversationTokenUsage tokenUsageData={data} />);

    expect(screen.queryByText('No price')).not.toBeInTheDocument();
    expect(screen.queryByText(/Set a price in Admin/)).not.toBeInTheDocument();
  });

  it('flags unpriced calls in the message popover too', () => {
    const messageData = {
      message_input_tokens: 1000,
      message_output_tokens: 100,
      message_cost_usd: 0,
      agents: [
        {
          agent_name: 'k8s_debug',
          input_tokens: 1000,
          output_tokens: 100,
          cost_usd: 0,
          unpriced_requests: 1,
          requests: 1,
          model_name: { String: 'new-model', Valid: true },
        },
      ],
    };
    render(<MessageTokenUsage messageData={messageData} />);

    expect(screen.getAllByText('No price').length).toBeGreaterThan(0);
    expect(screen.getByText(/Excludes 1 unpriced/)).toBeInTheDocument();
  });
});
