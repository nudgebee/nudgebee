// "Declare node" on an unmatched dependency endpoint. An endpoint carrying a
// Resource ID or account can never resolve to a declared node, so the action
// is shown disabled with what to fix rather than letting the user declare a
// node the dependency will never reach.

import { fireEvent, render, screen } from '@testing-library/react';
import ManualDependenciesTable from '../ManualDependenciesTable';

const dep = (over) => ({
  id: 3,
  source_node_type: 'Workload',
  source_name: 'billing-api',
  dest_node_type: 'Database',
  dest_name: 'billing-db',
  relationship_type: 'CALLS',
  resolution_status: 'dest_unmatched',
  ...over,
});

const renderTable = (rows, onDeclareNodeClick = jest.fn()) =>
  render(
    <ManualDependenciesTable
      rows={rows}
      onResolveClick={jest.fn()}
      onEditClick={jest.fn()}
      onReresolveClick={jest.fn()}
      onDeleteClick={jest.fn()}
      onDeclareNodeClick={onDeclareNodeClick}
      declarableTypes={new Set(['Database', 'Service'])}
    />
  );

const openMenu = () => fireEvent.click(screen.getAllByRole('button', { name: 'More actions' })[0]);

it('offers to declare the unmatched destination', async () => {
  const onDeclare = jest.fn();
  renderTable([dep()], onDeclare);
  openMenu();
  fireEvent.click(await screen.findByText('Declare destination node'));
  expect(onDeclare).toHaveBeenCalledWith(expect.objectContaining({ id: 3 }), 'dest');
});

it('disables it when the endpoint names a Resource ID', async () => {
  renderTable([dep({ dest_arn: 'arn:aws:rds:us-east-1:1:db:billing' })]);
  openMenu();
  const item = await screen.findByText(/Declare destination node \(clear its Resource ID \/ account first\)/);
  expect(item.closest('[role="menuitem"]')).toHaveAttribute('aria-disabled', 'true');
});

it('does not offer it for a type that cannot be declared, or a resolved row', async () => {
  renderTable([dep({ resolution_status: 'source_unmatched', source_node_type: 'Pod' }), dep({ id: 4, resolution_status: 'resolved' })]);
  for (const button of screen.getAllByRole('button', { name: 'More actions' })) {
    fireEvent.click(button);
    await screen.findAllByText('Delete');
    expect(screen.queryByText(/Declare .* node/)).not.toBeInTheDocument();
    fireEvent.keyDown(document.activeElement || document.body, { key: 'Escape' });
  }
});
