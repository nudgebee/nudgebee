// #41498: a failed graph load must say the load failed — not "No nodes match your
// filters" — and leave Apply usable so the same filters can be retried.

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import KnowledgeGraph from '../KnowledgeGraph';
import apiKubernetes1 from '@api1/kubernetes1';
import { toast } from '@ui/Toast';

jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: { knowledgeGraphFilterOptions: jest.fn(), knowledgeGraph: jest.fn(), knowledgeGraphGetNode: jest.fn(), knowledgeGraphGetEdge: jest.fn() },
}));
jest.mock('@api1/home', () => ({ __esModule: true, default: { getCloudAccounts: jest.fn(() => Promise.resolve([])) } }));
jest.mock('@lib/auth', () => ({ getCurrentTenant: () => ({ id: 't1' }), isTenantAdmin: () => false }));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

// "key=value" + Enter adds a Label chip and reports the KG query built from it.
jest.mock('@components/k8s/common/LogQueryBuilderAutocomplete', () => {
  const MockQueryBuilder = ({ heading, queryItems, onQueryItemsChange, onQueryChange }) => (
    <input
      aria-label={`${heading || 'Label'} input`}
      onKeyDown={(e) => {
        if (e.key !== 'Enter') return;
        const [label, value] = e.target.value.split('=');
        const next = [...(queryItems || []), { id: `${label}-${value}`, label, operator: '=', value }];
        onQueryItemsChange(next);
        onQueryChange({ query: JSON.stringify(next.map((c) => ({ key: c.label, value: c.value }))) });
      }}
    />
  );
  return { __esModule: true, default: MockQueryBuilder, PropertyFilterHelp: () => null };
});

const EMPTY_GRAPH = { data: { data: { kg_get_complete_graph: { data: { nodes: [], edges: [] } } } } };
// What queryGraphQL resolves with when the gateway answers 502 (#40696).
const GATEWAY_502 = { data: { errors: [{ message: 'parse_failed' }] }, status: 502 };

const applyButton = () => screen.getByTestId('kg-apply-filters-btn');

beforeEach(() => {
  jest.clearAllMocks();
  apiKubernetes1.knowledgeGraphFilterOptions.mockResolvedValue({ data: { data: { kg_get_filter_options: { data: {} } } } });
  apiKubernetes1.knowledgeGraph.mockResolvedValue(EMPTY_GRAPH);
});

const applyLabelFilterThatFails = async (failure) => {
  render(<KnowledgeGraph />);
  await waitFor(() => expect(apiKubernetes1.knowledgeGraph).toHaveBeenCalledTimes(1));
  failure();
  fireEvent.keyDown(screen.getByLabelText('Label input'), { key: 'Enter', target: { value: 'app.kubernetes.io/name=grafana' } });
  fireEvent.click(applyButton());
  await screen.findByTestId('kg-load-error');
};

it.each([
  ['an error response', () => apiKubernetes1.knowledgeGraph.mockResolvedValueOnce(GATEWAY_502)],
  ['a network failure', () => apiKubernetes1.knowledgeGraph.mockRejectedValueOnce(new Error('Network Error'))],
])('%s shows the load error, not the empty-filter state', async (_, failure) => {
  await applyLabelFilterThatFails(failure);

  expect(screen.queryByTestId('kg-empty-filtered')).not.toBeInTheDocument();
  expect(toast.error).toHaveBeenCalledWith('Failed to load knowledge graph data.');
  expect(applyButton()).not.toBeDisabled();
});

it('Apply after a failed load retries the same filters, and a genuine empty result still says no nodes match', async () => {
  await applyLabelFilterThatFails(() => apiKubernetes1.knowledgeGraph.mockResolvedValueOnce(GATEWAY_502));
  const failedRequest = apiKubernetes1.knowledgeGraph.mock.calls.at(-1)[0];

  fireEvent.click(applyButton());

  await screen.findByTestId('kg-empty-filtered');
  expect(apiKubernetes1.knowledgeGraph.mock.calls.at(-1)[0]).toEqual(failedRequest);
  expect(screen.queryByTestId('kg-load-error')).not.toBeInTheDocument();
  expect(applyButton()).toBeDisabled();
});

it('Retry on the error state refetches the applied filters', async () => {
  await applyLabelFilterThatFails(() => apiKubernetes1.knowledgeGraph.mockRejectedValueOnce(new Error('Network Error')));
  const failedRequest = apiKubernetes1.knowledgeGraph.mock.calls.at(-1)[0];

  fireEvent.click(screen.getByTestId('kg-load-error-retry-btn'));

  await screen.findByTestId('kg-empty-filtered');
  expect(apiKubernetes1.knowledgeGraph.mock.calls.at(-1)[0]).toEqual(failedRequest);
});
