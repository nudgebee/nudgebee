import apiKubernetes from '@api1/kubernetes';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  ...jest.requireActual('@lib/HttpService'),
  queryGraphQL: jest.fn(),
}));

const mockQuery = queryGraphQL as jest.Mock;

// The query engine truncates a grouped datetime column to the day unless the request
// names a unit. Callers that need the time (the investigation panel's first/latest
// firing) pass a column transformation; everyone else must get the old query.
describe('apiKubernetes.getK8sEventGroupings column transformations', () => {
  beforeEach(() => {
    mockQuery.mockReset();
    mockQuery.mockResolvedValue({ data: { data: { event_groupings: { rows: [] }, event_groupings_aggregate: { rows: [] } } } });
  });

  const listCall = () => {
    const query: string = mockQuery.mock.calls[0][0];
    return query.slice(query.indexOf('event_groupings: '), query.indexOf('event_groupings_aggregate:'));
  };

  it('adds the transformation to the list query only', async () => {
    await apiKubernetes.getK8sEventGroupings(
      1,
      0,
      { account_id: 'acc-1', fingerprint: 'fp-1' },
      ['id', 'status', 'starts_at'],
      ['id', 'status', 'starts_at'],
      { name: 'starts_at', order: 'desc' },
      [{ name: 'starts_at', expr: 'date_unit', args: ['second'] }]
    );

    expect(listCall()).toContain('column_transformations:[{name:"starts_at",expr:"date_unit",args:["second"]}]');
    expect(mockQuery.mock.calls[0][0]).not.toContain('__COLUMN_TRANSFORMATIONS__');
  });

  it('leaves the query unchanged when none is given', async () => {
    await apiKubernetes.getK8sEventGroupings(1, 0, { account_id: 'acc-1', fingerprint: 'fp-1' }, ['id'], ['id']);

    expect(listCall()).not.toContain('column_transformations');
    expect(mockQuery.mock.calls[0][0]).not.toContain('__COLUMN_TRANSFORMATIONS__');
  });
});

// A group's rank and source severity are those of its newest event — what its row
// shows. Triage re-scores an issue while it keeps firing, so a filter that matched
// any event of the group listed the issue under every rank it had passed through,
// and the per-rank lists added up to more rows than the unfiltered list.
describe('apiKubernetes.getK8sEventGroupings rank and severity filters', () => {
  const GROUP_BY = ['tenant_id', 'account_id', 'fingerprint'];

  beforeEach(() => {
    mockQuery.mockReset();
    mockQuery.mockResolvedValue({ data: { data: { event_groupings: { rows: [] }, event_groupings_aggregate: { rows: [] } } } });
  });

  const fullQuery = (): string => mockQuery.mock.calls[0][0];
  const listCall = () => fullQuery().slice(fullQuery().indexOf('event_groupings: '), fullQuery().indexOf('event_groupings_aggregate:'));
  const countCall = () => fullQuery().slice(fullQuery().indexOf('event_groupings_aggregate:'));

  it('filters groups on their newest event instead of events on their own rank', async () => {
    await apiKubernetes.getK8sEventGroupings(20, 0, { account_id: 'acc-1', nb_priority: 'P2' }, GROUP_BY, [...GROUP_BY, 'latest_computed_priority']);

    expect(listCall()).toContain('where:{account_id:{_eq:"acc-1"}}, having:{latest_computed_priority:{_eq:"P2"}},');
    expect(fullQuery()).not.toContain('__HAVING__');
  });

  it('counts the groups that pass the filter, so the total matches the rows', async () => {
    await apiKubernetes.getK8sEventGroupings(20, 0, { account_id: 'acc-1', nb_priority: 'P2', latest_priority: 'HIGH' }, GROUP_BY, GROUP_BY);

    const having = 'having:{latest_computed_priority:{_eq:"P2"},latest_priority:{_eq:"HIGH"}},';
    expect(listCall()).toContain(having);
    expect(countCall()).toContain(`${having} group_by:["latest_computed_priority","latest_priority"]`);
    expect(countCall()).toContain('args: ["tenant_id","account_id","fingerprint"]');
    expect(countCall()).toContain('columns:["latest_computed_priority","latest_priority","event_count"]');
  });

  it('reads a filtered total with no matching group as zero', async () => {
    const none = await apiKubernetes.getK8sEventGroupings(20, 0, { account_id: 'acc-1', nb_priority: 'P0' }, GROUP_BY, GROUP_BY);
    expect(none.data.event_groupings_aggregate.aggregate.count).toBe(0);

    mockQuery.mockResolvedValue({ data: { data: { event_groupings: { rows: [] }, event_groupings_aggregate: { rows: [{ event_count: 7 }] } } } });
    const some = await apiKubernetes.getK8sEventGroupings(20, 0, { account_id: 'acc-1', nb_priority: 'P2' }, GROUP_BY, GROUP_BY);
    expect(some.data.event_groupings_aggregate.aggregate.count).toBe(7);
  });

  it('leaves both queries as they were when neither filter is set', async () => {
    await apiKubernetes.getK8sEventGroupings(20, 0, { account_id: 'acc-1', priority: 'HIGH' }, GROUP_BY, GROUP_BY);

    expect(fullQuery()).not.toContain('having');
    expect(listCall()).toContain('where:{account_id:{_eq:"acc-1"},priority:{_eq:"HIGH"}}, group_by:["tenant_id","account_id","fingerprint"]');
    expect(countCall()).toContain(
      'group_by:[], column_transformations:[{name: "event_count", expr: "distinct", args: ["tenant_id","account_id","fingerprint"]}], columns:["event_count"]'
    );
  });
});
