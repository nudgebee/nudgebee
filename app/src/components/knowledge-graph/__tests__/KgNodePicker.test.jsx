// KG node picker's Node type dropdown lists specific types grouped by node type
// (same as the Knowledge Graph page's Node Type filter), so the node list is
// fetched by specific_type when the picked leaf is one, else by node_type.
import { fireEvent, render, waitFor } from '@testing-library/react';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import KgNodePicker from '../KgNodePicker';

jest.mock('@api1/knowledge-graph', () => ({
  __esModule: true,
  default: { getFilterOptions: jest.fn(), getNode: jest.fn() },
}));
jest.mock('@lib/auth', () => ({ getCurrentTenant: () => ({ id: 'tenant-1' }) }));
jest.mock('@ui/Toast', () => ({ toast: { error: jest.fn() } }));

const filterOptions = (data) => ({ data: { data: { kg_get_filter_options: { data } } } });

const renderPicker = (pickedNodeType) =>
  render(
    <KgNodePicker
      pickedAccountId='acc-1'
      pickedNodeType={pickedNodeType}
      cloudAccounts={[{ id: 'acc-1', account_name: 'iteration-test', cloud_provider: 'K8s' }]}
      onAccountChange={jest.fn()}
      onNodeTypeChange={jest.fn()}
      onPick={jest.fn()}
    />
  );

// Triggers render in order: Account, Node type, Node.
const openNodeDropdown = (container) => fireEvent.click(container.querySelectorAll('button[id^="auto-complete-"]')[2]);

beforeEach(() => {
  apiKnowledgeGraph.getFilterOptions.mockReset();
  apiKnowledgeGraph.getFilterOptions.mockImplementation(({ nodeTypes, specificTypes } = {}) =>
    Promise.resolve(
      nodeTypes || specificTypes
        ? filterOptions({ node_keys: [], node_ids: [] })
        : filterOptions({ node_types: { Workload: ['KubernetesDeployment', 'KubernetesStatefulSet'], ConfigMap: [] } })
    )
  );
});

it('fetches nodes by specific_type for a leaf under a broken-down node type', async () => {
  const { container } = renderPicker('KubernetesDeployment');
  openNodeDropdown(container);
  await waitFor(() =>
    expect(apiKnowledgeGraph.getFilterOptions).toHaveBeenLastCalledWith({ accountIds: ['acc-1'], specificTypes: ['KubernetesDeployment'] })
  );
});

it('fetches nodes by node_type for a node type with no breakdown', async () => {
  const { container } = renderPicker('ConfigMap');
  openNodeDropdown(container);
  await waitFor(() => expect(apiKnowledgeGraph.getFilterOptions).toHaveBeenLastCalledWith({ accountIds: ['acc-1'], nodeTypes: ['ConfigMap'] }));
});
