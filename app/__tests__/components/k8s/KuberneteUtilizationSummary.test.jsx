import React from 'react';
import { render, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockBarChart = jest.fn();

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getClusterMetrices2: jest.fn(),
  },
}));

jest.mock('@mui/material', () => {
  const ReactActual = jest.requireActual('react');
  const passthrough = ({ children }) => ReactActual.createElement('div', null, children);
  // Grid carries the IntersectionObserver ref, so it must render a real element.
  const Grid = ReactActual.forwardRef(({ children }, ref) => ReactActual.createElement('div', { ref }, children));
  return { Box: passthrough, Typography: passthrough, Grid };
});
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    Bar: (props) => {
      mockBarChart(props);
      return null;
    },
    Line: () => null,
  },
}));
jest.mock('@ui/Card', () => ({ __esModule: true, default: ({ children }) => children }));
jest.mock('@shared/widgets/ChartSwitcher', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/FilterDropdown', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/common/Heading', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/ListingLayout', () => {
  const passthrough = ({ children }) => children;
  passthrough.Toolbar = passthrough;
  passthrough.Body = passthrough;
  return { ListingLayout: passthrough };
});
jest.mock('@lib/datetime', () => ({
  getLast30Days: () => new Date(0),
  getDateStringFromDateUnit: (timestamp) => `t${timestamp}`,
}));

import kuberneteApi from '@api1/kubernetes';
import KuberneteUtilizationSummary from '@components/k8s/KuberneteUtilizationSummary';

describe('KuberneteUtilizationSummary CPU and memory series', () => {
  const originalObserver = global.IntersectionObserver;

  beforeAll(() => {
    // Report every observed element as visible so the lazy CPU/memory fetch runs.
    global.IntersectionObserver = class {
      constructor(callback) {
        this.callback = callback;
      }
      observe() {
        this.callback([{ isIntersecting: true }]);
      }
      disconnect() {}
    };
  });

  afterAll(() => {
    global.IntersectionObserver = originalObserver;
  });

  beforeEach(() => {
    jest.clearAllMocks();
    kuberneteApi.getClusterMetrices2.mockImplementation(({ metric }) =>
      Promise.resolve({
        data: {
          cloud_resource_metrics_groupings: metric.includes('cpu')
            ? [
                { timestamp: 1, avg_cpu_used_node: 0, total_cpu_allocatable: 1234.5 },
                { timestamp: 1, avg_memory_used_node: 0, total_memory_allocatable: 2048 * 1024 ** 3 },
                { timestamp: 2, avg_cpu_used_node: null, total_cpu_allocatable: '8' },
                { timestamp: 2, avg_memory_used_node: null, total_memory_allocatable: undefined },
              ]
            : [
                { metric: 'networkReceiveBytes', avg_value: 0, timestamp: 1 },
                { metric: 'networkReceiveBytes', avg_value: null, timestamp: 2 },
                { metric: 'networkTransferBytes', avg_value: 2048 * 1024 ** 3, timestamp: 1 },
                { metric: 'networkTransferBytes', avg_value: undefined, timestamp: 2 },
              ],
        },
      })
    );
  });

  it('passes raw numbers (0 kept, >=1000 unformatted, missing as null) to the CPU and memory charts', async () => {
    render(<KuberneteUtilizationSummary accountId='account-1' />);

    const propsFor = (label) =>
      mockBarChart.mock.calls
        .map(([props]) => props)
        .filter((props) => props.chartLabel?.[0] === label)
        .at(-1);

    await waitFor(() => {
      expect(propsFor('Avg CPU')?.data[0]).toHaveLength(2);
    });

    const cpu = propsFor('Avg CPU');
    const mem = propsFor('Avg Mem');

    expect(cpu.data).toEqual([
      [0, null],
      [1234.5, 8],
    ]);
    expect(mem.data).toEqual([
      [0, null],
      [2048, null],
    ]);
    expect(cpu.labels).toEqual(['t1', 't2']);
    expect(mem.labels).toEqual(['t1', 't2']);
    expect([...cpu.data.flat(), ...mem.data.flat()].every((value) => value === null || Number.isFinite(value))).toBe(true);
  });

  it('passes raw GB numbers to the network charts and maps missing values to null instead of 0', async () => {
    render(<KuberneteUtilizationSummary accountId='account-1' />);

    const propsFor = (label) =>
      mockBarChart.mock.calls
        .map(([props]) => props)
        .filter((props) => props.chartLabel?.[0] === label)
        .at(-1);

    await waitFor(() => {
      expect(propsFor('Ingress')?.data).toHaveLength(2);
    });

    expect(propsFor('Ingress').data).toEqual([0, null]);
    expect(propsFor('Egress').data).toEqual([2048, null]);
  });
});
