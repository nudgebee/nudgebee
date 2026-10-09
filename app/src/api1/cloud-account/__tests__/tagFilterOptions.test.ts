import { queryGraphQL } from '@lib/HttpService';
import apiCloudAccount, { EMPTY_TAG_VALUE, EMPTY_TAG_VALUE_LABEL } from '../index';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.fn((v) => JSON.stringify(v)),
}));

const query = queryGraphQL as jest.Mock;

beforeEach(() => {
  query.mockReset().mockResolvedValue({ data: { data: { cloud_resource_v2: { rows: [] } } } });
});

// The resource tables (EC2, RDS, ECS, S3, CloudFoundry) show both Active and Inactive
// resources — buildStateApiParams defaults the "All" state to ['Active','Inactive']. The
// tag filter options must query the same set, otherwise a tag carried only by a stopped/
// terminated resource never appears as a filter option. Regression guard for #38658.
describe('tag filter options include stopped/inactive resources', () => {
  it('getDistinctTagKeys queries both Active and Inactive', async () => {
    await apiCloudAccount.getDistinctTagKeys('acct', 'AmazonEC2');
    expect(query.mock.calls[0][2].where.status).toEqual({ _in: ['Active', 'Inactive'] });
  });

  it('getDistinctTagValues queries both Active and Inactive', async () => {
    await apiCloudAccount.getDistinctTagValues('acct', 'tier', 'AmazonEC2');
    expect(query.mock.calls[0][2].where.status).toEqual({ _in: ['Active', 'Inactive'] });
  });
});

// A GCP label may carry an empty value (`goog-vertex-ai: ""`). Empty string is falsy, so
// a raw "" value renders as a blank, unreadable dropdown row and collapses to "no filter"
// through the UI's `|| null` / `|| undefined` / `&&` funnels. The data layer instead
// surfaces it as a readable "(empty)" option carrying a sentinel, and translates the
// sentinel back to "" when building the query. Regression guard for #40330.
describe('empty tag value is readable and filterable', () => {
  it('getDistinctTagValues renders an empty value as "(empty)" carrying the sentinel', async () => {
    query.mockResolvedValue({
      data: { data: { cloud_resource_v2: { rows: [{ tags: { 'goog-vertex-ai': '' } }] } } },
    });
    const options = await apiCloudAccount.getDistinctTagValues('acct', 'goog-vertex-ai', 'Cloud Storage');
    expect(options).toEqual([{ label: EMPTY_TAG_VALUE_LABEL, value: EMPTY_TAG_VALUE }]);
    // The sentinel is truthy, so it survives the dropdown's `value || null` funnel.
    expect(EMPTY_TAG_VALUE).toBeTruthy();
  });

  it('getDistinctTagValues leaves non-empty values untouched', async () => {
    query.mockResolvedValue({
      data: { data: { cloud_resource_v2: { rows: [{ tags: { tier: 'prod' } }, { tags: { tier: '' } }] } } },
    });
    const options = await apiCloudAccount.getDistinctTagValues('acct', 'tier', 'AmazonEC2');
    expect(options).toEqual([
      { label: EMPTY_TAG_VALUE_LABEL, value: EMPTY_TAG_VALUE },
      { label: 'prod', value: 'prod' },
    ]);
  });

  it('getCloudResource translates the empty-value sentinel back to "" and never leaks it', async () => {
    await apiCloudAccount.getCloudResource({ account_id: 'acct', tagFilterKey: 'goog-vertex-ai', tagFilterValue: EMPTY_TAG_VALUE });
    const builtQuery: string = query.mock.calls[0][0];
    expect(builtQuery).toContain('goog-vertex-ai');
    // The `_contains` clause must target an empty value, not the opaque sentinel.
    expect(builtQuery).not.toContain(EMPTY_TAG_VALUE);
    expect(builtQuery).toContain('{\\"goog-vertex-ai\\":\\"\\"}');
  });

  it('getCloudResource keeps a real tag value verbatim', async () => {
    await apiCloudAccount.getCloudResource({ account_id: 'acct', tagFilterKey: 'tier', tagFilterValue: 'prod' });
    const builtQuery: string = query.mock.calls[0][0];
    expect(builtQuery).toContain('{\\"tier\\":\\"prod\\"}');
  });
});
