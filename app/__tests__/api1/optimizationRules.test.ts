import recommendationApi from '@api1/recommendation';
import { gqlStringify, queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.fn((value) => JSON.stringify(value)),
}));
jest.mock('@lib/datetime', () => ({}));
jest.mock('@api1/tickets', () => ({}));
jest.mock('@api1/mock', () => jest.fn());
jest.mock('@lib/cache', () => ({}));
jest.mock('src/utils/common', () => ({ safeJSONParse: JSON.parse }));

const query = queryGraphQL as jest.Mock;
const stringify = gqlStringify as jest.Mock;

beforeEach(() => {
  jest.clearAllMocks();
  query.mockResolvedValue({ data: { data: { recommendation_groupings_v2: { rows: [] } } } });
});

it('scopes optimization rules to the selected account', async () => {
  await recommendationApi.getDistinctOptimizationRules('aws-account');
  expect(stringify).toHaveBeenCalledWith(expect.objectContaining({ account_id: { _eq: 'aws-account' } }), []);
  expect(query).toHaveBeenCalledWith(expect.stringContaining('"account_id":{"_eq":"aws-account"}'), 'GetDistinctOptimizationRules');
});

it('keeps tenant-wide rules for a trigger without an account filter', async () => {
  await recommendationApi.getDistinctOptimizationRules();
  expect(stringify.mock.calls[0][0]).not.toHaveProperty('account_id');
});

it('keeps only valid category/rule pairs returned by the scoped query', async () => {
  query.mockResolvedValue({
    data: {
      data: {
        recommendation_groupings_v2: {
          rows: [{ category: 'RightSizing', rule_name: 'aws_idle_instance' }, { category: null, rule_name: 'bad' }, {}],
        },
      },
    },
  });
  await expect(recommendationApi.getDistinctOptimizationRules('aws-account')).resolves.toEqual([
    { category: 'RightSizing', rule_name: 'aws_idle_instance' },
  ]);
});
