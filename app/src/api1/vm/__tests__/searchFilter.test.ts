import { queryGraphQL } from '@lib/HttpService';
import apiVm from '../index';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn().mockResolvedValue({ data: { data: { vms: { rows: [] }, packages: { rows: [] } } } }),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
  escapeLikePattern: jest.requireActual('@lib/HttpService').escapeLikePattern,
}));
jest.mock('@api1/home', () => ({}));
jest.mock('@api1/integrations', () => ({}));
jest.mock('@api1/kubernetes', () => ({}));
jest.mock('@api1/tickets', () => ({}));

const query = queryGraphQL as jest.Mock;

const builtQueryFor = async (list: 'listVms' | 'listPackages', search: string) => {
  query.mockClear();
  await apiVm[list]({ accountId: `acct-${list}-${search}`, search });
  return query.mock.calls[0][0] as string;
};

// Typing `%` or `_` in the VM or package search must match literally, not act as an ILIKE
// wildcard that returns every row. Regression guard for #41436.
describe.each(['listVms', 'listPackages'] as const)('%s search escapes ILIKE wildcards', (list) => {
  it('escapes a typed % so it is not a match-all wildcard', async () => {
    const q = await builtQueryFor(list, '%');
    // The typed % is escaped (\%); only the wrapping %…% stay as wildcards.
    expect(q).toContain('_ilike:"%\\\\%%"');
  });

  it('escapes a typed _ so it is matched literally', async () => {
    const q = await builtQueryFor(list, '_');
    expect(q).toContain('_ilike:"%\\\\_%"');
  });

  it('leaves ordinary text as a plain contains pattern', async () => {
    const q = await builtQueryFor(list, 'openssl');
    expect(q).toContain('_ilike:"%openssl%"');
    expect(q).not.toContain('\\');
  });
});
