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
