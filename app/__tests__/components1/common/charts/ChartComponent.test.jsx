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
  Bar: ({ data: _data, options: _options }) => <div data-testid='bar-chart' />,
  Doughnut: ({ data: _data, options: _options }) => <div data-testid='doughnut-chart' />,
  Pie: ({ data: _data, options: _options }) => <div data-testid='pie-chart' />,
}));

import ChartComponent from '@components1/common/charts/ChartComponent';

const sampleData = {
  labels: ['A', 'B', 'C'],
  datasets: [{ label: 'Dataset', data: [1, 2, 3] }],
};

describe('ChartComponent', () => {
  it('renders shimmer when loading=true', () => {
    const { container } = render(<ChartComponent type='bar' data={sampleData} loading={true} />);
    expect(container.querySelector('.shimmer')).toBeInTheDocument();
  });

  it('renders Bar chart when type=bar and loading=false', () => {
    render(<ChartComponent type='bar' data={sampleData} loading={false} />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });

  it('renders Pie chart when type=pie and loading=false', () => {
    render(<ChartComponent type='pie' data={sampleData} loading={false} />);
    expect(screen.getByTestId('pie-chart')).toBeInTheDocument();
  });

  it('renders Line chart when type=line and loading=false', () => {
    render(<ChartComponent type='line' data={sampleData} loading={false} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('uses custom maxHeight when provided', () => {
    const { container } = render(<ChartComponent type='bar' data={sampleData} loading={true} maxHeight={400} />);
    const shimmer = container.querySelector('.shimmer');
    expect(shimmer).toBeInTheDocument();
    expect(shimmer.style.maxHeight).toBe('400px');
  });

  it('uses default maxHeight when not provided', () => {
    const { container } = render(<ChartComponent type='bar' data={sampleData} loading={true} />);
    const shimmer = container.querySelector('.shimmer');
    expect(shimmer.style.maxHeight).toBe('200px');
  });

  it('passes options to chart component', () => {
    const options = { responsive: true };
    render(<ChartComponent type='bar' data={sampleData} loading={false} options={options} />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });
});
