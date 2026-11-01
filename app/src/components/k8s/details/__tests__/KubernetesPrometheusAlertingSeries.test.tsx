import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import KubernetesPrometheus from '../KubernetesPrometheus';

/*
 * An alert rule returns every object that matches it. When the alert's own series
 * is flagged, the chart draws only that one and counts the rest, until asked.
 */

const chartLine = jest.fn((_props: { dataset: { label: string }[] }) => null);

jest.mock('next/router', () => ({ useRouter: () => ({ query: {} }) }));
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: { Line: (props: { dataset: { label: string }[] }) => chartLine(props) },
}));
jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { consumePrometheusQueries: jest.fn() } }));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { createAiFeedback: jest.fn() } }));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));
jest.mock('@components/k8s/common/KubernetesTable', () => () => null);
jest.mock('@components/k8s/common/QueryModeSwitcher', () => () => null);
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => () => null);
jest.mock('@shared/widgets/UserHistory', () => () => null);
jest.mock('@shared/buttons/DownloadButton', () => () => null);

const series = (deployment: string, alerting?: boolean) => ({
  ...(alerting ? { alerting: true } : {}),
  metric: { namespace: 'shop', deployment },
  timestamps: [1790783700, 1790783760],
  values: ['1', '1'],
});

const evidence = (seriesList: unknown[]) => [{ metadata: { query: 'kube_deployment_spec_replicas' }, data: { series_list_result: seriesList } }];

const renderChart = (preparedEvidences: unknown[]) =>
  render(
    <KubernetesPrometheus
      accountId='acc-1'
      showDrilldown={false}
      chartView
      showExtraOptions={false}
      showQueryBox={false}
      showDateTime={false}
      preparedEvidences={preparedEvidences}
    />
  );

const drawnLabels = () => chartLine.mock.calls[chartLine.mock.calls.length - 1][0].dataset.map((d) => d.label.split('\n').pop());

beforeEach(() => {
  chartLine.mockClear();
});

it('draws only the alerting object and counts the others, until asked for all', () => {
  renderChart(evidence([series('orders', true), series('cart'), series('web')]));

  expect(drawnLabels()).toEqual(['deployment=orders']);
  expect(screen.getByText("2 other objects also matched this alert's query.")).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Show all' }));
  expect(drawnLabels()).toEqual(['deployment=orders', 'deployment=cart', 'deployment=web']);
  expect(screen.getByText("Showing all 3 objects that matched this alert's query.")).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Show only the alerting object' }));
  expect(drawnLabels()).toEqual(['deployment=orders']);
});

it('draws every series when none is flagged', () => {
  renderChart(evidence([series('orders'), series('cart')]));

  expect(drawnLabels()).toEqual(['deployment=orders', 'deployment=cart']);
  expect(screen.queryByText(/also matched this alert's query/)).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Show all' })).not.toBeInTheDocument();
});
