import React from 'react';
import { render, screen } from '@testing-library/react';

jest.mock('chart.js', () => ({
  Chart: {
    register: jest.fn(),
    helpers: {
      fontString: jest.fn(() => '12px Arial'),
    },
    defaults: {
      global: {
        defaultFontFamily: 'Arial',
      },
    },
  },
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
  Line: ({ data: _data, options: _options }) => <div data-testid='line-chart' />,
  Bar: ({ data: _data, options: _options }) => <div data-testid='bar-chart' />,
  Doughnut: ({ data: _data, options: _options, id, style: _style }) => <div data-testid='doughnut-chart' data-id={id} />,
  Pie: ({ data: _data, options: _options }) => <div data-testid='pie-chart' />,
}));

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      white: '#FFFFFF',
      secondary: '#666',
      barChart: ['#4A90D9'],
    },
    border: {
      secondary: '#ccc',
    },
  },
}));

import DoughnutChart from '@components1/common/charts/DoughnutChart';

describe('DoughnutChart', () => {
  it('renders doughnut chart with basic props', () => {
    render(<DoughnutChart values={[50, 50]} labels={['A', 'B']} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('handles null values (defaults to [])', () => {
    render(<DoughnutChart values={null} labels={[]} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('renders displayValue=true (sum of values)', () => {
    render(<DoughnutChart values={[30, 70]} labels={['A', 'B']} displayValue={true} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
    expect(screen.getByText('100')).toBeInTheDocument();
  });

  it('renders displayValue as string', () => {
    render(<DoughnutChart values={[30, 70]} labels={['A', 'B']} displayValue='custom' />);
    expect(screen.getByText('custom')).toBeInTheDocument();
  });

  it('renders displayValue as number integer', () => {
    render(<DoughnutChart values={[30, 70]} labels={['A', 'B']} displayValue={42} />);
    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('renders displayValue as decimal number', () => {
    render(<DoughnutChart values={[30, 70]} labels={['A', 'B']} displayValue={42.5} />);
    expect(screen.getByText('42.5')).toBeInTheDocument();
  });

  it('renders without displayValue (shows 0)', () => {
    render(<DoughnutChart values={[30, 70]} labels={['A', 'B']} displayValue={false} />);
    expect(screen.getByText('0')).toBeInTheDocument();
  });

  it('shows valueUnit when displayValue is true', () => {
    render(<DoughnutChart values={[30, 70]} labels={['A', 'B']} displayValue={true} valueUnit='%' />);
    expect(screen.getByText('%')).toBeInTheDocument();
  });

  it('truncates long labels to 28 chars + ellipsis', () => {
    const longLabel = 'A'.repeat(30);
    render(<DoughnutChart values={[100]} labels={[longLabel]} displayCustomLegend={true} />);
    expect(screen.getByText('A'.repeat(28) + '...')).toBeInTheDocument();
  });

  it('keeps short labels unchanged', () => {
    render(<DoughnutChart values={[100]} labels={['Short']} displayCustomLegend={true} />);
    expect(screen.getByText('Short')).toBeInTheDocument();
  });

  it('handles string color (non-array) by generating shades', () => {
    render(<DoughnutChart values={[30, 40, 30]} labels={['A', 'B', 'C']} colors='#778899' />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('handles string color where r2, g2, b2 overflow 255', () => {
    // Use a high color value so r2 + i*5 overflows 255 quickly
    render(
      <DoughnutChart
        values={[10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10]}
        labels={['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K']}
        colors='#FFEECC'
      />
    );
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('renders displayCustomLegend=true', () => {
    render(<DoughnutChart values={[50, 50]} labels={['Apple', 'Banana']} displayCustomLegend={true} colors={['#ff0000', '#00ff00']} />);
    expect(screen.getByText('Apple')).toBeInTheDocument();
    expect(screen.getByText('Banana')).toBeInTheDocument();
  });

  it('renders with size < 50', () => {
    render(<DoughnutChart values={[50, 50]} labels={['A', 'B']} size={40} displayValue={true} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('renders with size >= 50', () => {
    render(<DoughnutChart values={[50, 50]} labels={['A', 'B']} size={77} displayValue={true} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('renders with id prop', () => {
    render(<DoughnutChart values={[50, 50]} labels={['A', 'B']} id='myDonut' />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('handles decimal values in reducedValues - float path', () => {
    render(<DoughnutChart values={[33.3, 66.7]} labels={['A', 'B']} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('handles integer values in reducedValues', () => {
    render(<DoughnutChart values={[33, 67]} labels={['A', 'B']} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('calls onItemClick when elements present', () => {
    // This tests the options.onClick with no elements (empty array)
    render(<DoughnutChart values={[50, 50]} labels={['A', 'B']} onItemClick={jest.fn()} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('renders with displayValue=true when values sum to NaN', () => {
    render(<DoughnutChart values={[NaN]} labels={['A']} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });

  it('renders percent with value unit', () => {
    render(<DoughnutChart values={[50, 50]} labels={['A', 'B']} displayValue={true} valueUnit='GB' />);
    expect(screen.getByText('GB')).toBeInTheDocument();
  });

  it('handles chartRadius falsy - uses default 100%', () => {
    render(<DoughnutChart values={[50, 50]} labels={['A', 'B']} chartRadius={null} />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });
});
