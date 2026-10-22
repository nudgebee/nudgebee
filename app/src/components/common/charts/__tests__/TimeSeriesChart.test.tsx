import * as React from 'react';
import { render, screen } from '@testing-library/react';
import { TimeSeriesChart, type TimeSeriesChartProps } from '../TimeSeriesChart';

// Capture the Chart.js options the wrapper hands to the canvas primitives.
let lastOptions: any;
jest.mock('../BarChart', () => (props: any) => {
  lastOptions = props.options;
  return null;
});
jest.mock('../SeriesChart', () => (props: any) => {
  lastOptions = props.options;
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
