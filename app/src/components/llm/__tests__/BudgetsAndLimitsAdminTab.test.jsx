import React from 'react';
import { render, screen } from '@testing-library/react';
import { useRouter } from 'next/router';
import BudgetsAndLimitsAdminTab from '../admin/BudgetsAndLimitsAdminTab';

jest.mock('next/router', () => ({ useRouter: jest.fn() }));
jest.mock('@components/llm/LLMConsumptionTab', () => {
  const UsageTab = () => <div>usage-tab</div>;
  return { __esModule: true, default: UsageTab };
});
jest.mock('@components/llm/ModelPricingTab', () => {
  const PricingTab = () => <div>pricing-tab</div>;
  return { __esModule: true, default: PricingTab };
});

describe('Budgets & Limits sub-tab from the URL', () => {
  it('opens Model Pricing when the link asks for it', () => {
    useRouter.mockReturnValue({ isReady: true, query: { budgetsTab: 'pricing' } });
    render(<BudgetsAndLimitsAdminTab />);
    expect(screen.getByText('pricing-tab')).toBeInTheDocument();
  });

  it('opens Usage & Limits by default', () => {
    useRouter.mockReturnValue({ isReady: true, query: {} });
    render(<BudgetsAndLimitsAdminTab />);
    expect(screen.getByText('usage-tab')).toBeInTheDocument();
  });

  it('switches to Model Pricing once the router is ready', () => {
    useRouter.mockReturnValue({ isReady: false, query: {} });
    const { rerender } = render(<BudgetsAndLimitsAdminTab />);
    expect(screen.getByText('usage-tab')).toBeInTheDocument();

    useRouter.mockReturnValue({ isReady: true, query: { budgetsTab: 'pricing' } });
    rerender(<BudgetsAndLimitsAdminTab />);
    expect(screen.getByText('pricing-tab')).toBeInTheDocument();
  });
});
