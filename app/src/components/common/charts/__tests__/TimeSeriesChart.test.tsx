import * as React from 'react';
import { render, screen } from '@testing-library/react';
import { TimeSeriesChart, type TimeSeriesChartProps } from '../TimeSeriesChart';

// Capture the Chart.js options and datasets the wrapper hands to the canvas primitives.
let lastOptions: any;
let lastDataset: any[];
jest.mock('../BarChart', () => (props: any) => {
  lastOptions = props.options;
  return null;
});
jest.mock('../SeriesChart', () => (props: any) => {
  lastOptions = props.options;
  lastDataset = props.dataset;
  return null;
});

const toMillions = (v: number) => `${Math.round(v / 1_000_000)}M`;

function yAxis(props: Partial<TimeSeriesChartProps>) {
  render(<TimeSeriesChart labels={['Mon', 'Tue']} series={[{ key: 'Tokens', data: [100_000_000, 200_000_000] }]} {...props} />);
  const ticks = lastOptions.scales.y.ticks;
  return { tick: (v: number) => ticks.callback(v), precision: ticks.precision };
}

describe('TimeSeriesChart y-axis ticks', () => {
  it('uses the compact formatter for an integer axis (200000000 → 200M)', () => {
    const { tick, precision } = yAxis({ integerY: true, compactFormat: toMillions });
    expect(tick(200_000_000)).toBe('200M');
    expect(precision).toBe(0);
  });

  it('keeps plain whole numbers for an integer axis with no compact formatter', () => {
    const { tick, precision } = yAxis({ integerY: true, shape: 'line' });
    expect(tick(1234.6)).toBe('1235');
    expect(precision).toBe(0);
  });

  it('defaults to compact currency when neither is set', () => {
    const { tick, precision } = yAxis({});
    expect(tick(2_000)).toMatch(/^\$/);
    expect(precision).toBeUndefined();
  });
});

describe('TimeSeriesChart drill hit-test', () => {
  const onSelect = jest.fn();
  beforeEach(() => onSelect.mockClear());

  function barChart() {
    render(<TimeSeriesChart labels={['Mon', 'Tue']} series={[{ key: 'Cost', data: [0, 100] }]} shape='bar' onSelectPoint={onSelect} />);
    return lastOptions.onClick;
  }

  it('drills when a bar is actually hit (intersect), not on empty canvas', () => {
    const onClick = barChart();
    // Empty space: Chart.js returns no intersecting element → no drill.
    const chart = { getElementsAtEventForMode: () => [] };
    onClick({}, [], chart);
    expect(onSelect).not.toHaveBeenCalled();
    // Pointer over the Tue bar → that column's element is returned → drill.
    const onBar = { getElementsAtEventForMode: () => [{ index: 1 }] };
    onClick({}, [], onBar);
    expect(onSelect).toHaveBeenCalledWith('Tue', 1);
  });

  it('does not lean on the els Chart.js passes (ignores snap-to-nearest)', () => {
    const onClick = barChart();
    // els is non-empty (the loose global interaction), but no real bar hit → no drill.
    onClick({}, [{ index: 0 }], { getElementsAtEventForMode: () => [] });
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('area/line drills only on/under the filled column, not the empty band above it', () => {
    render(<TimeSeriesChart labels={['Mon', 'Tue']} series={[{ key: 'Cost', data: [10, 40] }]} shape='area' onSelectPoint={onSelect} />);
    const onClick = lastOptions.onClick;
    // Fake scales: x maps pixel→index, y maps a stack total of 40 to pixel 50 (its top).
    const chart = {
      chartArea: { left: 0, right: 100, top: 0, bottom: 200 },
      isDatasetVisible: () => true,
      scales: { x: { getValueForPixel: () => 1 }, y: { getPixelForValue: () => 50 } },
    };
    // Click above the fill (y=20 < top 50) → ignored.
    onClick({ x: 80, y: 20 }, [], chart);
    expect(onSelect).not.toHaveBeenCalled();
    // Click inside the fill (y=120 ≥ top 50) → drill.
    onClick({ x: 80, y: 120 }, [], chart);
    expect(onSelect).toHaveBeenCalledWith('Tue', 1);
  });
});

describe('TimeSeriesChart legend totals', () => {
  const series = [{ key: 'Tokens', data: [9_000_000, 6_302_648] }];
  const full = (v: number) => v.toLocaleString('en-US');

  it('shows the legend formatter total when given, full value on hover', () => {
    render(<TimeSeriesChart labels={['Mon', 'Tue']} series={series} format={full} legendFormat={toMillions} />);
    const total = screen.getByText('15M');
    expect(total).toHaveAttribute('title', '15,302,648');
    expect(screen.queryByText('15,302,648')).toBeNull();
  });

  it('keeps the full formatter when no legend formatter is given', () => {
    render(<TimeSeriesChart labels={['Mon', 'Tue']} series={series} format={full} compactFormat={toMillions} />);
    expect(screen.getByText('15,302,648')).toBeInTheDocument();
  });

  it("shows each series' latest point when legendValue is latest", () => {
    render(<TimeSeriesChart labels={['Mon', 'Tue']} series={series} format={full} legendValue='latest' />);
    expect(screen.getByText('6,302,648')).toBeInTheDocument();
    expect(screen.queryByText('15,302,648')).toBeNull();
  });
});

describe('TimeSeriesChart series style and legend value', () => {
  const lineProps: TimeSeriesChartProps = {
    labels: ['a', 'b', 'c'],
    shape: 'line',
    format: (v) => `v${v}`,
    series: [
      { key: 'mem', data: [5, 6, 7] },
      { key: 'limit', data: [10, 10, 10] },
    ],
  };

  it('shows the latest value instead of the total when asked', () => {
    render(<TimeSeriesChart {...lineProps} legendValue='last' />);
    expect(screen.getByText('v7')).toBeInTheDocument();
    expect(screen.getByText('v10')).toBeInTheDocument();
    expect(screen.queryByText('v18')).not.toBeInTheDocument();
  });

  it('keeps the total by default', () => {
    render(<TimeSeriesChart {...lineProps} />);
    expect(screen.getByText('v18')).toBeInTheDocument();
  });

  it('draws a reference line thin and dotted, without a fill', () => {
    render(<TimeSeriesChart {...lineProps} referenceKeys={['limit']} />);
    const [mem, limit] = lastDataset;
    expect(mem.borderDash).toBeUndefined();
    expect(mem.fill).toBe('origin');
    expect(limit).toMatchObject({ fill: false, borderWidth: 1.5, borderDash: [2, 3] });
  });
});
