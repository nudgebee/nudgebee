import React from 'react';
import { render, screen } from '@testing-library/react';
import { ToolCharts } from '@components/llm/common/ToolDetails';

// A step that saved a chart with its result (metadata.charts, as the conversation
// query returns it) shows that chart in Tool Details, ahead of the raw response.
// AgentChart is real (its spec validation decides chart vs nothing); only the
// canvas-backed chart primitive is stubbed.

jest.mock('@shared/viewers/MarkDowns', () => () => null);
jest.mock('@shared/tables/CustomTable', () => () => null);
jest.mock('@components/k8s/common/KubernetesTable', () => () => null);
jest.mock('@components/k8s/common/logTableMapper', () => ({ mapToTableData: () => [] }));
jest.mock('@components/recommendations/security/KubernetesSecurityDetails', () => () => null);
jest.mock('@ui/DiffViewer', () => ({ DiffViewer: () => null }));
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    TimeSeries: ({ labels }) => <div data-testid='chart-timeseries'>{labels.join(',')}</div>,
    Doughnut: () => <div data-testid='chart-doughnut' />,
  },
}));

const spec = {
  type: 'line',
  title: 'container_memory_working_set_bytes vs thresholds (UTC)',
  labels: ['Oct 17 02:04', 'Oct 17 04:57', 'Oct 17 07:49'],
  series: [
    { key: 'container_memory_working_set_bytes', data: [243, 255, 261] },
    { key: '100% hard limit', data: [300, 300, 300] },
  ],
  format: 'bytes',
};

describe('ToolCharts', () => {
  it('renders each saved chart from metadata stored as a JSON string', () => {
    render(<ToolCharts metadata={JSON.stringify({ charts: { chart_6a62ec20fb53: spec }, db_queries: 0 })} />);
    expect(screen.getByTestId('tool-details-charts')).toBeInTheDocument();
    expect(screen.getByTestId('chart-timeseries')).toHaveTextContent('Oct 17 02:04,Oct 17 04:57,Oct 17 07:49');
  });

  it('renders from already-parsed metadata', () => {
    render(<ToolCharts metadata={{ charts: { a: spec } }} />);
    expect(screen.getAllByTestId('chart-timeseries')).toHaveLength(1);
  });

  it('shows charts in ref order, which a tool uses to put its lead chart first', () => {
    const at = (label) => ({ ...spec, labels: [label, `${label} 2`, `${label} 3`] });
    render(<ToolCharts metadata={{ charts: { chart_1bbb: at('normal view'), chart_0aaa: at('now view') } }} />);
    const charts = screen.getAllByTestId('chart-timeseries');
    expect(charts[0]).toHaveTextContent('now view');
    expect(charts[1]).toHaveTextContent('normal view');
  });

  it.each([
    ['no metadata', undefined],
    ['metadata without charts', { db_queries: 2 }],
    ['unparseable metadata', '{not json'],
    ['empty charts', { charts: {} }],
    ['only charts that cannot be drawn', { charts: { chart_0aaa: {}, chart_1bbb: { type: 'radar' } } }],
  ])('renders nothing for %s', (_, metadata) => {
    const { container } = render(<ToolCharts metadata={metadata} />);
    expect(container).toBeEmptyDOMElement();
  });
});
