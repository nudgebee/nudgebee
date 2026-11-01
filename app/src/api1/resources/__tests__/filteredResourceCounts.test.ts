import { buildFilteredResourceCountsWhere } from '../index';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn() }));
jest.mock('@lib/contexts', () => ({ getGlobalStartDate: jest.fn(), getGlobalEndDate: jest.fn() }));
jest.mock('@lib/budget', () => ({ getBudgetExpectedMonthlyExpense: jest.fn() }));

type Row = { service: string; type: string; status: string; rows: number };

// Evaluates the where clause the way the query layer turns it into SQL, for the
// operators this filter uses (_eq, _in, _not_in, _or).
const matches = (where: any, row: Row): boolean => {
  const value: Record<string, string> = {
    resource_service_name: row.service,
    resource_type: row.type,
    resource_status: row.status,
  };
  return Object.entries(where).every(([key, cond]: [string, any]) => {
    if (key === '_or') return cond.some((branch: any) => matches(branch, row));
    if (key === 'account_id') return true;
    if ('_eq' in cond) return value[key] === cond._eq;
    if ('_in' in cond) return cond._in.includes(value[key]);
    if ('_not_in' in cond) return !cond._not_in.includes(value[key]);
    throw new Error(`unexpected condition on ${key}`);
  });
};

const countFor = (rows: Row[], service: string) =>
  rows.filter((r) => r.service === service && matches(buildFilteredResourceCountsWhere('acct'), r)).reduce((n, r) => n + r.rows, 0);

// Active AmazonEC2 rows of one real account: the tile showed 550,887 "EC2
// Instances" while the EC2 Instances tab listed 589.
const ec2Rows: Row[] = [
  { service: 'AmazonEC2', type: 'snapshot', status: 'Active', rows: 546281 },
  { service: 'AmazonEC2', type: 'storage', status: 'Active', rows: 1907 },
  { service: 'AmazonEC2', type: 'provisioned-throughput', status: 'Active', rows: 1292 },
  { service: 'AmazonEC2', type: 'system-operation', status: 'Active', rows: 818 },
  { service: 'AmazonEC2', type: 'compute-instance', status: 'Active', rows: 589 },
  { service: 'AmazonEC2', type: 'data-transfer', status: 'Active', rows: 531 },
  { service: 'AmazonEC2', type: 'cpu-credits', status: 'Active', rows: 6 },
];

it('counts only instances on the EC2 tile, matching the EC2 Instances tab', () => {
  expect(countFor(ec2Rows, 'AmazonEC2')).toBe(589);
});

it('counts only the listed kind for every mapped service', () => {
  const rows: Row[] = [
    { service: 'AmazonS3', type: 'storage', status: 'Active', rows: 12 },
    { service: 'AmazonS3', type: 'snapshot', status: 'Active', rows: 40 },
    { service: 'Compute Engine', type: 'compute.googleapis.com/Instance', status: 'Active', rows: 7 },
    { service: 'Compute Engine', type: 'compute.googleapis.com/Disk', status: 'Active', rows: 30 },
  ];
  expect(countFor(rows, 'AmazonS3')).toBe(12);
  expect(countFor(rows, 'Compute Engine')).toBe(7);
});

it('keeps unmapped services on the deny-list, without snapshots or IOPS charges', () => {
  const rows: Row[] = [
    { service: 'AmazonRDS', type: 'db', status: 'Active', rows: 9 },
    { service: 'AmazonRDS', type: 'snapshot', status: 'Active', rows: 300 },
    { service: 'AmazonRDS', type: 'system-operation', status: 'Active', rows: 4 },
    { service: 'AWSLambda', type: 'function', status: 'Active', rows: 5 },
  ];
  expect(countFor(rows, 'AmazonRDS')).toBe(9);
  expect(countFor(rows, 'AWSLambda')).toBe(5);
});

it('never counts inactive rows', () => {
  expect(countFor([{ service: 'AmazonEC2', type: 'compute-instance', status: 'Deleted', rows: 3 }], 'AmazonEC2')).toBe(0);
});
