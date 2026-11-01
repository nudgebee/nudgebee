import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import QueryMetrics from '../QueryMetrics';
import observability from '@api1/observability';
import apiAccount from '@api1/account';
import { snackbar } from '@ui/Toast';

/*
 * Switching the metrics provider is a fresh start: the query box clears along with
 * the results. The editor text and the builder's picks live inside QueryModeSwitcher,
 * so these tests render the real one, builder included (only CodeMirror is swapped
 * for a textarea), and check that what the editor shows is what Submit sends.
 */

const mockRouter: { isReady: boolean; query: Record<string, string> } = { isReady: true, query: {} };

jest.mock('next/router', () => ({ useRouter: () => mockRouter }));
jest.mock('@context/DataContext', () => ({ useData: () => ({ selectedCluster: null }) }));
jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: jest.fn() } }));
jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: jest.fn(), metricsList: jest.fn(), metricsLabelList: jest.fn(), getMetricsQuery: jest.fn() },
}));
jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: {} }));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: {} }));
jest.mock('@ui/Toast', () => {
  const api = { success: jest.fn(), error: jest.fn(), warning: jest.fn() };
  return { toast: api, snackbar: api };
});
jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null } }));
jest.mock('@components/k8s/common/KubernetesTable', () => () => null);
jest.mock('@components/k8s/KubernetesClusterSummary', () => ({ SummaryBlock: () => null }));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => () => null);
jest.mock('@shared/widgets/UserHistory', () => () => null);
jest.mock('@shared/buttons/DownloadButton', () => () => null);
jest.mock('@shared/icons/CloudIcon', () => () => null);
jest.mock('@hooks/useLlmAsyncPolling', () => ({
  useLlmAsyncPolling: () => ({ startPolling: jest.fn() }),
  extractQueryResultFromConversation: jest.fn(),
}));
jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ assistantName: 'Assistant' }) }));
jest.mock('@codemirror/view', () => ({ placeholder: () => ({}) }));
jest.mock('@codemirror/lint', () => ({ linter: () => ({}) }));
jest.mock('@prometheus-io/codemirror-promql', () => ({
  PromQLExtension: class {
    setComplete() {
      return this;
    }
    activateCompletion() {
      return this;
    }
    asExtension() {
      return {};
    }
  },
}));
jest.mock('@uiw/react-codemirror', () => {
  const react = jest.requireActual('react');
  return {
    __esModule: true,
    EditorView: { lineWrapping: {} },
    default: ({ value, onChange }: { value: string; onChange: (next: string) => void }) =>
      react.createElement('textarea', {
        'aria-label': 'Query editor',
        value,
        onChange: (e: { target: { value: string } }) => onChange(e.target.value),
      }),
  };
});

const metricsQuery = observability.metricsQuery as jest.Mock;
const getDefaultProvider = apiAccount.getDefaultProvider as jest.Mock;

const providers = (defaultProvider: string, others: string[]) => ({
  data: {
    data: {
      observability_get_default_provider: {
        provider: defaultProvider,
        available_providers: [defaultProvider, ...others].map((provider) => ({ provider })),
      },
    },
  },
});

// A fresh account id per test: the screen caches the provider list per account.
let accountSeq = 0;
const renderScreen = () => {
  accountSeq += 1;
  return render(<QueryMetrics accountId={`acc-${accountSeq}`} showDrilldown={false} chartView showExtraOptions showQueryBox />);
};

const editor = () => screen.getByLabelText('Query editor');
const submitButton = () => screen.getByRole('button', { name: 'Submit' });

// The button is disabled while a request is in flight, so waiting for it on both
// sides of the click covers the whole round trip.
const submit = async () => {
  await waitFor(() => expect(submitButton()).toBeEnabled());
  fireEvent.click(submitButton());
  await waitFor(() => expect(submitButton()).toBeEnabled());
};

const expectLastRequest = (query: string, provider: string) => {
  expect(metricsQuery).toHaveBeenCalled();
  const request = metricsQuery.mock.calls[metricsQuery.mock.calls.length - 1][0];
  expect(Object.values(request.queries)).toEqual([query]);
  expect(request.metric_provider).toBe(provider);
};

const switchProviderTo = async (label: RegExp) => {
  fireEvent.click(await screen.findByRole('button', { name: /Metrics Provider:/ }));
  // hidden: the menu fades in, and jsdom never finishes the transition that would
  // make its items count as visible.
  fireEvent.click(await screen.findByRole('menuitem', { name: label, hidden: true }));
};

beforeEach(() => {
  jest.clearAllMocks();
  mockRouter.query = {};
  metricsQuery.mockResolvedValue({ data: { data: { metrics_list: { results: [] } } } });
  (observability.metricsList as jest.Mock).mockResolvedValue({ data: { data: { metrics_list_names: [{ metric: 'node_load1' }] } } });
  (observability.metricsLabelList as jest.Mock).mockResolvedValue({ data: { data: { metrics_list_labels: [] } } });
  (observability.getMetricsQuery as jest.Mock).mockResolvedValue({ data: { data: { metrics_get_query: { results: {} } } } });
});

it('empties the query box on a provider switch, then runs what is typed against the new provider', async () => {
  getDefaultProvider.mockResolvedValue(providers('openobserve', ['prometheus']));
  renderScreen();
  await screen.findByRole('button', { name: /Metrics Provider:/ });

  fireEvent.change(editor(), { target: { value: 'up' } });
  await submit();
  expectLastRequest('up', 'openobserve');

  await switchProviderTo(/Prometheus/);
  await waitFor(() => expect(editor()).toHaveValue(''));

  // Nothing is shown, so nothing is sent, and the screen says why.
  metricsQuery.mockClear();
  await submit();
  expect(metricsQuery).not.toHaveBeenCalled();
  expect(snackbar.error).toHaveBeenCalledWith('Please enter a query before submitting');

  fireEvent.change(editor(), { target: { value: 'node_load1' } });
  await submit();
  expectLastRequest('node_load1', 'prometheus');
});

it('drops the metric picked in the Builder when the provider is switched', async () => {
  getDefaultProvider.mockResolvedValue(providers('openobserve', ['prometheus']));
  renderScreen();
  await screen.findByRole('button', { name: /Metrics Provider:/ });

  fireEvent.click(screen.getByRole('radio', { name: 'Builder' }));
  fireEvent.click(await screen.findByText('Select a Metric'));
  fireEvent.click(await screen.findByText('node_load1'));
  await submit();
  expectLastRequest('node_load1', 'openobserve');

  // The switch lands on the Code tab. The metric picked for the previous provider
  // must not come back as the query there.
  await switchProviderTo(/Prometheus/);
  await waitFor(() => expect(editor()).toHaveValue(''));

  metricsQuery.mockClear();
  await submit();
  expect(metricsQuery).not.toHaveBeenCalled();
});

it('keeps a query that arrived in the link when the account default is not Prometheus', async () => {
  // The screen starts on Prometheus and only then learns the account default. That
  // first resolve is not a switch, so it must not clear the query the link carried.
  mockRouter.query = { query: 'up' };
  getDefaultProvider.mockResolvedValue(providers('victoria-metrics', ['prometheus']));
  renderScreen();
  await screen.findByRole('button', { name: /Victoria Metrics/ });
  await waitFor(() => expect(editor()).toHaveValue('up'));

  metricsQuery.mockClear();
  await submit();
  expectLastRequest('up', 'victoria-metrics');
});
