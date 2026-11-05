import React from 'react';
import { render, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockBarChart = jest.fn();

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getk8ClusterTrendData: jest.fn(),
    getMetrices: jest.fn(),
  },
}));

jest.mock('@mui/material', () => ({ Box: ({ children }) => children }));
jest.mock('@shared/Title', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/charts/BarChart', () => ({
  __esModule: true,
  default: (props) => {
    mockBarChart(props);
    return null;
  },
}));
jest.mock('@shared/charts/LineCharts', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/widgets/ChartSwitcher', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/FilterDropdown', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/ListingLayout', () => {
  const passthrough = ({ children }) => children;
  passthrough.Toolbar = passthrough;
  passthrough.Body = passthrough;
  return { ListingLayout: passthrough };
});
jest.mock('@lib/datetime', () => ({
  getLastSixMonths: () => new Date(0),
  getDateStringFromDateUnit: (timestamp) => `t${timestamp}`,
}));
jest.mock('src/utils/colors', () => ({ ds: { space: { 0: 0, 4: 4, 5: 5 }, text: { title: 'title' } } }));

import kuberneteApi from '@api1/kubernetes';
import OptimizeRecommendationCharts from '@components/cloudaccount/OptimizeRecommendationCharts';

describe('OptimizeRecommendationCharts network series', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    kuberneteApi.getk8ClusterTrendData.mockResolvedValue({ data: { cloudaccount_k8s_aggregate: [] } });
    kuberneteApi.getMetrices.mockResolvedValue({
      data: {
        cloud_resource_metrics_groupings: [
          { metric: 'networkReceiveBytes', avg_value: 0, timestamp: 1 },
          { metric: 'networkTransferBytes', avg_value: 2 * 1024 ** 3, timestamp: 2 },
          { metric: 'networkReceiveBytes', avg_value: 3 * 1024 ** 3, timestamp: 3 },
          { metric: 'networkReceiveBytes', avg_value: null, timestamp: 4 },
          { metric: 'networkTransferBytes', avg_value: undefined, timestamp: 5 },
        ],
      },
    });
  });

  it('passes numeric GB values to the charts and preserves zero points', async () => {
    render(<OptimizeRecommendationCharts accountId='account-1' />);

    await waitFor(() => {
      expect(mockBarChart.mock.calls.some(([props]) => props.chartLabel?.[0] === 'Ingress (GB)' && props.data.length === 3)).toBe(true);
    });

    const propsFor = (label) =>
      mockBarChart.mock.calls
        .map(([props]) => props)
        .filter((props) => props.chartLabel?.[0] === label)
        .at(-1);
    const ingress = propsFor('Ingress (GB)');
    const egress = propsFor('Egress (GB)');

    expect(ingress.data).toEqual([0, 3, null]);
    expect(ingress.labels).toEqual(['t1', 't3', 't4']);
    expect(egress.data).toEqual([2, null]);
    expect(egress.labels).toEqual(['t2', 't5']);
    expect([...ingress.data, ...egress.data].every((value) => value === null || Number.isFinite(value))).toBe(true);
  });
});
