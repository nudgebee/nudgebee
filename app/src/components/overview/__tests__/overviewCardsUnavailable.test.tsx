import { render, screen } from '@testing-library/react';
import CloudAccountOverviewCard from '@components/overview/CloudAccountOverviewCard';
import VmAccountOverviewCard from '@components/overview/VmAccountOverviewCard';

jest.mock('@api1/vm', () => ({ SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low'] }));

const cloudSummary = {
  accountId: 'a',
  currency: 'USD',
  mtdSpend: 0,
  lastMonthSpend: 0,
  ytdSpend: 0,
  resourceCount: 0,
  recommendationCount: 0,
  estimatedSavings: 0,
  alertCount: 0,
};

// "-" must mean "the rollup never arrived"; a real zero must read as 0 so an
// empty account and a failed query stop looking identical.
describe('Account Overview cards: zero vs unavailable', () => {
  it('cloud card shows 0 for an account with no data', () => {
    render(<CloudAccountOverviewCard accountId='a' accountName='empty-aws' cloudProvider='AWS' summary={cloudSummary} />);
    // Resources, Recommendations, Alerts read "0"; the savings tile keeps its own
    // "-" for a zero saving, same as the K8s tile beside it.
    expect(screen.getAllByText('0').length).toBeGreaterThanOrEqual(3);
    expect(screen.queryByText('Vulnerability summary could not be loaded.')).toBeNull();
  });

  it('cloud card shows "-" everywhere when the summary failed to load', () => {
    render(<CloudAccountOverviewCard accountId='a' accountName='aws-prod' cloudProvider='AWS' summary={undefined} />);
    expect(screen.getAllByText('-').length).toBeGreaterThanOrEqual(7); // 3 counts + 4 cost cells
    expect(screen.queryByText('$0')).toBeNull();
    expect(screen.queryByText('0')).toBeNull();
  });

  it('VM card says the summary could not be loaded rather than "no vulnerabilities"', () => {
    render(<VmAccountOverviewCard accountId='v' accountName='fleet' summary={undefined} onOpen={jest.fn()} />);
    expect(screen.getByText(/could not be loaded/)).toBeInTheDocument();
    expect(screen.queryByText(/No open package vulnerabilities/)).toBeNull();
  });

  it('VM card keeps the "no vulnerabilities" copy for a clean fleet', () => {
    render(
      <VmAccountOverviewCard
        accountId='v'
        accountName='fleet'
        summary={{ accountId: 'v', vmCount: 3, severities: {}, vulnerabilityCount: 0 }}
        onOpen={jest.fn()}
      />
    );
    expect(screen.getByText(/No open package vulnerabilities/)).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('0')).toBeInTheDocument();
  });
});
