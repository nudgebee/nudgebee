import '@testing-library/jest-dom';
import { cleanup, render, screen } from '@testing-library/react';
import { CloudCostSummary } from '@components/cloudaccount/CloudCostSummary';

jest.mock('@shared/charts/DoughnutChart', () => ({
  __esModule: true,
  default: ({ displayValue }: { displayValue: string }) => <div data-testid='savings-doughnut'>{displayValue}</div>,
}));

afterEach(cleanup);

// Every producer prices savings in USD; only spend is in the account's billing
// currency. The card used to print the USD savings with the spend symbol, so an
// account billed in INR read "₹173/yr" for a $173 estimate.
describe('CloudCostSummary savings stay in USD', () => {
  const summary = {
    gross_spends_aggregate: { aggregate: { sum: { amount: 50000 } } },
    yearly_gross_spends_aggregate: { aggregate: { sum: { amount: 600000 } } },
    recommendation_aggregate: { aggregate: { sum: { estimated_savings: 1000 } } },
  };

  it('prints savings in $ with a USD marker and keeps spend in the billing currency', () => {
    const { container } = render(<CloudCostSummary clusterSummary={summary} currencySymbol='₹' />);
    const text = container.textContent || '';
    expect(text).toContain('$12,000/yr');
    expect(text).not.toContain('₹12,000');
    expect(text).toContain('₹50,000');
    expect(screen.getByText('estimated 12 mos · USD')).toBeInTheDocument();
    // USD savings over INR spend is not a percentage.
    expect(screen.queryByTestId('savings-doughnut')).not.toBeInTheDocument();
  });

  it('shows no marker and keeps the savings share for a USD-billed account', () => {
    render(<CloudCostSummary clusterSummary={summary} currencySymbol='$' />);
    expect(screen.getByText('estimated 12 mos')).toBeInTheDocument();
    expect(screen.getByTestId('savings-doughnut')).toBeInTheDocument();
  });
});
