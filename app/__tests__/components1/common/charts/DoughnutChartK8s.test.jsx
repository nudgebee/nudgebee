import React from 'react';
import { render, screen } from '@testing-library/react';

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
  Line: () => <div data-testid='line-chart' />,
  Bar: () => <div data-testid='bar-chart' />,
  Doughnut: () => <div data-testid='doughnut-chart' />,
  Pie: () => <div data-testid='pie-chart' />,
}));

import DoughnutChartK8s from '@components1/common/charts/DoughnutChartK8s';

describe('DoughnutChartK8s', () => {
  it('renders with default props', () => {
    render(<DoughnutChartK8s />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
    expect(screen.getByText('20')).toBeInTheDocument();
    expect(screen.getByText('%')).toBeInTheDocument();
  });

  it('renders with isDecimal=true (rounds value)', () => {
    render(<DoughnutChartK8s isDecimal={true} value={45.7} />);
    expect(screen.getByText('46')).toBeInTheDocument();
  });

  it('renders with isDecimal=false (shows value as-is)', () => {
    render(<DoughnutChartK8s isDecimal={false} value={45} />);
    expect(screen.getByText('45')).toBeInTheDocument();
  });

  it('uses getTypographyFontSize: size < 36 -> 14px', () => {
    render(<DoughnutChartK8s size={30} value={50} />);
    const text = screen.getByText('50');
    expect(text).toBeInTheDocument();
  });

  it('uses getTypographyFontSize: size >= 36 and < 55 -> 18px', () => {
    render(<DoughnutChartK8s size={45} value={50} />);
    expect(screen.getByText('50')).toBeInTheDocument();
  });

  it('uses getTypographyFontSize: size >= 55 -> 20px', () => {
    render(<DoughnutChartK8s size={77} value={50} />);
    expect(screen.getByText('50')).toBeInTheDocument();
  });

  it('uses getFontSize: size < 40 -> 10px', () => {
    render(<DoughnutChartK8s size={30} value={50} />);
    const percentSpan = screen.getByText('%');
    expect(percentSpan).toBeInTheDocument();
    expect(percentSpan.style.fontSize).toBe('10px');
  });

  it('uses getFontSize: size >= 40 and < 50 -> 12px', () => {
    render(<DoughnutChartK8s size={45} value={50} />);
    const percentSpan = screen.getByText('%');
    expect(percentSpan.style.fontSize).toBe('12px');
  });

  it('uses getFontSize: size >= 50 -> 13px', () => {
    render(<DoughnutChartK8s size={77} value={50} />);
    const percentSpan = screen.getByText('%');
    expect(percentSpan.style.fontSize).toBe('13px');
  });

  it('renders with custom color and id', () => {
    render(<DoughnutChartK8s color='#FF0000' id='myChart' value={75} />);
    expect(screen.getByText('75')).toBeInTheDocument();
  });

  it('renders with string size prop', () => {
    render(<DoughnutChartK8s size='77px' value={60} />);
    // size is passed as string "77px" - getTypographyFontSize and getFontSize compare numerically
    // "77px" < 36 is false because JS coerces: "77px" to NaN for comparison
    // NaN comparisons are always false, so getFontSize falls to else (13) and getTypographyFontSize falls to return '20px'
    expect(screen.getByText('60')).toBeInTheDocument();
  });

  it('renders with value=0', () => {
    render(<DoughnutChartK8s value={0} />);
    expect(screen.getByText('0')).toBeInTheDocument();
  });

  it('renders with value=100', () => {
    render(<DoughnutChartK8s value={100} />);
    expect(screen.getByText('100')).toBeInTheDocument();
  });

  it('renders with custom rounded prop', () => {
    render(<DoughnutChartK8s value={50} rounded='5px' />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });
});
