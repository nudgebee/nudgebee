import React from 'react';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import { chipsLegendPlugin } from '../legendChips';

// jsdom has no canvas: capture what the chart would be drawn with instead.
const mockLine = jest.fn();
jest.mock('chart.js', () => ({
  Chart: { register: jest.fn() },
  CategoryScale: jest.fn(),
  LinearScale: jest.fn(),
  PointElement: jest.fn(),
  LineElement: jest.fn(),
  Title: jest.fn(),
  Tooltip: jest.fn(),
  Legend: jest.fn(),
  Colors: jest.fn(),
}));
jest.mock('react-chartjs-2', () => ({
  Line: (props) => {
    mockLine(props);
    return <div data-testid='line' />;
  },
  getElementAtEvent: jest.fn(),
}));

import LineCharts from '../LineCharts';

const dataset = [{ label: 'pod=cart (x)', data: [0.5, 1.5, 2] }];
const labels = ['10:00', '10:01', '10:02'];
const lastOptions = () =>
  mockLine.mock.calls
    .map(([props]) => props)
    .filter(Boolean)
    .at(-1).options;

const hoverTooltip = (options, raw) => {
  options.plugins.tooltip.external({
    chart: { canvas: { getBoundingClientRect: () => ({ left: 0, top: 0 }) } },
    tooltip: { opacity: 1, title: ['10:01'], caretX: 0, caretY: 0, dataPoints: [{ dataset: dataset[0], raw, parsed: { y: raw } }] },
  });
  return document.getElementById('chartjs-tooltip').textContent;
};

describe('LineCharts formatValue', () => {
  beforeEach(() => mockLine.mockClear());

  it('leaves the axis and tooltip as they were when not given', () => {
    render(<LineCharts dataset={dataset} labels={labels} timeAxis={false} />);
    const options = lastOptions();
    expect(options.scales.y.ticks).toBeUndefined();
    const text = hoverTooltip(options, 1.5);
    expect(text).toContain('Value: 1.50 x');
    expect(text).toContain('Max: 2.00 x');
  });

  it('formats the axis ticks, tooltip value and stats with it', () => {
    const formatValue = (value) => `${value} GiB`;
    render(<LineCharts dataset={dataset} labels={labels} timeAxis={false} formatValue={formatValue} />);
    const options = lastOptions();
    expect(options.scales.y.ticks.callback(0.5)).toBe('0.5 GiB');
    const text = hoverTooltip(options, 1.5);
    expect(text).toContain('Value: 1.5 GiB');
    expect(text).toContain('Max: 2 GiB');
    // The unit a label carries in parentheses is not appended a second time.
    expect(text).not.toContain('GiB x');
  });

  it('does not write into the caller’s scaleOptions', () => {
    const scaleOptions = { y: { display: true }, x: { grid: { display: true } } };
    render(<LineCharts dataset={dataset} labels={labels} timeAxis={false} scaleOptions={scaleOptions} formatValue={(v) => `${v} m`} />);
    expect(lastOptions().scales.y.ticks.callback(250)).toBe('250 m');
    expect(lastOptions().scales.y.display).toBe(true);
    expect(scaleOptions.y).toEqual({ display: true });
  });
});

describe('chipsLegendPlugin format', () => {
  const chart = {
    options: {
      plugins: { legend: { labels: { generateLabels: () => [{ datasetIndex: 0, text: 'pod=cart', fillStyle: '#000', hidden: false }] } } },
    },
    data: { datasets: [{ data: [0.5, 2] }] },
  };

  it('prints the stats through the formatter', () => {
    render(<div id='legend' />);
    chipsLegendPlugin('legend', { format: (value) => `${value} GiB` }).afterUpdate(chart);
    expect(screen.getByTestId('chart-legend-detail')).toHaveTextContent('Max2 GiB');
    expect(screen.getByTestId('chart-legend-detail')).toHaveTextContent('Min0.5 GiB');
  });

  it('keeps the unit suffix without one', () => {
    render(<div id='legend2' />);
    chipsLegendPlugin('legend2', { unit: 'cores' }).afterUpdate(chart);
    expect(screen.getByTestId('chart-legend-detail')).toHaveTextContent('Max2.00 cores');
  });
});
