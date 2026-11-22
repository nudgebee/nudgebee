// The node details popup shows the node's role, offers to change it only to
// someone who may, and keeps the role overlay's bookkeeping keys out of the
// raw properties table.

import { render, screen } from '@testing-library/react';
import NodeDetails from '../NodeDetails';
import { hasWriteAccess, isTenantAdmin } from '@lib/auth';

jest.mock('@lib/auth', () => ({
  getCurrentTenant: () => ({ id: 'tenant-1' }),
  hasWriteAccess: jest.fn(),
  isTenantAdmin: jest.fn(),
}));
jest.mock('@api1/knowledge-graph', () => ({
  __esModule: true,
  default: { listNodeRoles: jest.fn().mockResolvedValue({ roles: [{ value: 'cache', label: 'Cache' }], target_node_types: ['Workload'] }) },
}));
// The properties table: one cell per key, enough to see which keys are shown.
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData }) => (
    <ul data-testid='properties-table'>
      {tableData.map((row, i) => (
        <li key={i}>{JSON.stringify(row)}</li>
      ))}
    </ul>
  ),
}));
// The real helper pulls in the markdown viewer; one row per key is enough here.
jest.mock('@components/k8s/investigate/cards/util', () => ({
  getTableData4: ([obj]) => ({ headers: [], convertedJson2: Object.entries(obj || {}).map(([k, v]) => [k, v]) }),
}));
jest.mock('@shared/format/Datetime', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/knowledge-graph/ManualNodesTable', () => ({ humanizeType: (t) => t }));
jest.mock('@components/knowledge-graph/NodeRoleDialog', () => ({ __esModule: true, default: () => null }));

const userNode = {
  id: 'n1',
  node_type: 'Workload',
  cloud_account_id: 'acct-1',
  unique_key: 'k8s:acct-1::Workload:redis:redis-master',
  properties: { name: 'redis-master', role: 'cache', engine: 'redis', role_source: 'user', detected_role: 'database', detected_engine: 'postgres' },
};

beforeEach(() => {
  jest.clearAllMocks();
  hasWriteAccess.mockReturnValue(true);
  isTenantAdmin.mockReturnValue(false);
});

it('shows a user role, what it overrode, and the way to change it', async () => {
  render(<NodeDetails node={userNode} onRoleChange={jest.fn()} />);
  const row = screen.getByTestId('kg-node-role');
  expect(row).toHaveTextContent('Cache');
  expect(row).toHaveTextContent('engine: redis');
  expect(row).toHaveTextContent('User-assigned');
  expect(row).toHaveTextContent('detected: Database');
  expect(await screen.findByTestId('kg-node-role-edit')).toHaveTextContent('Edit role');
});

it('hides the bookkeeping keys from the properties table', () => {
  render(<NodeDetails node={userNode} onRoleChange={jest.fn()} />);
  const table = screen.getByTestId('properties-table').textContent;
  expect(table).toContain('redis-master');
  expect(table).not.toContain('role_source');
  expect(table).not.toContain('detected_role');
  expect(table).not.toContain('detected_engine');
});

it('is read-only without write access to the node’s account', () => {
  hasWriteAccess.mockReturnValue(false);
  render(<NodeDetails node={userNode} onRoleChange={jest.fn()} />);
  expect(screen.getByTestId('kg-node-role')).toHaveTextContent('Cache');
  expect(screen.queryByTestId('kg-node-role-edit')).not.toBeInTheDocument();
});

it('asks for tenant-wide access on a node outside any account', () => {
  const outside = { ...userNode, cloud_account_id: 'tenant-1' };
  render(<NodeDetails node={outside} onRoleChange={jest.fn()} />);
  expect(screen.queryByTestId('kg-node-role-edit')).not.toBeInTheDocument();
  expect(hasWriteAccess).not.toHaveBeenCalled();
});

it('shows nothing for a node with no role that cannot be given one here', () => {
  render(<NodeDetails node={{ ...userNode, properties: { name: 'api' } }} />);
  expect(screen.queryByTestId('kg-node-role')).not.toBeInTheDocument();
});
