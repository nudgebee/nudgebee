import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: {
    metricsQuery: jest.fn(),
  },
}));

// Source consumes `useCloudMetricsQueryPanel` (a hook) instead of the old QueryPanel component.
// We mock the hook so tests can simulate user picking valid / missing-X params via buttons that
// invoke the captured `onChange` callback.
jest.mock('@components/cloudaccount/cloud-metrics/CloudMetricsQueryPanel', () => ({
  __esModule: true,
  useCloudMetricsQueryPanel: ({ provider, accountId, onChange }: any) => ({
    primaryFilters: (
      <>
        <div data-testid='qp-provider'>{provider}</div>
        <div data-testid='qp-account'>{accountId}</div>
        <button
          data-testid='qp-emit-valid'
          onClick={() =>
            onChange({
              region: 'us-east-1',
              resourceIds: ['i-12345'],
              resourceType: 'AWS::EC2::Instance',
              metricNames: ['CPUUtilization'],
              statistics: ['Average'],
              serviceName: 'ec2',
            })
          }
        >
          Emit Valid
        </button>
        <button data-testid='qp-emit-no-region' onClick={() => onChange({ region: '', resourceIds: [], metricNames: [], statistics: [] })}>
          Emit No Region
        </button>
        <button
          data-testid='qp-emit-no-resources'
          onClick={() => onChange({ region: 'us-east-1', resourceIds: [], metricNames: ['CPUUtilization'], statistics: ['Average'] })}
        >
          Emit No Resources
        </button>
        <button
          data-testid='qp-emit-no-metrics'
          onClick={() => onChange({ region: 'us-east-1', resourceIds: ['i-1'], metricNames: [], statistics: ['Average'] })}
        >
          Emit No Metrics
        </button>
      </>
    ),
    secondaryFilters: null,
  }),
}));

jest.mock('@utils/colors');

jest.mock('@utils/common', () => ({
  formatMetricName: (name: string) => name,
}));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: ({ onChange }: any) => (
    <>
      <button
        data-testid='date-range-shortcut'
        onClick={() => onChange({ selection: { startTime: 1000, endTime: 2000, shortcutClickTime: 60_000 } })}
      >
        1m
      </button>
      <button data-testid='date-range-absolute' onClick={() => onChange({ selection: { startTime: 1000, endTime: 2000, shortcutClickTime: 0 } })}>
        Absolute
      </button>
    </>
  ),
}));

jest.mock('@ui/Button', () => ({
  __esModule: true,
  Button: ({ children, onClick, disabled, loading }: any) => (
    <button data-testid={`btn-${typeof children === 'string' ? children : 'icon'}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Banner', () => ({
  __esModule: true,
  Banner: ({ message }: any) => <div data-testid='banner'>{message}</div>,
}));

jest.mock('@ui/EmptyState', () => ({
  __esModule: true,
  EmptyState: ({ title, description }: any) => (
    <div data-testid='empty-state'>
      <div>{title}</div>
      <div>{description}</div>
    </div>
  ),
}));

jest.mock('@ui/Skeleton', () => ({
  __esModule: true,
  Skeleton: ({ ariaLabel }: any) => <div data-testid='skeleton' aria-label={ariaLabel} />,
}));

jest.mock('@ui/WidgetCard', () => ({
  __esModule: true,
  default: ({ children }: any) => <div data-testid='widget-card'>{children}</div>,
}));

jest.mock('@ui/Chart', () => {
  const Chart: any = () => null;
  Chart.Line = ({ chartTitle, dataset, labels }: any) => (
    <div data-testid={`chart-${chartTitle}`}>
      <div data-testid={`chart-labels-${chartTitle}`}>{(labels || []).join('|')}</div>
      <div data-testid={`chart-datasets-${chartTitle}`}>{(dataset || []).map((d: any) => `${d.label}:${d.data.join(',')}`).join(';')}</div>
    </div>
  );
  return { __esModule: true, default: Chart };
});

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout: any = ({ children, id }: any) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions }: any) => (
    <div data-testid='toolbar'>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }: any) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout };
});

import CloudMetricsViewer from '@components/cloudaccount/cloud-metrics/CloudMetricsViewer';

const observability = require('@api1/observability').default;

const sampleMetricsResponse = {
  data: {
    data: {
      metrics_list: {
        results: [
          {
            payload: [
              {
                metric: { name: 'CPUUtilization', resource_id: 'i-12345' },
                timestamps: [1000, 2000, 3000],
                values: [10, 20, 30],
              },
              {
                metric: { name: 'CPUUtilization', resource_id: 'i-67890' },
                timestamps: [1000, 2000, 3000],
                values: [50, 60, 70],
              },
              {
                metric: { name: 'NetworkIn', resource_id: 'i-12345' },
                timestamps: [1000, 2000],
                values: [1024, 2048],
              },
            ],
          },
        ],
      },
    },
  },
};

describe('CloudMetricsViewer (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    observability.metricsQuery.mockResolvedValue(sampleMetricsResponse);
  });

  it('passes provider + accountId through to query panel', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    expect(screen.getByTestId('qp-provider')).toHaveTextContent('AWS');
    expect(screen.getByTestId('qp-account')).toHaveTextContent('acc-1');
  });

  it('shows initial empty state before params emitted', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    expect(screen.getByText(/Select a service, region, and resource/)).toBeInTheDocument();
  });

  it('does not fetch on mount until params emitted', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    await act(async () => {});
    expect(observability.metricsQuery).not.toHaveBeenCalled();
  });

  it('shows region-missing error when Run Query pressed without region', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);

    fireEvent.click(screen.getByTestId('qp-emit-no-region'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Please select a region')).toBeInTheDocument());
    expect(observability.metricsQuery).not.toHaveBeenCalled();
  });

  it('shows resource-missing error when no resourceIds', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);

    fireEvent.click(screen.getByTestId('qp-emit-no-resources'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Please select at least one resource')).toBeInTheDocument());
    expect(observability.metricsQuery).not.toHaveBeenCalled();
  });

  it('shows metric-missing error when no metricNames', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);

    fireEvent.click(screen.getByTestId('qp-emit-no-metrics'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Please select at least one metric')).toBeInTheDocument());
    expect(observability.metricsQuery).not.toHaveBeenCalled();
  });

  it('fetches with composed request payload when Run Query pressed', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);

    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.metricsQuery).toHaveBeenCalled());
    const payload = observability.metricsQuery.mock.calls[0][0];
    expect(payload).toMatchObject({
      account_id: 'acc-1',
      metric_provider: 'aws_cloudwatch',
      metric_provider_source: 'user',
      instant: false,
      request: {
        service_name: 'ec2',
        region: 'us-east-1',
        resource_ids: ['i-12345'],
        resource_type: 'AWS::EC2::Instance',
        metric_names: ['CPUUtilization'],
        statistics: ['Average'],
      },
    });
  });

  it('renders one chart per metric, grouping datasets by resource_id', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);

    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByTestId('chart-CPUUtilization (%)')).toBeInTheDocument());
    expect(screen.getByTestId('chart-NetworkIn')).toBeInTheDocument();

    const cpuDatasets = screen.getByTestId('chart-datasets-CPUUtilization (%)').textContent;
    expect(cpuDatasets).toContain('i-12345:10,20,30');
    expect(cpuDatasets).toContain('i-67890:50,60,70');

    const netDatasets = screen.getByTestId('chart-datasets-NetworkIn').textContent;
    expect(netDatasets).toContain('i-12345:1024,2048');
  });

  it('appends unit label to chartTitle for Percent and Count metrics only', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);

    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByTestId('chart-CPUUtilization (%)')).toBeInTheDocument());
    expect(screen.queryByTestId('chart-NetworkIn (Bytes)')).not.toBeInTheDocument();
    expect(screen.getByTestId('chart-NetworkIn')).toBeInTheDocument();
  });

  it('aligns timestamps across datasets, filling missing with 0', async () => {
    observability.metricsQuery.mockResolvedValue({
      data: {
        data: {
          metrics_list: {
            results: [
              {
                payload: [
                  {
                    metric: { name: 'CPUUtilization', resource_id: 'i-A' },
                    timestamps: [1000, 2000, 3000],
                    values: [10, 20, 30],
                  },
                  {
                    metric: { name: 'CPUUtilization', resource_id: 'i-B' },
                    timestamps: [2000, 3000],
                    values: [50, 60],
                  },
                ],
              },
            ],
          },
        },
      },
    });

    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByTestId('chart-CPUUtilization (%)')).toBeInTheDocument());

    const datasets = screen.getByTestId('chart-datasets-CPUUtilization (%)').textContent;
    expect(datasets).toContain('i-A:10,20,30');
    expect(datasets).toContain('i-B:0,50,60');
  });

  it('parses result-level error from results array (no charts rendered)', async () => {
    observability.metricsQuery.mockResolvedValue({
      data: { data: { metrics_list: { results: [{ error: 'Permission denied for namespace' }] } } },
    });

    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.metricsQuery).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByTestId(/^chart-/)).not.toBeInTheDocument());
  });

  it('reaches catch block on structured rejection (clears charts)', async () => {
    observability.metricsQuery.mockRejectedValue({
      response: { data: { errors: [{ message: 'Quota exceeded' }] } },
    });

    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.metricsQuery).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByTestId(/^chart-/)).not.toBeInTheDocument());
  });

  it('calls API on Run Query with valid params (rejection-path coverage)', async () => {
    observability.metricsQuery.mockRejectedValue(new Error('Network down'));

    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.metricsQuery).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByTestId(/^chart-/)).not.toBeInTheDocument());
  });

  it('auto-fetches when date range changes after valid params emitted', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    observability.metricsQuery.mockClear();

    fireEvent.click(screen.getByTestId('date-range-absolute'));

    await waitFor(() => expect(observability.metricsQuery).toHaveBeenCalled());
    const payload = observability.metricsQuery.mock.calls[0][0];
    expect(payload.start_time).toBe(1000);
    expect(payload.end_time).toBe(2000);
  });

  it('does not auto-fetch on date change before valid params are present', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-no-region'));
    observability.metricsQuery.mockClear();

    fireEvent.click(screen.getByTestId('date-range-absolute'));

    await act(async () => {});
    expect(observability.metricsQuery).not.toHaveBeenCalled();
  });

  it('shortcut date sets start_time = now - delta, end_time = now', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    observability.metricsQuery.mockClear();

    fireEvent.click(screen.getByTestId('date-range-shortcut'));

    await waitFor(() => expect(observability.metricsQuery).toHaveBeenCalled());
    const payload = observability.metricsQuery.mock.calls[0][0];
    expect(payload.end_time - payload.start_time).toBe(60_000);
  });

  it('Run Query button disabled while loading', async () => {
    let resolveFn: any;
    observability.metricsQuery.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByTestId('btn-Run Query')).toBeDisabled());

    await act(async () => {
      resolveFn(sampleMetricsResponse);
    });

    await waitFor(() => expect(screen.getByTestId('btn-Run Query')).not.toBeDisabled());
  });

  it('refetches when Run Query clicked again after charts already rendered', async () => {
    render(<CloudMetricsViewer accountId='acc-1' provider='AWS' />);
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));
    await waitFor(() => expect(screen.getByTestId('chart-CPUUtilization (%)')).toBeInTheDocument());

    observability.metricsQuery.mockClear();
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.metricsQuery).toHaveBeenCalled());
  });
});
