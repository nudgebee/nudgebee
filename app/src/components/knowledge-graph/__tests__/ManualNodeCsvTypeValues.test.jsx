// The CSV help lists exactly the type values the importer accepts: node_type
// outside a connected account, specific_type per connected cloud.

import { fireEvent, render, screen, within } from '@testing-library/react';
import ManualNodeCsvTypeValues, { csvTypeCategories, groupCsvTypeValues } from '../ManualNodeCsvTypeValues';

const name = { name: 'name', required: true };
const SCHEMA = [
  {
    cloud_account_id: 'aws-1',
    cloud_provider: 'AWS',
    kinds: [
      { specific_type: 'SQSQueue', node_type: 'MessageQueue', key_fields: [name, { name: 'region', required: true }] },
      {
        specific_type: 'RDSInstance',
        node_type: 'Database',
        key_fields: [name, { name: 'region', required: true }, { name: 'network', required: false }],
      },
    ],
  },
  // A second AWS account offers the same types: they are listed once.
  {
    cloud_account_id: 'aws-2',
    cloud_provider: 'AWS',
    kinds: [{ specific_type: 'SQSQueue', node_type: 'MessageQueue', key_fields: [name, { name: 'region', required: true }] }],
  },
  {
    cloud_account_id: 'k8s-1',
    cloud_provider: 'K8s',
    kinds: [{ specific_type: 'KubernetesDeployment', node_type: 'Workload', key_fields: [name, { name: 'namespace', required: true }] }],
  },
  {
    cloud_provider: 'external',
    kinds: [{ specific_type: 'Database', node_type: 'Database', key_fields: [name, { name: 'namespace', required: false }] }],
  },
];

it('groups type values by place and lists each specific type once', () => {
  const { outside, providers } = groupCsvTypeValues(SCHEMA);
  expect(outside.map((k) => k.node_type)).toEqual(['Database']);
  expect(providers.map((p) => p.provider)).toEqual(['AWS', 'K8s']);
  expect(providers[0].kinds.map((k) => k.specific_type)).toEqual(['RDSInstance', 'SQSQueue']);
});

it('makes one category per account type, outside accounts first', () => {
  expect(csvTypeCategories(SCHEMA).map((c) => `${c.label}:${c.column}`)).toEqual([
    'Not in an account:node_type',
    'AWS:specific_type',
    'Kubernetes:specific_type',
  ]);
});

it('shows one account type at a time, with its values, node types and identity columns', () => {
  render(<ManualNodeCsvTypeValues schema={SCHEMA} />);
  const panel = screen.getByTestId('manual-nodes-csv-type-values');

  // Outside an account is shown first, keyed by node_type.
  expect(within(panel).getByRole('button', { name: 'Not in an account (1)' })).toHaveAttribute('aria-pressed', 'true');
  expect(within(panel).getByRole('columnheader', { name: 'node_type' })).toBeInTheDocument();
  expect(within(panel).queryByText('RDSInstance')).not.toBeInTheDocument();

  fireEvent.click(within(panel).getByRole('button', { name: 'AWS (2)' }));
  expect(within(panel).getByRole('columnheader', { name: 'specific_type' })).toBeInTheDocument();
  const rds = within(panel).getByText('RDSInstance').closest('tr');
  expect(within(rds).getByText('Database')).toBeInTheDocument();
  expect(within(rds).getByText('region, network (optional)')).toBeInTheDocument();
  expect(within(panel).queryByText('KubernetesDeployment')).not.toBeInTheDocument();

  fireEvent.click(within(panel).getByRole('button', { name: 'Kubernetes (1)' }));
  const deploy = within(panel).getByText('KubernetesDeployment').closest('tr');
  expect(within(deploy).getByText('namespace')).toBeInTheDocument();
});

it('renders nothing until the schema has loaded', () => {
  const { container } = render(<ManualNodeCsvTypeValues schema={[]} />);
  expect(container).toBeEmptyDOMElement();
});
