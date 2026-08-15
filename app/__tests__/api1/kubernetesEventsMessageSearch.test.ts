/**
 * Regression coverage for the Events "Search by message" filter at the query
 * layer: the free-text term has to survive into the list, count and grouping
 * where-clauses (including when the time filter is switched off) and has to
 * narrow the demo-tenant fixtures the same way.
 */
const mockQueryGraphQL = jest.fn();
jest.mock('@lib/HttpService', () => {
  const actual = jest.requireActual('@lib/HttpService');
  return {
    __esModule: true,
    ...actual,
    queryGraphQL: (...args: any[]) => mockQueryGraphQL(...args),
  };
});

const mockGetMockData = jest.fn();
jest.mock('@api1/mock', () => ({
  __esModule: true,
  default: (...args: any[]) => mockGetMockData(...args),
}));

import k8sApi from '@api1/kubernetes';

const baseQuery = {
  account_id: ['acc-1'],
  startDate: new Date('2026-08-01T00:00:00Z'),
  endDate: new Date('2026-08-15T00:00:00Z'),
};

const sentQuery = (call = 0) => mockQueryGraphQL.mock.calls[call][0] as string;

beforeEach(() => {
  jest.clearAllMocks();
  mockQueryGraphQL.mockResolvedValue({
    data: { data: { events: { rows: [] }, events_aggregate: { rows: [{ count: 0 }] }, event_groupings: { rows: [] } } },
  });
});

describe('k8s events message search — where clause', () => {
  it('adds a case-insensitive title match to the list query', async () => {
    await k8sApi.getK8sEvents(10, 0, { ...baseQuery, messageSearch: 'oom killed', onlyData: true });
    expect(sentQuery()).toContain('{title:{_ilike:"%oom killed%"}}');
  });

  it('adds the same match to the count query so totals agree with the rows', async () => {
    await k8sApi.getK8sEventsCount({ ...baseQuery, messageSearch: 'oom killed' });
    expect(sentQuery()).toContain('{title:{_ilike:"%oom killed%"}}');
  });

  it('adds the match to the groupings query behind the trend chart', async () => {
    await k8sApi.getK8sEventGroupings(1000, 0, { account_id: ['acc-1'], messageSearch: 'oom killed' });
    expect(sentQuery()).toContain('title:{_ilike:"%oom killed%"}');
  });

  it('keeps the match when the time filter is disabled', async () => {
    // `timeFilter: false` used to delete the whole `_and` array, taking the
    // message-search clause with it.
    await k8sApi.getK8sEvents(10, 0, { ...baseQuery, messageSearch: 'oom killed', timeFilter: false, onlyData: true });
    expect(sentQuery()).toContain('{title:{_ilike:"%oom killed%"}}');
    expect(sentQuery()).not.toContain('created_at:{_gte');
    expect(sentQuery()).not.toContain('created_at:{_lte');
  });

  it('ignores a blank search term', async () => {
    await k8sApi.getK8sEvents(10, 0, { ...baseQuery, messageSearch: '   ', onlyData: true });
    expect(sentQuery()).not.toContain('_ilike');
  });
});

describe('k8s events message search — demo tenant', () => {
  const demoEvents = [
    { id: '1', title: 'Pod OOM killed', priority: 'HIGH', status: 'FIRING' },
    { id: '2', title: 'Disk pressure on node', priority: 'HIGH', status: 'FIRING' },
  ];

  beforeEach(() => {
    mockGetMockData.mockResolvedValue({
      AllEvents: { list_k8_issues_data: { data: { events: demoEvents } } },
    });
  });

  it('narrows the demo fixtures by message', async () => {
    const res: any = await k8sApi.getK8sEvents(10, 0, { account_id: 'demo', messageSearch: 'oom' });
    expect(res.data.events.map((e: any) => e.id)).toEqual(['1']);
    expect(res.data.events_aggregate.aggregate.count).toBe(1);
  });

  it('matches case-insensitively', async () => {
    const res: any = await k8sApi.getK8sEvents(10, 0, { account_id: 'demo', messageSearch: 'DISK' });
    expect(res.data.events.map((e: any) => e.id)).toEqual(['2']);
  });

  it('reports the matching count for the demo tenant', async () => {
    const res: any = await k8sApi.getK8sEventsCount({ account_id: 'demo', messageSearch: 'oom' });
    expect(res.count).toBe(1);
  });

  it('returns everything when no search is applied', async () => {
    const res: any = await k8sApi.getK8sEvents(10, 0, { account_id: 'demo' });
    expect(res.data.events).toHaveLength(2);
  });
});
