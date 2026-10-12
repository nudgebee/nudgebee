import { render, screen } from '@testing-library/react';
import apiKubernetes1 from '@api1/kubernetes1';
import PrometheusCPUUsageCard from '../PrometheusCPUUsageCard';

jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { utilisationApi: jest.fn() } }));
jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => <div data-testid='cpu-chart' /> } }));
jest.mock('@ui/EmptyState', () => ({
  EmptyState: ({ title, description }) => (
    <div data-testid='cpu-empty'>
      {title}: {description}
    </div>
  ),
}));

const event = {
  starts_at: '2025-01-15T02:48:08Z',
  cloud_account_id: 'account-1',
  subject_type: 'pod',
  subject_namespace: 'shop',
  subject_name: 'cart-7f9',
};
const KEYS = ['cpu_usage_pod', 'cpu_request_pod', 'cpu_limit_pod'];
const samples = [{ metric: {}, timestamps: [1736909288, 1736909348], values: ['0.2', '0.4'] }];

// The card as the page builds it: asked whether it has content, then for its line and its body.
const openCard = async (results) => {
  apiKubernetes1.utilisationApi.mockResolvedValue(results);
  const card = new PrometheusCPUUsageCard();
  const shown = await card.canRenderContent(null, event);
  const Body = card.getContentComponents()[0];
  render(<Body />);
  return { shown, lines: card.getHighLightsData().map((l) => l.message) };
};

describe('PrometheusCPUUsageCard', () => {
  beforeEach(() => apiKubernetes1.utilisationApi.mockReset());

  it('charts the usage and states its peak when the metrics source answered', async () => {
    const { shown, lines } = await openCard(KEYS.map((key) => ({ query_key: key, payload: key === 'cpu_limit_pod' ? [] : samples })));
    expect(shown).toBe(true);
    expect(lines).toEqual(['CPU peaked at 0.4 cores, 100% of its 0.4-core request; no CPU limit']);
    expect(screen.getByTestId('cpu-chart')).toBeInTheDocument();
    expect(screen.queryByTestId('cpu-empty')).not.toBeInTheDocument();
  });

  // What the server sends for a cluster with no metrics source: every key present, no series,
  // and a note saying why (api-server/services/observability/utilisation_complete.go).
  it('states the server’s reason, on the line and in place of an empty chart', async () => {
    const note = 'no metrics provider resolved for this account';
    const { shown, lines } = await openCard(KEYS.map((key) => ({ query_key: key, payload: [], note })));
    expect(shown).toBe(true);
    expect(lines).toEqual([`No CPU data: ${note}`]);
    expect(screen.getByTestId('cpu-empty')).toHaveTextContent(`No CPU data for this alert: ${note}`);
    expect(screen.queryByTestId('cpu-chart')).not.toBeInTheDocument();
  });

  it('uses the error of a failed query when it carries no note', async () => {
    const { lines } = await openCard(KEYS.map((key) => ({ query_key: key, payload: [], error: 'connection refused' })));
    expect(lines).toEqual(['No CPU data: connection refused']);
  });

  it('keeps the plain no-samples line and the chart when the source answered with nothing', async () => {
    const { lines } = await openCard(KEYS.map((key) => ({ query_key: key, payload: [] })));
    expect(lines).toEqual(['No CPU samples for this pod in the 10 minutes around the alert']);
    expect(screen.getByTestId('cpu-chart')).toBeInTheDocument();
  });
});
