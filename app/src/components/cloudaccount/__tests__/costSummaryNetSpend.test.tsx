import '@testing-library/jest-dom';
import { cleanup, render, screen } from '@testing-library/react';
import { CloudCostSummary } from '@components/cloudaccount/CloudCostSummary';

jest.mock('@shared/charts/DoughnutChart', () => ({
  __esModule: true,
  default: () => <div data-testid='savings-doughnut' />,
}));

afterEach(cleanup);

// When this month's credits fully cover the bill, net spend is an exact $0 — a
// known figure, not missing data. It must read "< $1" (per the ticket), never
// "No data" (which is what SubCaption's zero-means-absent rule produced).
describe('CloudCostSummary net spend (MTD)', () => {
  const withCredits = (gross: number, credits: number) => ({
    gross_spends_aggregate: { aggregate: { sum: { amount: gross } } },
    lm_gross_spends_aggregate: { aggregate: { sum: { amount: 1000 } } },
    credits_aggregate: { aggregate: { sum: { amount: -credits } } },
  });

  it('shows "< $1" (not "No data") when credits fully cover the month (net = $0)', () => {
    const { container } = render(<CloudCostSummary clusterSummary={withCredits(323.87, 323.87)} currencySymbol='$' />);
    expect(screen.getByText('Net spend (MTD):')).toBeInTheDocument();
    expect(container.textContent).toContain('< $1');
    expect(container.textContent).not.toContain('No data');
  });

  it('shows the net figure when credits only partly offset the month', () => {
    const { container } = render(<CloudCostSummary clusterSummary={withCredits(500, 94.48)} currencySymbol='$' />);
    expect(screen.getByText('Net spend (MTD):')).toBeInTheDocument();
    // 500 - 94.48 = 405.52 -> "$406" at zero-decimal precision.
    expect(container.textContent).toContain('$406');
  });

  it('omits the net spend line when there are no current-month credits', () => {
    const summary = {
      gross_spends_aggregate: { aggregate: { sum: { amount: 500 } } },
      lm_gross_spends_aggregate: { aggregate: { sum: { amount: 1000 } } },
    };
    render(<CloudCostSummary clusterSummary={summary} currencySymbol='$' />);
    expect(screen.queryByText('Net spend (MTD):')).not.toBeInTheDocument();
  });
});
