// Node Type filter options (#40705): groups and leaves read as spaced names, and
// search still finds a type by its spaced or raw PascalCase name.
import { buildNodeTypeOptions } from '../kgFilterCascade';

const options = buildNodeTypeOptions(['ComputeInstance', 'Database', 'VPC'], {
  ComputeInstance: ['EC2Instance', 'GCEInstance'],
  Database: ['DynamoDBTable', 'RDSInstance'],
});

// Same haystack FilterDropdown searches: the visible label plus `searchText`.
const search = (q) => options.filter((o) => `${o.label} ${o.searchText || ''}`.toLowerCase().includes(q.toLowerCase())).map((o) => o.value);

it('spaces group headers and option labels, keeping raw values', () => {
  expect(options).toEqual([
    expect.objectContaining({ label: 'EC2 Instance', value: 'EC2Instance', group: 'Compute Instance' }),
    expect.objectContaining({ label: 'GCE Instance', value: 'GCEInstance', group: 'Compute Instance' }),
    expect.objectContaining({ label: 'DynamoDB Table', value: 'DynamoDBTable', group: 'Database' }),
    expect.objectContaining({ label: 'RDS Instance', value: 'RDSInstance', group: 'Database' }),
    expect.objectContaining({ label: 'VPC', value: 'VPC' }),
  ]);
  expect(options[4].group).toBeUndefined();
});

it.each([
  ['DynamoDB Table', ['DynamoDBTable']],
  ['DynamoDBTable', ['DynamoDBTable']],
  ['RDS Instance', ['RDSInstance']],
  ['RDSInstance', ['RDSInstance']],
  ['Compute Instance', ['EC2Instance', 'GCEInstance']],
  ['ComputeInstance', ['EC2Instance', 'GCEInstance']],
  ['vpc', ['VPC']],
])('search "%s" finds %j', (q, values) => {
  expect(search(q)).toEqual(values);
});
