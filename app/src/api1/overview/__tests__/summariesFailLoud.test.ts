import { queryGraphQL } from '@lib/HttpService';
import apiOverview from '../index';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn(() => '{}') }));
jest.mock('@api1/vm', () => ({ OPEN_STATUSES: ['Open'], VM_VULNERABILITY_RULE: 'vm_vulnerability' }));

// A GraphQL error used to fold into the zeroed defaults, so a failed rollup
// rendered as "$0 spend, 0 resources" on every cloud card and "No open package
// vulnerabilities" on every VM card. The caller must be able to tell.
describe('account overview summaries fail loudly', () => {
  beforeEach(() => jest.clearAllMocks());

  it('throws when the cloud rollup comes back with GraphQL errors', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({ data: { errors: [{ message: 'permission denied' }] } });
    await expect(apiOverview.listCloudAccountSummaries(['a'])).rejects.toThrow('permission denied');
  });

  it('throws when the VM rollup has no data envelope at all', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({ data: {} });
    await expect(apiOverview.listVmAccountSummaries(['a'])).rejects.toThrow(/Failed to load VM account summaries/);
  });

  it('still zero-fills accounts that are simply absent from a successful response', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({
      data: { data: { mtd: { rows: [{ account_id: 'a', spend_amount: 5, currency_type: 'USD' }] }, prev_month: { rows: [] }, ytd: { rows: [] } } },
    });
    const out = await apiOverview.listCloudAccountSummaries(['a', 'b']);
    expect(out.a.mtdSpend).toBe(5);
    expect(out.b).toMatchObject({ accountId: 'b', mtdSpend: 0, resourceCount: 0 });
  });
});
