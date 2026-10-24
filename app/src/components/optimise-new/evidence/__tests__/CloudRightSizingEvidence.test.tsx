import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import apiCloudAccount from '@api1/cloud-account';
import CloudRightSizingEvidence from '../CloudRightSizingEvidence';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: { getCloudResourceMetrics: jest.fn(), getCloudResourceIdentity: jest.fn() },
}));
jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: { getRecommendationDetails: jest.fn(() => null) },
}));
jest.mock('@ui/Chart', () => {
  const Line = () => <div data-testid='chart' />;
  return { __esModule: true, default: { Line } };
});

const fetchMetrics = apiCloudAccount.getCloudResourceMetrics as jest.Mock;
const lookupResource = apiCloudAccount.getCloudResourceIdentity as jest.Mock;
const DAY_MS = 24 * 60 * 60 * 1000;
const fetchedDays = (call: number) => {
  const { startDate, endDate } = fetchMetrics.mock.calls[call][0];
  return Math.round((endDate.getTime() - startDate.getTime()) / DAY_MS);
};

const metricRows = {
  data: { data: { cloud_metric_groupings_v2: { rows: [{ metric: 'CPUUtilization', timestamp: '2026-10-01T00:00:00Z', avg_value: 4 }] } } },
};
const noRows = { data: { data: { cloud_metric_groupings_v2: { rows: [] } } } };

const fullRecommendation = { id: 'rec-1', account_id: 'acct-1', resource_id: 'i-0abc' };
const renderEvidence = () =>
  render(
    <CloudRightSizingEvidence
      recommendation={{ current_instance_type: 'm5.large', recommended_instance_type: 't3.medium' }}
      ruleName='aws_ec2_underutilized'
      fullRecommendation={fullRecommendation}
    />
  );

beforeEach(() => {
  fetchMetrics.mockReset();
  lookupResource.mockReset();
  lookupResource.mockResolvedValue({ resource_id: 'i-0abc', service_name: 'AmazonEC2', region: 'us-east-1' });
});

describe('CloudRightSizingEvidence range', () => {
  it('fetches a week by default and refetches over the picked range', async () => {
    fetchMetrics.mockResolvedValue(metricRows);
    renderEvidence();
    await screen.findByText('Resource Monitoring (7d)');
    expect(fetchedDays(0)).toBe(7);

    fireEvent.click(screen.getByRole('tab', { name: '30d' }));
    await screen.findByText('Resource Monitoring (30d)');
    expect(fetchMetrics).toHaveBeenCalledTimes(2);
    expect(fetchedDays(1)).toBe(30);
  });

  it('never leaves the previous range on screen when the picked one is empty, and keeps the selector', async () => {
    fetchMetrics.mockResolvedValueOnce(metricRows).mockResolvedValueOnce(noRows);
    renderEvidence();
    await screen.findByText('Resource Monitoring (7d)');

    fireEvent.click(screen.getByRole('tab', { name: '14d' }));
    await waitFor(() => expect(screen.queryByTestId('chart')).not.toBeInTheDocument());
    expect(screen.queryByText(/Resource Monitoring/)).not.toBeInTheDocument();
    expect(screen.getByTestId('trend-range-toggle')).toBeInTheDocument();
  });

  it('drops the previous range while the next one loads, and after it fails', async () => {
    let fail: (e: any) => void = () => {};
    fetchMetrics.mockResolvedValueOnce(metricRows).mockImplementationOnce(() => new Promise((_, reject) => (fail = reject)));
    jest.spyOn(console, 'error').mockImplementation(() => {});
    renderEvidence();
    await screen.findByText('Resource Monitoring (7d)');

    fireEvent.click(screen.getByRole('tab', { name: '30d' }));
    expect(screen.queryByTestId('chart')).not.toBeInTheDocument();
    fail(new Error('timeout'));
    await waitFor(() => expect(fetchMetrics).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId('chart')).not.toBeInTheDocument();
    expect(screen.getByTestId('trend-range-toggle')).toBeInTheDocument();
  });

  it('offers a longer range on a utilization rule whose week came back empty', async () => {
    fetchMetrics.mockResolvedValue(noRows);
    renderEvidence();
    await waitFor(() => expect(fetchMetrics).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('trend-range-toggle')).toBeInTheDocument();
  });

  it('offers no range on a rule without utilization series when no metrics come back', async () => {
    fetchMetrics.mockResolvedValue(noRows);
    render(
      <CloudRightSizingEvidence
        recommendation={{ reason: 'Retention period not set.' }}
        ruleName='aws_cloudwatch_log_group_retention'
        fullRecommendation={fullRecommendation}
      />
    );
    await waitFor(() => expect(fetchMetrics).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByRole('progressbar')).not.toBeInTheDocument());
    expect(screen.queryByTestId('trend-range-toggle')).not.toBeInTheDocument();
  });

  it('keeps the range on such a rule once metrics came back, even when a picked range is empty', async () => {
    fetchMetrics.mockResolvedValueOnce(metricRows).mockResolvedValueOnce(noRows).mockResolvedValue(noRows);
    const props = { recommendation: { reason: 'x' }, ruleName: 'aws_rds_free_storage_space' };
    const { rerender } = render(<CloudRightSizingEvidence {...props} fullRecommendation={fullRecommendation} />);
    await screen.findByText('Resource Monitoring (7d)');
    expect(screen.getByTestId('trend-range-toggle')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: '30d' }));
    await waitFor(() => expect(screen.queryByTestId('chart')).not.toBeInTheDocument());
    await waitFor(() => expect(fetchMetrics).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('trend-range-toggle')).toBeInTheDocument();

    // A resource that has returned nothing yet does not inherit the picker.
    rerender(<CloudRightSizingEvidence {...props} fullRecommendation={{ id: 'rec-2', account_id: 'acct-1', resource_id: 'vol-0def' }} />);
    await waitFor(() => expect(fetchMetrics).toHaveBeenCalledTimes(3));
    await waitFor(() => expect(screen.queryByRole('progressbar')).not.toBeInTheDocument());
    expect(screen.queryByTestId('trend-range-toggle')).not.toBeInTheDocument();
  });

  it('does not refetch, or clear, when a parent hands down a fresh but equal object', async () => {
    fetchMetrics.mockResolvedValue(metricRows);
    const props = { recommendation: { current_instance_type: 'm5.large' }, ruleName: 'aws_ec2_underutilized' };
    const { rerender } = render(<CloudRightSizingEvidence {...props} fullRecommendation={{ ...fullRecommendation }} />);
    await screen.findByText('Resource Monitoring (7d)');

    rerender(<CloudRightSizingEvidence {...props} fullRecommendation={{ ...fullRecommendation }} />);
    rerender(<CloudRightSizingEvidence {...props} fullRecommendation={{ ...fullRecommendation }} />);
    expect(fetchMetrics).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Resource Monitoring (7d)')).toBeInTheDocument();
  });
});
