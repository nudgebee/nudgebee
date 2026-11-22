import { queryGraphQL } from '@lib/HttpService';
import apiCloudAccount from '../index';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn().mockResolvedValue({ data: { data: { cloud_resourses: { rows: [] } } } }),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
  escapeLikePattern: jest.requireActual('@lib/HttpService').escapeLikePattern,
}));

const query = queryGraphQL as jest.Mock;

const builtQueryFor = async (nameFilter: string) => {
  query.mockClear();
  await apiCloudAccount.getCloudResource({ account_id: 'acct', serviceName: 'AmazonEC2', nameFilter });
  return query.mock.calls[0][0] as string;
};

// Typing `%` or `_` in a cloud resource search must match literally, not act as an ILIKE
// wildcard that returns every row. Regression guard for #40558.
describe('cloud resource name search escapes ILIKE wildcards', () => {
  it('escapes a typed % so it is not a match-all wildcard', async () => {
    const q = await builtQueryFor('%');
    // The typed % is escaped (\%); only the wrapping %…% stay as wildcards.
    expect(q).toContain('_ilike:"%\\\\%%"');
  });

  it('escapes a typed _ so it is matched literally', async () => {
    const q = await builtQueryFor('a_b');
    expect(q).toContain('_ilike:"%a\\\\_b%"');
  });

  it('leaves ordinary text as a plain contains pattern', async () => {
    const q = await builtQueryFor('orders');
    expect(q).toContain('_ilike:"%orders%"');
    expect(q).not.toContain('\\');
  });
});
