// The declare-node form decides a node's identity, so these pin what reaches
// the backend: only the fields the type declares, never a submit with a
// compulsory key field missing, and — when the resource is already collected —
// a way to use that node instead of declaring a second one.

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import EditManualNodeDialog from '../EditManualNodeDialog';
import apiKnowledgeGraph from '@api1/knowledge-graph';

jest.mock('@api1/knowledge-graph', () => ({
  __esModule: true,
  default: {
    createManualNode: jest.fn(),
    updateManualNode: jest.fn(),
    getFilterOptions: jest.fn(),
    listNodeRoles: jest.fn(),
    setNodeRole: jest.fn(),
    removeNodeRole: jest.fn(),
  },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));
// Text pulls in the markdown viewer, whose ES-module dependency this test
// environment cannot load; the table only needs the value shown.
jest.mock('@shared/format/Text', () => ({ __esModule: true, default: ({ value }) => <span>{value}</span> }));

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

// Kinds of the tenant's own, outside any account: a NetworkSwitch under the
// CloudResource category, with free key/value properties.
const outsideKeyFields = [nameField, { name: 'namespace', label: 'Namespace' }];
const CUSTOM_KIND = {
  specific_type: 'NetworkSwitch',
  node_type: 'CloudResource',
  key_fields: outsideKeyFields,
  properties: [],
  free_properties: true,
};
const withOutside = (extra) => [SCHEMA[0], { ...SCHEMA[2], kinds: [...SCHEMA[2].kinds, CUSTOM_KIND], ...extra }];

// The dialog renders in a portal, so the picker is looked up on the document.
const openTypePicker = () => fireEvent.click(document.querySelector('#manual-node-type'));

const addProperty = (key, value) => {
  fireEvent.click(screen.getByRole('button', { name: 'Add property' }));
  const keys = screen.getAllByPlaceholderText('key');
  const values = screen.getAllByPlaceholderText('value');
  fireEvent.change(keys[keys.length - 1], { target: { value: key } });
  fireEvent.change(values[values.length - 1], { target: { value } });
};

it('declares a node of a custom kind with its category, its kind and free properties', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({ data: { data: { kg_create_manual_node: { data: { id: 2 } } } } });
  renderDialog({ schema: withOutside(), initial: { node_type: 'NetworkSwitch', name: 'sw-core-02' } });

  addProperty('site', 'blr-1');
  addProperty('vlans', '["10", "20"]');
  fireEvent.click(screen.getByRole('button', { name: 'Declare' }));

  await waitFor(() =>
    expect(apiKnowledgeGraph.createManualNode).toHaveBeenCalledWith({
      name: 'sw-core-02',
      node_type: 'CloudResource',
      specific_type: 'NetworkSwitch',
      description: '',
      properties: { site: 'blr-1', vlans: ['10', '20'] },
      labels: {},
    })
  );
});

it('keeps a built-in kind outside any account on the request it always had', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({ data: { data: { kg_create_manual_node: { data: { id: 3 } } } } });
  renderDialog({ schema: withOutside({ can_define_kinds: true }), initial: { node_type: 'Database', name: 'billing-db' } });
  fireEvent.click(screen.getByRole('button', { name: 'Declare' }));
  await waitFor(() => expect(apiKnowledgeGraph.createManualNode).toHaveBeenCalled());
  expect(apiKnowledgeGraph.createManualNode.mock.calls[0][0]).not.toHaveProperty('specific_type');
});

it('refuses a property the server reserves for a custom kind', () => {
  renderDialog({ schema: withOutside(), initial: { node_type: 'NetworkSwitch', name: 'sw-core-02' } });
  addProperty('region', 'us-east-1');
  expect(screen.getByText(/“region” cannot be a property/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Declare' })).toBeDisabled();
});

it('offers a new kind only to a caller who may define one', () => {
  const { unmount } = renderDialog({ schema: withOutside() });
  openTypePicker();
  expect(screen.getByText('Network Switch')).toBeInTheDocument();
  expect(screen.queryByText('New kind…')).not.toBeInTheDocument();
  unmount();

  renderDialog({ schema: withOutside({ can_define_kinds: true }) });
  openTypePicker();
  expect(screen.getByText('New kind…')).toBeInTheDocument();
});

it('introduces a new kind under the chosen category, checking its name first', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({ data: { data: { kg_create_manual_node: { data: { id: 4 } } } } });
  renderDialog({ schema: withOutside({ can_define_kinds: true }) });
  openTypePicker();
  fireEvent.click(screen.getByText('New kind…'));

  const kindName = screen.getByTestId('manual-node-kind-name');
  const declare = screen.getByRole('button', { name: 'Declare' });
  fireEvent.change(kindName, { target: { value: 'networkRouter' } });
  expect(screen.getByText(/Start with a capital letter/)).toBeInTheDocument();
  fireEvent.change(kindName, { target: { value: 'Database' } });
  expect(screen.getByText(/Database is a built-in type/)).toBeInTheDocument();
  fireEvent.change(kindName, { target: { value: 'NetworkSwitch' } });
  expect(screen.getByText(/already in the type list/)).toBeInTheDocument();
  fireEvent.change(kindName, { target: { value: 'NetworkRouter' } });
  fireEvent.change(screen.getByTestId('manual-node-name'), { target: { value: 'rt-edge-01' } });
  expect(declare).toBeEnabled();

  addProperty('model', 'ASR1001');
  fireEvent.click(declare);
  await waitFor(() =>
    expect(apiKnowledgeGraph.createManualNode).toHaveBeenCalledWith({
      name: 'rt-edge-01',
      node_type: 'CloudResource',
      specific_type: 'NetworkRouter',
      description: '',
      properties: { model: 'ASR1001' },
      labels: {},
    })
  );
});

it('edits a custom-kind declaration in the key/value editor, keeping values it did not touch', async () => {
  apiKnowledgeGraph.updateManualNode.mockResolvedValue({ data: { data: { kg_update_manual_node: { data: { id: 11 } } } } });
  renderDialog({
    schema: withOutside(),
    row: {
      id: 11,
      cloud_provider: 'external',
      node_type: 'CloudResource',
      specific_type: 'NetworkSwitch',
      name: 'sw-core-01',
      description: '',
      properties: { site: 'blr-1', ports: 48, vlans: ['10', '20'] },
      labels: {},
      status: 'active',
    },
  });

  expect(screen.getByDisplayValue('site')).toBeInTheDocument();
  expect(screen.getByDisplayValue('48')).toBeInTheDocument();
  fireEvent.change(screen.getByDisplayValue('blr-1'), { target: { value: 'blr-2' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() =>
    expect(apiKnowledgeGraph.updateManualNode).toHaveBeenCalledWith({
      id: 11,
      node: { description: '', properties: { site: 'blr-2', ports: 48, vlans: ['10', '20'] }, labels: {} },
    })
  );
});

// ---- What the node acts as ----------------------------------------------------

const pickRole = async (label) => {
  fireEvent.click(document.querySelector('#manual-node-role'));
  fireEvent.click(await screen.findByText(label));
};

it('declares a node with the role it acts as and that role’s engine', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({ data: { data: { kg_create_manual_node: { data: { id: 4 } } } } });
  renderDialog({ schema: withOutside(), initial: { node_type: 'NetworkSwitch', name: 'sw-core-03' } });

  await pickRole('Load balancer');
  fireEvent.change(screen.getByTestId('manual-node-role-engine'), { target: { value: ' haproxy ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Declare' }));

  await waitFor(() => expect(apiKnowledgeGraph.createManualNode).toHaveBeenCalled());
  expect(apiKnowledgeGraph.createManualNode.mock.calls[0][0]).toMatchObject({ name: 'sw-core-03', role: 'loadbalancer', engine: 'haproxy' });
});

it('uses one engine field for a type that declares its own engine', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({ data: { data: { kg_create_manual_node: { data: { id: 5 } } } } });
  renderDialog({ initial: { node_type: 'Database', name: 'billing-db' } });

  fireEvent.click(screen.getByText('More details'));
  // The schema's engine field is not shown a second time…
  expect(screen.queryByText('engine')).not.toBeInTheDocument();
  // …and with no role picked the row's engine is the declared engine alone.
  fireEvent.change(screen.getByTestId('manual-node-role-engine'), { target: { value: 'postgres' } });
  fireEvent.click(screen.getByRole('button', { name: 'Declare' }));

  await waitFor(() => expect(apiKnowledgeGraph.createManualNode).toHaveBeenCalled());
  const request = apiKnowledgeGraph.createManualNode.mock.calls[0][0];
  expect(request.properties).toEqual({ engine: 'postgres' });
  expect(request).not.toHaveProperty('role');
  expect(request).not.toHaveProperty('engine');
});

it('changes an existing declaration’s role through the role action after saving it', async () => {
  apiKnowledgeGraph.updateManualNode.mockResolvedValue({ data: { data: { kg_update_manual_node: { data: { id: 12, role: 'cache' } } } } });
  apiKnowledgeGraph.setNodeRole.mockResolvedValue({ data: { data: { kg_upsert_node_role: { data: { node: { id: 'kg-12' } } } } } });
  const onSaved = jest.fn();
  const row = {
    id: 12,
    kg_node_id: 'kg-12',
    tenant_id: 't1',
    node_type: 'CloudResource',
    specific_type: 'NetworkSwitch',
    name: 'sw-core-04',
    properties: {},
    labels: {},
    status: 'active',
    role: 'cache',
  };
  renderDialog({ schema: withOutside(), row, onSaved });

  await pickRole('Proxy');
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));

  await waitFor(() => expect(apiKnowledgeGraph.setNodeRole).toHaveBeenCalledWith({ node_id: 'kg-12', role: 'proxy', engine: '' }));
  expect(onSaved).toHaveBeenCalledWith({ id: 12, role: 'proxy', engine: undefined });
});

it('removes the role when an edit sets it back to nothing in particular', async () => {
  apiKnowledgeGraph.updateManualNode.mockResolvedValue({ data: { data: { kg_update_manual_node: { data: { id: 13, role: 'cache' } } } } });
  apiKnowledgeGraph.removeNodeRole.mockResolvedValue({ data: { data: { kg_delete_node_role: { data: { node: { id: 'kg-13' } } } } } });
  const row = {
    id: 13,
    kg_node_id: 'kg-13',
    node_type: 'CloudResource',
    specific_type: 'NetworkSwitch',
    name: 'sw-5',
    properties: {},
    labels: {},
    status: 'active',
    role: 'cache',
  };
  renderDialog({ schema: withOutside(), row });

  await pickRole('Nothing in particular');
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));

  await waitFor(() => expect(apiKnowledgeGraph.removeNodeRole).toHaveBeenCalledWith({ node_id: 'kg-13' }));
  expect(apiKnowledgeGraph.setNodeRole).not.toHaveBeenCalled();
});

it('does not rewrite the role when an edit leaves it alone', async () => {
  apiKnowledgeGraph.updateManualNode.mockResolvedValue({ data: { data: { kg_update_manual_node: { data: { id: 14 } } } } });
  // The role row has no engine; the declaration's own engine property fills the field.
  const row = {
    id: 14,
    kg_node_id: 'kg-14',
    node_type: 'Database',
    specific_type: 'Database',
    name: 'billing-db',
    properties: { engine: 'postgres' },
    labels: {},
    status: 'active',
    role: 'database',
  };
  renderDialog({ row });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));

  await waitFor(() => expect(apiKnowledgeGraph.updateManualNode).toHaveBeenCalled());
  expect(apiKnowledgeGraph.setNodeRole).not.toHaveBeenCalled();
  expect(apiKnowledgeGraph.removeNodeRole).not.toHaveBeenCalled();
});

// An Azure key reads the resource group, so a declaration in an Azure account
// must send it: the form sends whatever key fields the type declares.
const AZURE_SCHEMA = [
  {
    cloud_account_id: 'az-1',
    account_name: 'Prod Azure',
    cloud_provider: 'Azure',
    kinds: [
      {
        specific_type: 'AzureSQLDatabase',
        node_type: 'Database',
        key_fields: [
          { name: 'name', label: 'Name', required: true },
          { name: 'region', label: 'Region', required: true },
          { name: 'resource_group', label: 'Resource group', required: true },
        ],
        properties: [],
      },
    ],
  },
];

it('sends every key field the type declares, including an Azure resource group', async () => {
  apiKnowledgeGraph.createManualNode.mockResolvedValue({ data: { data: { kg_create_manual_node: { data: { id: 3, kg_node_id: 'n3' } } } } });
  renderDialog({ schema: AZURE_SCHEMA });

  fireEvent.click(document.getElementById('manual-node-place'));
  fireEvent.click(await screen.findByText('Prod Azure'));
  fireEvent.click(document.getElementById('manual-node-type'));
  fireEvent.click(await screen.findByText(/SQL Database/));

  fireEvent.change(screen.getByTestId('manual-node-name'), { target: { value: 'sqlsrv1/orders' } });
  fireEvent.change(screen.getByTestId('manual-node-region'), { target: { value: 'eastus' } });
  const declare = screen.getByRole('button', { name: 'Declare' });
  expect(declare).toBeDisabled(); // resource group still empty
  fireEvent.change(screen.getByTestId('manual-node-resource_group'), { target: { value: ' rg-shop ' } });
  fireEvent.click(declare);

  await waitFor(() =>
    expect(apiKnowledgeGraph.createManualNode).toHaveBeenCalledWith(
      expect.objectContaining({
        cloud_account_id: 'az-1',
        specific_type: 'AzureSQLDatabase',
        name: 'sqlsrv1/orders',
        region: 'eastus',
        resource_group: 'rg-shop',
      })
    )
  );
});

it('shows an Azure declaration with its resource group as a locked key field', () => {
  renderDialog({
    schema: AZURE_SCHEMA,
    row: {
      id: 11,
      cloud_account_id: 'az-1',
      cloud_provider: 'Azure',
      specific_type: 'AzureSQLDatabase',
      node_type: 'Database',
      name: 'sqlsrv1/orders',
      region: 'eastus',
      resource_group: 'rg-shop',
      properties: {},
      labels: {},
      status: 'active',
    },
  });
  expect(screen.getByTestId('manual-node-resource_group')).toHaveValue('rg-shop');
  expect(screen.getByTestId('manual-node-resource_group')).toBeDisabled();
});
