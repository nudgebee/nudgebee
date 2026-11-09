// The declare-node form decides a node's identity, so these pin what reaches
// the backend: only the fields the type declares, never a submit with a
// compulsory key field missing, and — when the resource is already collected —
// a way to use that node instead of declaring a second one.

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import EditManualNodeDialog from '../EditManualNodeDialog';
import apiKnowledgeGraph from '@api1/knowledge-graph';

jest.mock('@api1/knowledge-graph', () => ({
  __esModule: true,
  default: { createManualNode: jest.fn(), updateManualNode: jest.fn(), getFilterOptions: jest.fn() },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

const nameField = { name: 'name', label: 'Name', required: true };
const SCHEMA = [
  {
    cloud_account_id: 'acct-1',
    account_name: 'Prod AWS',
    account_number: '123456789012',
    cloud_provider: 'AWS',
    kinds: [
      {
        specific_type: 'EC2Instance',
        node_type: 'ComputeInstance',
        key_fields: [
          { name: 'name', label: 'Name', required: true },
          { name: 'region', label: 'Region', required: true },
          { name: 'network', label: 'Network', required: true, pick_node_type: 'VPC' },
        ],
        properties: [{ name: 'instance_type' }],
      },
    ],
  },
  {
    cloud_account_id: 'k8s-1',
    account_name: 'prod-cluster',
    cloud_provider: 'K8s',
    kinds: [
      {
        specific_type: 'KubernetesDeployment',
        node_type: 'Workload',
        key_fields: [
          { name: 'name', label: 'Name', required: true },
          { name: 'namespace', label: 'Namespace', required: true },
        ],
        properties: [{ name: 'annotations', shape: 'map' }, { name: 'container_images', shape: 'list' }, { name: 'replicas' }],
      },
    ],
  },
  {
    cloud_provider: 'external',
    kinds: [
      {
        specific_type: 'Database',
        node_type: 'Database',
        key_fields: [nameField, { name: 'namespace', label: 'Namespace' }],
        properties: [{ name: 'engine' }],
      },
    ],
  },
];

const renderDialog = (props = {}) => render(<EditManualNodeDialog open schema={SCHEMA} onClose={jest.fn()} onSaved={jest.fn()} {...props} />);

beforeEach(() => {
  jest.clearAllMocks();
  apiKnowledgeGraph.getFilterOptions.mockResolvedValue({ data: { data: { kg_get_filter_options: { data: { node_keys: [], node_ids: [] } } } } });
});

it('keeps Declare disabled until a type and its compulsory fields are filled', () => {
  renderDialog();
  expect(screen.getByRole('button', { name: 'Declare' })).toBeDisabled();
});

it('declares a node outside connected accounts from a dependency prefill', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({ data: { data: { kg_create_manual_node: { data: { id: 1, kg_node_id: 'n1' } } } } });
  const onSaved = jest.fn();
  renderDialog({ initial: { node_type: 'Database', name: 'billing-db', namespace: 'finance' }, onSaved });

  // Outside any account no collector can merge the node — the form says so.
  expect(screen.getByText(/can never be matched by a collector/)).toBeInTheDocument();
  const declare = screen.getByRole('button', { name: 'Declare' });
  expect(declare).toBeEnabled();
  fireEvent.click(declare);

  await waitFor(() =>
    expect(apiKnowledgeGraph.createManualNode).toHaveBeenCalledWith({
      name: 'billing-db',
      node_type: 'Database',
      namespace: 'finance',
      description: '',
      properties: {},
      labels: {},
    })
  );
  expect(onSaved).toHaveBeenCalledWith({ id: 1, kg_node_id: 'n1' });
});

it('blocks submit again when a compulsory field is cleared', () => {
  renderDialog({ initial: { node_type: 'Database', name: 'billing-db' } });
  fireEvent.change(screen.getByTestId('manual-node-name'), { target: { value: '   ' } });
  expect(screen.getByRole('button', { name: 'Declare' })).toBeDisabled();
});

it('offers the collected node when the resource is already in the graph', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({
    data: { errors: [{ message: 'already in the graph', extensions: { upstream: { status: 409, body: { node_id: 'collected-1' } } } }] },
  });
  const onUseExisting = jest.fn();
  const onSaved = jest.fn();
  renderDialog({ initial: { node_type: 'Database', name: 'billing-db' }, onUseExisting, onSaved });

  fireEvent.click(screen.getByRole('button', { name: 'Declare' }));
  await screen.findByText('Already in the graph');
  expect(onSaved).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole('button', { name: 'Use existing node' }));
  expect(onUseExisting).toHaveBeenCalledWith('collected-1');
});

it('edits only the details — identity fields are locked and not sent', async () => {
  apiKnowledgeGraph.updateManualNode.mockResolvedValue({ data: { data: { kg_update_manual_node: { data: { id: 7 } } } } });
  const row = {
    id: 7,
    cloud_account_id: 'acct-1',
    specific_type: 'EC2Instance',
    node_type: 'ComputeInstance',
    name: 'web-1',
    region: 'us-east-1',
    network_node_id: 'vpc-node',
    description: 'old',
    properties: {},
    labels: {},
    status: 'active',
  };
  renderDialog({ row });

  expect(screen.getByTestId('manual-node-name')).toBeDisabled();
  expect(screen.getByTestId('manual-node-region')).toBeDisabled();

  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() =>
    expect(apiKnowledgeGraph.updateManualNode).toHaveBeenCalledWith({ id: 7, node: { description: 'old', properties: {}, labels: {} } })
  );
});

it('shows a Kubernetes declaration with its namespace as a locked key field', () => {
  renderDialog({
    row: {
      id: 9,
      cloud_account_id: 'k8s-1',
      cloud_provider: 'K8s',
      specific_type: 'KubernetesDeployment',
      node_type: 'Workload',
      name: 'checkout',
      namespace: 'payments',
      properties: {},
      labels: {},
      status: 'active',
    },
  });
  expect(screen.getByTestId('manual-node-name')).toHaveValue('checkout');
  expect(screen.getByTestId('manual-node-namespace')).toHaveValue('payments');
  expect(screen.getByTestId('manual-node-namespace')).toBeDisabled();
  expect(screen.queryByTestId('manual-node-region')).not.toBeInTheDocument();
});

it('edits map and list properties in the shape the collector stores them', async () => {
  apiKnowledgeGraph.updateManualNode.mockResolvedValue({ data: { data: { kg_update_manual_node: { data: { id: 9 } } } } });
  renderDialog({
    row: {
      id: 9,
      cloud_account_id: 'k8s-1',
      cloud_provider: 'K8s',
      specific_type: 'KubernetesDeployment',
      node_type: 'Workload',
      name: 'checkout',
      namespace: 'payments',
      description: '',
      properties: { annotations: { team: 'payments' }, container_images: ['checkout:1.4'], replicas: 3 },
      labels: {},
      status: 'active',
    },
  });

  // Annotations are key/value rows like labels, not one text box.
  expect(screen.getByText('annotations')).toBeInTheDocument();
  expect(screen.getByDisplayValue('team')).toBeInTheDocument();
  expect(screen.getByText('Separate values with commas')).toBeInTheDocument();

  fireEvent.change(screen.getByDisplayValue('checkout:1.4'), { target: { value: 'checkout:1.5, envoy:1.30 ,' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add entry' }));
  const keys = screen.getAllByPlaceholderText('key');
  const values = screen.getAllByPlaceholderText('value');
  fireEvent.change(keys[keys.length - 1], { target: { value: 'tier' } });
  fireEvent.change(values[values.length - 1], { target: { value: 'gold' } });

  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() =>
    expect(apiKnowledgeGraph.updateManualNode).toHaveBeenCalledWith({
      id: 9,
      node: {
        description: '',
        properties: { annotations: { team: 'payments', tier: 'gold' }, container_images: ['checkout:1.5', 'envoy:1.30'], replicas: 3 },
        labels: {},
      },
    })
  );
});
