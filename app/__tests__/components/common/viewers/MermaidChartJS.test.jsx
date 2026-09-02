import { render, screen } from '@testing-library/react';
import MermaidChartJS from '@shared/viewers/MermaidChartJS';

jest.mock('@shared/charts/ChartComponent', () => ({
  __esModule: true,
  default: ({ type, data }) => (
    <div data-testid='chart' data-type={type}>
      {data?.labels?.join(',')}
    </div>
  ),
}));

jest.mock('@shared/ErrorBoundary', () => ({
  withErrorBoundary: (Component) => Component,
}));

const validMermaid = `xychart-beta
  title "Monthly Revenue"
  x-axis ["Jan", "Feb", "Mar"]
  y-axis "Revenue (USD)"
  line [100, 200, 150]`;

const barMermaid = `xychart-beta
  title "Sales"
  x-axis ["Q1", "Q2"]
  bar [500, 700]`;

describe('MermaidChartJS', () => {
  beforeEach(() => {
    jest.spyOn(console, 'warn').mockImplementation(() => {});
  });
  afterEach(() => {
    jest.restoreAllMocks();
  });

  describe('valid chart', () => {
    it('renders chart for valid mermaid xychart code', () => {
      render(<MermaidChartJS mermaidCode={validMermaid} />);
      expect(screen.getByTestId('chart')).toBeInTheDocument();
    });

    it('passes x-axis labels to chart', () => {
      render(<MermaidChartJS mermaidCode={validMermaid} />);
      expect(screen.getByTestId('chart')).toHaveTextContent('Jan');
    });

    it('defaults to line chart type', () => {
      render(<MermaidChartJS mermaidCode={validMermaid} />);
      expect(screen.getByTestId('chart')).toHaveAttribute('data-type', 'line');
    });

    it('uses bar chart type for bar keyword', () => {
      render(<MermaidChartJS mermaidCode={barMermaid} />);
      expect(screen.getByTestId('chart')).toHaveAttribute('data-type', 'bar');
    });
  });

  describe('invalid chart', () => {
    it('shows "Unable to parse chart data" for empty input', () => {
      render(<MermaidChartJS mermaidCode='' />);
      expect(screen.getByText('Unable to parse chart data')).toBeInTheDocument();
    });

    it('shows "Unable to parse chart data" for invalid syntax', () => {
      render(<MermaidChartJS mermaidCode='flowchart TD\n  A --> B' />);
      expect(screen.getByText('Unable to parse chart data')).toBeInTheDocument();
    });

    it('does not render chart component when parsing fails', () => {
      render(<MermaidChartJS mermaidCode='invalid' />);
      expect(screen.queryByTestId('chart')).not.toBeInTheDocument();
    });
  });
});
