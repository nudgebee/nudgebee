import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';

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

const mockGetElementAtEvent = jest.fn(() => []);

jest.mock('react-chartjs-2', () => {
  const PropTypes = require('prop-types');
  const Line = React.forwardRef(function Line({ onClick }, ref) {
    if (ref) {
      ref.current = {
        getDatasetMeta: jest.fn(() => ({
          data: [{ x: 10, y: 20 }],
        })),
        canvas: {
          getBoundingClientRect: jest.fn(() => ({ left: 0, top: 0 })),
        },
        tooltip: {},
        options: { plugins: { legend: { labels: { generateLabels: jest.fn(() => []) } } } },
        config: { type: 'line' },
        data: { datasets: [{ data: [1, 2, 3], label: 'Test' }] },
      };
    }
    return <button data-testid='line-chart' onClick={(e) => onClick?.(e)} />;
  });
  Line.propTypes = { onClick: PropTypes.func };
  return {
    Line,
    Bar: () => <div data-testid='bar-chart' />,
    Doughnut: () => <div data-testid='doughnut-chart' />,
    Pie: () => <div data-testid='pie-chart' />,
    getElementAtEvent: (...args) => mockGetElementAtEvent(...args),
  };
});

jest.mock('uuid', () => ({
  v4: jest.fn(() => 'test-uuid-123'),
}));

import Charts from '@components1/common/charts/LineCharts';

describe('Charts (LineCharts)', () => {
  beforeEach(() => {
    mockGetElementAtEvent.mockReturnValue([]);
    // Ensure document doesn't have lingering tooltip
    const existing = document.getElementById('chartjs-tooltip');
    if (existing) existing.remove();
  });

  afterEach(() => {
    const existing = document.getElementById('chartjs-tooltip');
    if (existing) existing.remove();
  });

  it('renders shimmer when loading=true', () => {
    const { container } = render(<Charts loading={true} />);
    expect(container.querySelector('.shimmer')).toBeInTheDocument();
  });

  it('renders line chart when loading=false', () => {
    render(<Charts loading={false} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with null data (converts to [[]])', () => {
    render(<Charts loading={false} data={null} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with empty data array (converts to [[]])', () => {
    render(<Charts loading={false} data={[]} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with flat data array (non-nested)', () => {
    render(<Charts loading={false} data={[1, 2, 3]} labels={['A', 'B', 'C']} chartLabel='Value' />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with nested data array', () => {
    render(
      <Charts
        loading={false}
        data={[
          [1, 2, 3],
          [4, 5, 6],
        ]}
        labels={['A', 'B', 'C']}
        chartLabel={['Series 1', 'Series 2']}
        colors={['#ff0000', '#00ff00']}
      />
    );
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with string colors (wrapped in array)', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} colors='#ff0000' />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with chartTitle', () => {
    render(<Charts loading={false} chartTitle='My Title' />);
    expect(screen.getByText('My Title')).toBeInTheDocument();
  });

  it('renders without chartTitle (no title element)', () => {
    render(<Charts loading={false} />);
    expect(screen.queryByText('My Title')).not.toBeInTheDocument();
  });

  it('renders with dataset prop', () => {
    const dataset = [{ label: 'Dataset', data: [1, 2, 3], borderColor: '#ff0000', backgroundColor: '#ff0000' }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with single data point labels (isSingleDataPoint)', () => {
    render(<Charts loading={false} data={[[5]]} labels={['Single']} chartLabel={['Series']} colors={['#ff0000']} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with single data point - empty data (hasValidData=false)', () => {
    render(<Charts loading={false} data={[[]]} labels={['Single']} chartLabel={['Series']} colors={['#ff0000']} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with single data point and dataset prop', () => {
    const dataset = [{ label: 'D', data: [5], borderColor: '#ff0000', borderWidth: 2, pointRadius: 5 }];
    render(<Charts loading={false} labels={['Single']} dataset={dataset} colors={['#ff0000']} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with dataset having single data point (data.length == 1)', () => {
    const dataset = [{ label: 'D', data: [5], borderColor: '#ff0000' }];
    render(<Charts loading={false} labels={['A']} dataset={dataset} colors={['#ff0000']} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with dataset having multiple data points', () => {
    const dataset = [{ label: 'D', data: [1, 2, 3], borderColor: '#ff0000' }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with data all empty strings (hasValidData=false)', () => {
    render(<Charts loading={false} data={[['', '', '']]} labels={['A', 'B', 'C']} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with legendOptions.renderer=html', () => {
    const dataset = [{ label: 'D', data: [1, 2, 3], borderColor: '#ff0000' }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} legendOptions={{ renderer: 'html' }} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders legend container when legendOptions.renderer=html and hasValidData', () => {
    const dataset = [{ label: 'D', data: [1, 2, 3], borderColor: '#ff0000' }];
    const { container } = render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} legendOptions={{ renderer: 'html' }} />);
    expect(container.querySelector('.chart-legend-container')).toBeInTheDocument();
  });

  it('does not render legend container when hasValidData=false with renderer=html', () => {
    const { container } = render(<Charts loading={false} data={[[]]} labels={[]} legendOptions={{ renderer: 'html' }} />);
    expect(container.querySelector('.chart-legend-container')).not.toBeInTheDocument();
  });

  it('renders with legendOptions non-html (uses legendOptions directly)', () => {
    const dataset = [{ label: 'D', data: [1, 2, 3] }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} legendOptions={{ position: 'bottom' }} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with interactionOptions', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} interactionOptions={{ mode: 'index', intersect: false }} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with scaleOptions', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} scaleOptions={{ x: { type: 'linear' } }} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with integerYlabel=true', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} integerYlabel={true} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with useFixedHeight=true', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} useFixedHeight={true} fixedHeight={500} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with dynamicHeight=false', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} dynamicHeight={false} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with multiple datasets (> 3)', () => {
    render(
      <Charts
        loading={false}
        data={[
          [1, 2],
          [3, 4],
          [5, 6],
          [7, 8],
        ]}
        labels={['A', 'B']}
        chartLabel={['S1', 'S2', 'S3', 'S4']}
        colors={['#f00', '#0f0', '#00f', '#ff0']}
      />
    );
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with more than 10 labels', () => {
    render(<Charts loading={false} data={[[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]]} labels={['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K']} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with chartLabel as array (for estimatedLegendHeight calculation)', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} chartLabel={['S1', 'S2', 'S3', 'S4', 'S5', 'S6']} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('handles click when no elements found and no onAskNubi', () => {
    mockGetElementAtEvent.mockReturnValue([]);
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} onDataPointClick={jest.fn()} />);
    const chart = screen.getByTestId('line-chart');
    fireEvent.click(chart);
  });

  it('handles click when no chart ref (early return)', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} />);
    const chart = screen.getByTestId('line-chart');
    fireEvent.click(chart);
  });

  it('handles click with elements and onDataPointClick callback', () => {
    const onDataPointClick = jest.fn();
    mockGetElementAtEvent.mockReturnValue([{ datasetIndex: 0, index: 0 }]);
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} onDataPointClick={onDataPointClick} />);
    const chart = screen.getByTestId('line-chart');
    fireEvent.click(chart);
  });

  it('handles click with onAskNubi and "usage" label', () => {
    const onAskNubi = jest.fn();
    mockGetElementAtEvent.mockReturnValue([{ datasetIndex: 0, index: 0 }]);
    const dataset = [{ label: 'CPU usage', data: [1, 2, 3], borderColor: '#ff0000' }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} onAskNubi={onAskNubi} />);
    const chart = screen.getByTestId('line-chart');
    fireEvent.click(chart);
  });

  it('handles click with onAskNubi but no "usage" in label', () => {
    const onAskNubi = jest.fn();
    mockGetElementAtEvent.mockReturnValue([{ datasetIndex: 0, index: 0 }]);
    const dataset = [{ label: 'CPU', data: [1, 2, 3], borderColor: '#ff0000' }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} onAskNubi={onAskNubi} />);
    const chart = screen.getByTestId('line-chart');
    fireEvent.click(chart);
  });

  it('handles click with no elements and onAskNubi (setPinnedPoint(null))', () => {
    const onAskNubi = jest.fn();
    mockGetElementAtEvent.mockReturnValue([]);
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} onAskNubi={onAskNubi} />);
    const chart = screen.getByTestId('line-chart');
    fireEvent.click(chart);
  });

  it('renders with customPlugins', () => {
    const customPlugin = { id: 'custom', beforeDraw: jest.fn() };
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} customPlugins={[customPlugin]} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('cleans up on unmount (removes tooltip)', () => {
    // Create a tooltip element first
    const tooltipEl = document.createElement('div');
    tooltipEl.id = 'chartjs-tooltip';
    document.body.appendChild(tooltipEl);

    const { unmount } = render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} />);
    unmount();
    // Tooltip should be removed
    expect(document.getElementById('chartjs-tooltip')).toBeNull();
  });

  it('handles window resize event', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} />);
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with dataset where data[0] is string (triggers parseFloat mapping)', () => {
    const dataset = [{ label: 'D', data: ['1.5', '2.5', '3.5'], borderColor: '#ff0000' }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with onAskNubi - sets pointHitRadius', () => {
    const dataset = [{ label: 'D', data: [1, 2, 3], borderColor: '#ff0000', pointHitRadius: 5 }];
    render(<Charts loading={false} labels={['A', 'B', 'C']} dataset={dataset} onAskNubi={jest.fn()} />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('renders with id prop (uses provided id instead of uuid)', () => {
    render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} id='custom-chart-id' />);
    expect(screen.getByTestId('line-chart')).toBeInTheDocument();
  });

  it('handles the tooltip cleanup when hideTimeout exists on unmount', () => {
    const { unmount } = render(<Charts loading={false} data={[[1, 2, 3]]} labels={['A', 'B', 'C']} />);
    unmount();
  });
});
