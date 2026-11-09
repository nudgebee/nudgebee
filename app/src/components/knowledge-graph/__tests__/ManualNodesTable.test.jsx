// What each declaration status lets the operator do: an adopted node belongs
// to the collector (no edit), a possible duplicate offers Merge.

import { fireEvent, render, screen } from '@testing-library/react';
import ManualNodesTable, { humanizeType } from '../ManualNodesTable';

const row = (over) => ({
  id: 1,
  name: 'orders',
  specific_type: 'SQSQueue',
  node_type: 'MessageQueue',
  cloud_account_id: 'acct-1',
  region: 'us-east-1',
  unique_key: 'aws:acct-1:us-east-1:MessageQueue::orders',
  status: 'active',
  created_at: '2026-10-01T00:00:00Z',
  ...over,
});

const renderTable = (rows, handlers = {}) =>
  render(
    <ManualNodesTable
      rows={rows}
      accountNames={{ 'acct-1': 'Prod AWS' }}
      onEditClick={handlers.onEdit || jest.fn()}
      onMergeClick={handlers.onMerge || jest.fn()}
      onDeleteClick={handlers.onDelete || jest.fn()}
    />
  );

const openMenu = () => fireEvent.click(screen.getAllByRole('button', { name: 'More actions' })[0]);

it('shows where the node lives and its type', () => {
  renderTable([row()]);
  expect(screen.getByText('Prod AWS · us-east-1')).toBeInTheDocument();
  expect(screen.getByText('SQS Queue')).toBeInTheDocument();
  expect(screen.getByText('Active')).toBeInTheDocument();
});

// The declare form's type picker searches these labels, so "EC2" + "Instance"
// must come apart or typing "EC2 Instance" finds nothing.
it.each([
  ['SQSQueue', 'SQS Queue'],
  ['EC2Instance', 'EC2 Instance'],
  ['S3Bucket', 'S3 Bucket'],
  ['RDSInstance', 'RDS Instance'],
  ['GCSBucket', 'GCS Bucket'],
  ['APIGatewayHttpApi', 'API Gateway Http Api'],
  ['Database', 'Database'],
])('humanizes %s as %s', (type, label) => {
  expect(humanizeType(type)).toBe(label);
});

it('labels a node outside connected accounts', () => {
  renderTable([row({ cloud_account_id: '', region: '', namespace: 'finance' })]);
  expect(screen.getByText('Outside connected accounts · finance')).toBeInTheDocument();
});

it('offers Merge only for a possible duplicate', async () => {
  const onMerge = jest.fn();
  renderTable([row({ status: 'possible_duplicate' })], { onMerge });
  openMenu();
  fireEvent.click(await screen.findByText('Merge into collected node'));
  expect(onMerge).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }));
});

it('does not offer Edit on an adopted node', async () => {
  renderTable([row({ status: 'adopted' })]);
  expect(screen.getByText('Adopted')).toBeInTheDocument();
  openMenu();
  await screen.findByText('Delete');
  expect(screen.queryByText('Edit')).not.toBeInTheDocument();
});

it('shows the empty state', () => {
  renderTable([]);
  expect(screen.getByText('No manual nodes yet')).toBeInTheDocument();
});
