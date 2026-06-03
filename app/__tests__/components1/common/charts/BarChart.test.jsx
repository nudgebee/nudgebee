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
  Line: ({ data: _data, options: _options }) => <div data-testid='line-chart' />,
  Bar: ({ data: _data, options: _options, plugins: _plugins, id, style: _style }) => <div data-testid='bar-chart' data-id={id} />,
  Doughnut: ({ data: _data, options: _options }) => <div data-testid='doughnut-chart' />,
  Pie: ({ data: _data, options: _options }) => <div data-testid='pie-chart' />,
}));

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      barChart: ['#4A90D9', '#7B68EE'],
      white: '#FFFFFF',
      secondary: '#666',
    },
    border: {
      secondary: '#ccc',
    },
  },
}));

import BarChart from '@components1/common/charts/BarChart';

describe('BarChart', () => {
  it('renders bar chart with default props', () => {
    render(<BarChart />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('renders shimmer when loading=true', () => {
    const { container } = render(<BarChart loading={true} />);
    expect(container.querySelector('.shimmer')).toBeInTheDocument();
  });

  it('renders bar chart when loading=false', () => {
    render(<BarChart loading={false} />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('handles null data by converting to [[]]', () => {
    render(<BarChart data={null} labels={['A', 'B']} />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('handles empty data array by converting to [[]]', () => {
    render(<BarChart data={[]} labels={['A', 'B']} />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('handles string colors by wrapping in array', () => {
    render(<BarChart data={[[1, 2, 3]]} labels={['A', 'B', 'C']} colors='#FF0000' />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('handles string chartLabel by wrapping in array', () => {
    render(<BarChart data={[[1, 2, 3]]} labels={['A', 'B', 'C']} chartLabel='My Chart' />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('handles flat (non-nested) data array by wrapping in array', () => {
    render(<BarChart data={[1, 2, 3]} labels={['A', 'B', 'C']} chartLabel='My Chart' />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('uses custom dataset when provided', () => {
    const dataset = [{ label: 'Custom', data: [1, 2, 3], backgroundColor: '#ff0000' }];
    render(<BarChart data={[[1, 2, 3]]} labels={['A', 'B', 'C']} dataset={dataset} />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('uses chart data when dataset is empty', () => {
    render(<BarChart data={[[1, 2, 3]]} labels={['A', 'B', 'C']} dataset={[]} chartLabel={['Series 1']} colors={['#FF0000']} />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('sets chartTitle in options when provided', () => {
    render(<BarChart data={[[1, 2, 3]]} labels={['A', 'B', 'C']} chartTitle='My Title' />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('renders with an id prop', () => {
    render(<BarChart data={[[1, 2, 3]]} labels={['A', 'B', 'C']} id='myChart' />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('handles multiple datasets', () => {
    render(
      <BarChart
        data={[
          [1, 2, 3],
          [4, 5, 6],
        ]}
        labels={['A', 'B', 'C']}
        chartLabel={['Series 1', 'Series 2']}
        colors={['#FF0000', '#00FF00']}
      />
    );
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });
});
