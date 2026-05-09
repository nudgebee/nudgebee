import React from 'react';
import { render, screen } from '@testing-library/react';
import MermaidChartJS, { MermaidChartJS as NamedMermaidChartJS } from '@components1/common/MermaidChartJS';

jest.mock('@components1/common/charts/ChartComponent', () => ({
  __esModule: true,
  default: ({ type, data }) => (
    <div data-testid='chart-component' data-type={type}>
      {data?.labels?.map((l, i) => (
        <span key={i} data-testid='chart-label'>
          {l}
        </span>
      ))}
    </div>
  ),
}));

const validLineMermaidCode = `
xychart-beta
title "Response Time"
x-axis "Time" [Jan, Feb, Mar]
y-axis "ms"
line "P99" [100, 200, 150]
`;

const validBarMermaidCode = `
xychart-beta
title "Error Count"
x-axis "Month" [Jan, Feb, Mar]
y-axis "Count"
bar "Errors" [5, 10, 7]
`;

const multipleDatasetsMermaidCode = `
xychart-beta
x-axis [Jan, Feb, Mar]
line "P50" [50, 60, 55]
line "P99" [100, 200, 150]
`;

describe('MermaidChartJS', () => {
  it('renders "Unable to parse chart data" when mermaidCode is empty string', () => {
    render(<MermaidChartJS mermaidCode='' />);
    expect(screen.getByText('Unable to parse chart data')).toBeInTheDocument();
  });

  it('renders "Unable to parse chart data" when mermaid code has no datasets', () => {
    render(<MermaidChartJS mermaidCode='xychart-beta\nx-axis [Jan, Feb]' />);
    expect(screen.getByText('Unable to parse chart data')).toBeInTheDocument();
  });

  it('renders ChartComponent for valid line chart code', () => {
    render(<MermaidChartJS mermaidCode={validLineMermaidCode} />);
    expect(screen.getByTestId('chart-component')).toBeInTheDocument();
  });

  it('renders ChartComponent for valid bar chart code', () => {
    render(<MermaidChartJS mermaidCode={validBarMermaidCode} />);
    expect(screen.getByTestId('chart-component')).toBeInTheDocument();
  });

  it('passes correct chart type "line" for line charts', () => {
    render(<MermaidChartJS mermaidCode={validLineMermaidCode} />);
    expect(screen.getByTestId('chart-component')).toHaveAttribute('data-type', 'line');
  });

  it('passes correct chart type "bar" for bar charts', () => {
    render(<MermaidChartJS mermaidCode={validBarMermaidCode} />);
    expect(screen.getByTestId('chart-component')).toHaveAttribute('data-type', 'bar');
  });

  it('renders x-axis labels in chart data', () => {
    render(<MermaidChartJS mermaidCode={validLineMermaidCode} />);
    const labels = screen.getAllByTestId('chart-label');
    expect(labels).toHaveLength(3);
    expect(labels[0]).toHaveTextContent('Jan');
    expect(labels[1]).toHaveTextContent('Feb');
    expect(labels[2]).toHaveTextContent('Mar');
  });

  it('renders chart for multiple datasets', () => {
    render(<MermaidChartJS mermaidCode={multipleDatasetsMermaidCode} />);
    expect(screen.getByTestId('chart-component')).toBeInTheDocument();
  });

  it('is also exported as named export', () => {
    render(<NamedMermaidChartJS mermaidCode={validLineMermaidCode} />);
    expect(screen.getByTestId('chart-component')).toBeInTheDocument();
  });

  it('renders "Unable to parse chart data" when xAxisData is empty', () => {
    const noXAxis = `
xychart-beta
line "Data" [1, 2, 3]
`;
    render(<MermaidChartJS mermaidCode={noXAxis} />);
    expect(screen.getByText('Unable to parse chart data')).toBeInTheDocument();
  });
});
