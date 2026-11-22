// The role dialog decides what reaches kg_upsert_node_role / kg_delete_node_role,
// and tells the user what happened beyond the node they opened.

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import NodeRoleDialog from '../NodeRoleDialog';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { toast } from '@ui/Toast';
import { resetNodeRoleVocabulary } from '../nodeRoles';

jest.mock('@api1/knowledge-graph', () => ({
  __esModule: true,
  default: { listNodeRoles: jest.fn(), setNodeRole: jest.fn(), removeNodeRole: jest.fn() },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn(), warning: jest.fn(), info: jest.fn() } }));
// A native select stands in for the design-system one.
jest.mock('@ui/Select', () => ({
  __esModule: true,
  Select: ({ id, options, value, onChange }) => (
    <select data-testid={id} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value=''>-</option>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));
// The modal shell renders its children and footer inline.
jest.mock('@ui/Modal', () => ({
  __esModule: true,
  Modal: ({ open, title, children, actionButtons }) =>
    open ? (
      <div>
        <h2>{title}</h2>
        {children}
        {actionButtons}
      </div>
    ) : null,
}));

const detectedNode = { id: 'n1', node_type: 'Workload', properties: { name: 'redis-master', role: 'database', engine: 'postgres' } };
const userNode = {
  id: 'n1',
  node_type: 'Workload',
  properties: { name: 'redis-master', role: 'cache', engine: 'redis', role_source: 'user', detected_role: 'database', detected_engine: 'postgres' },
};
const result = (extra = {}) => ({ node: { id: 'n1', properties: { role: 'cache' } }, applied: [], skipped: [], build_in_progress: false, ...extra });

beforeEach(() => {
  jest.clearAllMocks();
  resetNodeRoleVocabulary();
  apiKnowledgeGraph.listNodeRoles.mockResolvedValue({
    roles: [
      { value: 'database', label: 'Database' },
      { value: 'cache', label: 'Cache' },
    ],
    target_node_types: ['Workload'],
  });
});

it('shows what was detected and saves the chosen role with its engine', async () => {
  apiKnowledgeGraph.setNodeRole.mockResolvedValue({ data: { data: { kg_upsert_node_role: { data: result() } } } });
  const onSaved = jest.fn();
  const onClose = jest.fn();
  render(<NodeRoleDialog open node={detectedNode} onClose={onClose} onSaved={onSaved} />);

  expect(screen.getByTestId('kg-node-role-detected')).toHaveTextContent('Database · postgres');
  // A detected role is not the user's: there is nothing to remove yet.
  expect(screen.queryByTestId('kg-node-role-remove')).not.toBeInTheDocument();
  expect(screen.getByTestId('kg-node-role-save')).toBeDisabled();

  await screen.findByRole('option', { name: 'Cache' });
  fireEvent.change(screen.getByTestId('kg-node-role-select'), { target: { value: 'cache' } });
  fireEvent.change(screen.getByTestId('kg-node-role-engine'), { target: { value: ' redis ' } });
  fireEvent.click(screen.getByTestId('kg-node-role-save'));

  await waitFor(() => expect(apiKnowledgeGraph.setNodeRole).toHaveBeenCalledWith({ node_id: 'n1', role: 'cache', engine: 'redis' }));
  expect(onSaved).toHaveBeenCalledWith(result());
  expect(onClose).toHaveBeenCalled();
  expect(toast.success).toHaveBeenCalledWith('Role set.');
});

it('removes a user role and reports linked instances it could not change', async () => {
  apiKnowledgeGraph.removeNodeRole.mockResolvedValue({
    data: { data: { kg_delete_node_role: { data: result({ applied: ['twin-a'], skipped: ['twin-b'], build_in_progress: true }) } } },
  });
  render(<NodeRoleDialog open node={userNode} onClose={jest.fn()} onSaved={jest.fn()} />);

  expect(screen.getByTestId('kg-node-role-select')).toHaveValue('cache');
  // Unchanged: nothing to save.
  expect(screen.getByTestId('kg-node-role-save')).toBeDisabled();
  fireEvent.click(screen.getByTestId('kg-node-role-remove'));

  await waitFor(() => expect(apiKnowledgeGraph.removeNodeRole).toHaveBeenCalledWith({ node_id: 'n1' }));
  expect(toast.success).toHaveBeenCalledWith('Role removed. Also removed on 1 linked instance.');
  expect(toast.warning).toHaveBeenCalledWith('Not changed on 1 linked instance in an account you cannot edit.');
  expect(toast.info).toHaveBeenCalled();
});

it('keeps the dialog open and says why when the server refuses', async () => {
  apiKnowledgeGraph.setNodeRole.mockResolvedValue({ data: { errors: [{ message: 'this type of node cannot carry a role' }] } });
  const onSaved = jest.fn();
  const onClose = jest.fn();
  render(<NodeRoleDialog open node={detectedNode} onClose={onClose} onSaved={onSaved} />);

  await screen.findByRole('option', { name: 'Cache' });
  fireEvent.change(screen.getByTestId('kg-node-role-select'), { target: { value: 'cache' } });
  fireEvent.click(screen.getByTestId('kg-node-role-save'));

  await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Could not change the role: this type of node cannot carry a role'));
  expect(onSaved).not.toHaveBeenCalled();
  expect(onClose).not.toHaveBeenCalled();
});
