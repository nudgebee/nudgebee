// The dependency form offers every relationship the server accepts, explains
// which need a node declared outside any account, and lets an endpoint name a
// kind of the tenant's own.

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import EditManualDependencyDialog from '../EditManualDependencyDialog';
import apiKnowledgeGraph from '@api1/knowledge-graph';

jest.mock('@api1/knowledge-graph', () => ({
  __esModule: true,
  default: { getCloudAccounts: jest.fn(), getNode: jest.fn(), createManualDependency: jest.fn(), resolveManualDependency: jest.fn() },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));
jest.mock('../KgNodePicker', () => ({ __esModule: true, default: () => null }));

const SCHEMA = [
  {
    cloud_provider: 'external',
    kinds: [
      { specific_type: 'Database', node_type: 'Database', key_fields: [], properties: [] },
      { specific_type: 'NetworkSwitch', node_type: 'CloudResource', key_fields: [], properties: [], free_properties: true },
    ],
  },
];

const ROW = {
  id: 5,
  source_node_type: 'NetworkSwitch',
  source_name: 'sw-core-01',
  dest_node_type: 'CloudResource',
  dest_name: 'rack-a',
  relationship_type: 'CONNECTED_TO',
};

beforeEach(() => {
  jest.clearAllMocks();
  apiKnowledgeGraph.getCloudAccounts.mockResolvedValue({ data: { data: { cloud_accounts: { rows: [] } } } });
});

const renderDialog = (props = {}) => render(<EditManualDependencyDialog open schema={SCHEMA} onClose={jest.fn()} onSaved={jest.fn()} {...props} />);

it('offers all nine relationship types', async () => {
  renderDialog({ row: ROW });
  await waitFor(() => expect(apiKnowledgeGraph.getCloudAccounts).toHaveBeenCalled());
  // The relationship picker shows the stored type; open it to list the rest.
  fireEvent.click(screen.getByText('CONNECTED_TO'));
  for (const rel of ['CALLS', 'PUBLISHES_TO', 'SUBSCRIBES_TO', 'DEPENDS_ON', 'RUNS_ON', 'HOSTED_ON', 'ROUTES_TO', 'BELONGS_TO']) {
    expect(screen.getByText(rel)).toBeInTheDocument();
  }
});

it('shows an endpoint of a custom kind and the CloudResource category as their own node types', async () => {
  renderDialog({ row: ROW });
  await waitFor(() => expect(apiKnowledgeGraph.getCloudAccounts).toHaveBeenCalled());
  expect(screen.getByText('NetworkSwitch (your kind)')).toBeInTheDocument();
  expect(screen.getByText('CloudResource (other)')).toBeInTheDocument();
});

// #41145: a declaration from a node to itself resolves cleanly and draws a
// self-loop, so the form refuses it rather than letting the server answer.
const SELF_ROW = {
  id: 7,
  source_node_type: 'Workload',
  source_name: 'app-dev',
  source_namespace: 'nudgebee',
  dest_node_type: 'Workload',
  dest_name: 'app-dev',
  dest_namespace: 'nudgebee',
  relationship_type: 'CALLS',
};

it('refuses a declaration whose source and destination are the same node', async () => {
  renderDialog({ row: SELF_ROW });
  await waitFor(() => expect(apiKnowledgeGraph.getCloudAccounts).toHaveBeenCalled());

  expect(screen.getByText('Source and destination are the same node')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();

  // Naming a different destination clears it.
  fireEvent.change(screen.getAllByDisplayValue('app-dev')[1], { target: { value: 'app-test' } });
  await waitFor(() => expect(screen.queryByText('Source and destination are the same node')).not.toBeInTheDocument());
  expect(screen.getByRole('button', { name: 'Save' })).not.toBeDisabled();
});

it('does not warn on a half-filled form, where both sides start out blank', async () => {
  renderDialog();
  await waitFor(() => expect(apiKnowledgeGraph.getCloudAccounts).toHaveBeenCalled());

  expect(screen.queryByText('Source and destination are the same node')).not.toBeInTheDocument();
});
