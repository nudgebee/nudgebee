import { queryGraphQL } from '@lib/HttpService';
import recommendationApi from '../index';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  splitAndParallelQuery: jest.fn(),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
}));

const query = queryGraphQL as jest.Mock;

beforeEach(() => {
  query.mockReset().mockResolvedValue({
    data: {
      data: { recommendation_groupings_v2: { rows: [{ category: 'Configuration', rule_name: 'aws_rds_backup_disabled' }, { category: null }] } },
    },
  });
});

// Regression guard for #39837: the Optimization trigger's Rule Name list was tenant-wide,
// so an AWS account was offered GCP rules.
describe('getDistinctOptimizationRules', () => {
  it('narrows the rules to the given accounts', async () => {
    const rules = await recommendationApi.getDistinctOptimizationRules(['acct-1', 'acct-2']);

    expect(query.mock.calls[0][0]).toContain('account_id:{_in:["acct-1","acct-2"]}');
    expect(query.mock.calls[0][0]).toContain('group_by: ["category", "rule_name"]');
    expect(rules).toEqual([{ category: 'Configuration', rule_name: 'aws_rds_backup_disabled' }]);
  });

  it('reads the whole tenant when no account is given', async () => {
    await recommendationApi.getDistinctOptimizationRules();

    expect(query.mock.calls[0][0]).not.toContain('account_id');
    expect(query.mock.calls[0][0]).toContain('category:{_in:["RightSizing","InfraUpgrade","Configuration","K8sSpotRecommendation"]}');
  });
});
