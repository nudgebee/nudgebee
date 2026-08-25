jest.mock('@shared/charts/LineCharts', () => ({
  __esModule: true,
  default: ({ 'data-testid': testId }: { 'data-testid'?: string }) => <div data-testid={testId ?? 'line-chart'}>LineChart</div>,
}));

jest.mock('@shared/charts/BarChart', () => ({
  __esModule: true,
  default: ({ 'data-testid': testId }: { 'data-testid'?: string }) => <div data-testid={testId ?? 'bar-chart'}>BarChart</div>,
}));

jest.mock('@shared/charts/DoughnutChart', () => ({
  __esModule: true,
  default: ({ 'data-testid': testId }: { 'data-testid'?: string }) => <div data-testid={testId ?? 'doughnut-chart'}>DoughnutChart</div>,
}));

import { render, screen } from '@testing-library/react';
import Chart from '@ui/Chart';

describe('Chart namespace', () => {
  it('exports Line component', () => {
    expect(Chart.Line).toBeDefined();
  });

  it('exports Bar component', () => {
    expect(Chart.Bar).toBeDefined();
  });

  it('exports Doughnut component', () => {
    expect(Chart.Doughnut).toBeDefined();
  });
});

describe('Chart.Line', () => {
  it('renders without crashing', () => {
    render(<Chart.Line data={[]} labels={[]} data-testid='line-chart' />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });
});

describe('Chart.Bar', () => {
  it('renders without crashing', () => {
    render(<Chart.Bar data={[]} labels={[]} data-testid='bar-chart' />);
    expect(screen.getByTestId('bar-chart')).toBeInTheDocument();
  });
});

describe('Chart.Doughnut', () => {
  it('renders without crashing', () => {
    render(<Chart.Doughnut values={[]} labels={[]} onItemClick={undefined} data-testid='doughnut-chart' />);
    expect(screen.getByTestId('doughnut-chart')).toBeInTheDocument();
  });
});
