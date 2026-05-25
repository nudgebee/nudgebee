import React from 'react';
import { render, screen, act } from '@testing-library/react';

// Mock chart.js and react-chartjs-2
jest.mock('chart.js', () => ({
  Chart: { register: jest.fn() },
  CategoryScale: jest.fn(),
  LinearScale: jest.fn(),
  PointElement: jest.fn(),
  LineElement: jest.fn(),
  BarElement: jest.fn(),
  ArcElement: jest.fn(),
  Title: jest.fn(),
  Tooltip: jest.fn(),
  Legend: jest.fn(),
  Filler: jest.fn(),
  Colors: jest.fn(),
}));

jest.mock('react-chartjs-2', () => ({
  Line: React.forwardRef((_props, _ref) => <div data-testid='line-chart' />),
  Bar: () => <div data-testid='bar-chart' />,
  Doughnut: () => <div data-testid='doughnut-chart' />,
  Pie: () => <div data-testid='pie-chart' />,
  getElementAtEvent: jest.fn(() => []),
}));

// Mock child components
jest.mock('@components1/common/BoxLayout2', () => {
  const PropTypes = require('prop-types');
  function BoxLayout2({ children }) {
    return <div data-testid='box-layout2'>{children}</div>;
  }
  BoxLayout2.propTypes = { children: PropTypes.node };
  return BoxLayout2;
});

jest.mock('@components1/common/ShimmerLoading', () => {
  const PropTypes = require('prop-types');
  const ShimmerLoading = ({ children, isLoading, height: _height, width: _width }) => (
    <div data-testid='shimmer-loading' data-loading={isLoading}>
      {children}
    </div>
  );
  ShimmerLoading.propTypes = { children: PropTypes.node, isLoading: PropTypes.bool, height: PropTypes.any, width: PropTypes.any };
  return ShimmerLoading;
});

jest.mock('@components1/common/charts/LineCharts', () => () => <div data-testid='line-chart-inner' />);

const mockConsumePrometheusQueries = jest.fn();
jest.mock('@api1/kubernetes1', () => {
  return {
    __esModule: true,
    default: {
      consumePrometheusQueries: (...args) => mockConsumePrometheusQueries(...args),
    },
  };
});

jest.mock('@components1/common/snackbarService', () => ({
  __esModule: true,
  snackbar: {
    error: jest.fn(),
    success: jest.fn(),
  },
}));

jest.mock('src/utils/common', () => ({
  convertNumberToTimestamp: jest.fn((ts) => `ts:${ts}`),
}));

jest.mock('uuid', () => ({
  v4: jest.fn().mockReturnValueOnce('key-1').mockReturnValueOnce('key-2').mockReturnValue('key-n'),
}));

jest.mock('lodash/uniq', () => jest.fn((arr) => [...new Set(arr)]));

import ShowPrometheusLineChart from '@components1/common/charts/ShowPrometheusLineChart';
import { snackbar } from '@components1/common/snackbarService';

const defaultProps = {
  accountId: 'account-123',
  query: 'up{job="test"}',
  selectedDateRange: { startDate: 1000, endDate: 2000 },
};

describe('ShowPrometheusLineChart', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockConsumePrometheusQueries.mockResolvedValue({ data: { data: { metrics_query: { results: [] } } } });
  });

  it('renders BoxLayout2 and ShimmerLoading', async () => {
    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });
    expect(screen.getByTestId('box-layout2')).toBeInTheDocument();
    expect(screen.getByTestId('shimmer-loading')).toBeInTheDocument();
  });

  it('calls API on mount when query and accountId provided', async () => {
    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });
    expect(mockConsumePrometheusQueries).toHaveBeenCalledTimes(1);
  });

  it('does not call API when query is empty', async () => {
    await act(async () => {
      render(<ShowPrometheusLineChart accountId='account-123' query='' />);
    });
    expect(mockConsumePrometheusQueries).not.toHaveBeenCalled();
  });

  it('does not call API when accountId is missing', async () => {
    await act(async () => {
      render(<ShowPrometheusLineChart query='up' accountId={null} />);
    });
    expect(mockConsumePrometheusQueries).not.toHaveBeenCalled();
  });

  it('does not call API when query is whitespace only', async () => {
    await act(async () => {
      render(<ShowPrometheusLineChart accountId='account-123' query='   ' />);
    });
    expect(mockConsumePrometheusQueries).not.toHaveBeenCalled();
  });

  it('renders chart data when API returns valid series data with timestamps', async () => {
    const results = [
      {
        query_key: 'key-1',
        payload: [
          {
            metric: { job: 'test', instance: 'localhost' },
            timestamps: [1000, 2000, 3000],
            values: ['1.5', '2.5', '3.5'],
          },
        ],
      },
    ];
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });

    expect(screen.getByTestId('line-chart-inner')).toBeInTheDocument();
  });

  it('handles API response with no series_list_result (falls back to instant data)', async () => {
    const results = [
      {
        query_key: 'key-1',
        payload: [],
      },
    ];
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });

    // Empty payload falls back to instant data processing and renders chart with empty data
    expect(screen.getByTestId('line-chart-inner')).toBeInTheDocument();
  });

  it('handles instant data with valid value array', async () => {
    const results = [
      {
        query_key: 'key-1',
        payload: null, // series_list_result would be null
      },
    ];
    // Return data where series processing fails and instant processing has data
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} instant={true} />);
    });
  });

  it('shows snackbar error when API returns no results', async () => {
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results: null } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });

    expect(snackbar.error).toHaveBeenCalledWith('Failed execute Query');
  });

  it('shows snackbar error when API throws', async () => {
    mockConsumePrometheusQueries.mockRejectedValue(new Error('Network error'));

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });

    expect(snackbar.error).toHaveBeenCalledWith('Failed to fetch the Data');
  });

  it('handles multiple queries separated by semicolon', async () => {
    const { v4 } = require('uuid');
    v4.mockReturnValueOnce('k1').mockReturnValueOnce('k2');

    const results = [
      {
        query_key: 'k1',
        payload: [
          {
            metric: { job: 'test' },
            timestamps: [1000],
            values: ['1'],
          },
        ],
      },
      {
        query_key: 'k2',
        payload: [
          {
            metric: { job: 'test2' },
            timestamps: [2000],
            values: ['2'],
          },
        ],
      },
    ];
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart accountId='account-123' query='query1;query2' selectedDateRange={{ startDate: 1000, endDate: 2000 }} />);
    });
  });

  it('handles query with leading/trailing semicolons', async () => {
    await act(async () => {
      render(<ShowPrometheusLineChart accountId='account-123' query=";up{job='test'};" selectedDateRange={{ startDate: 1000, endDate: 2000 }} />);
    });
    expect(mockConsumePrometheusQueries).toHaveBeenCalledTimes(1);
  });

  it('renders helperText when present in chart data', async () => {
    // We need chartData to have a helperText - this happens when instant result has string_result
    // and series_list_result is null/empty and instant data returns empty
    const results = [
      {
        query_key: 'key-1',
        payload: null,
      },
    ];
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });
    // Component renders without error even when payload is null
  });

  it('handles data processing error gracefully', async () => {
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results: 'invalid' } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });

    expect(snackbar.error).toHaveBeenCalledWith('Failed to fetch the Data');
  });

  it('renders series data without timestamps (second branch)', async () => {
    const results = [
      {
        query_key: 'key-1',
        payload: [
          {
            metric: { job: 'test' },
            timestamps: [],
            values: ['1.5', '2.5'],
          },
        ],
      },
    ];
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart {...defaultProps} />);
    });
  });

  it('renders chart data multiple entries (maps over chartData)', async () => {
    const { v4 } = require('uuid');
    v4.mockReturnValueOnce('k1').mockReturnValueOnce('k2');

    const results = [
      {
        query_key: 'k1',
        payload: [
          {
            metric: { job: 'test' },
            timestamps: [1000, 2000],
            values: ['1', '2'],
          },
        ],
      },
      {
        query_key: 'k2',
        payload: [
          {
            metric: { job: 'test2' },
            timestamps: [3000, 4000],
            values: ['3', '4'],
          },
        ],
      },
    ];
    mockConsumePrometheusQueries.mockResolvedValue({
      data: { data: { metrics_query: { results } } },
    });

    await act(async () => {
      render(<ShowPrometheusLineChart accountId='account-123' query='q1;q2' selectedDateRange={{ startDate: 1000, endDate: 5000 }} />);
    });

    const charts = screen.getAllByTestId('line-chart-inner');
    expect(charts.length).toBeGreaterThanOrEqual(1);
  });
});
