import apiTrace from '../trace';

/*
 * The server refuses a filter it will not run and says why — a regex outside
 * the supported subset, a trace provider that cannot filter that way. These
 * calls return only `data`, so for a caller that wrote the filter itself the
 * refusal used to arrive as an empty result: the dashboard panel said "Nothing
 * came back", which reads as "the filter matched nothing".
 */

const queryGraphQL = jest.fn();

jest.mock('@lib/HttpService', () => ({
  __esModule: true,
  queryGraphQL: (...args: unknown[]) => queryGraphQL(...args),
  gqlStringify: (value: unknown) => JSON.stringify(value),
}));

jest.mock('@api1/mock', () => ({ __esModule: true, default: jest.fn() }));

const REFUSAL = 'regex escape \\d is not supported: a backslash may only escape punctuation';
const refused = { data: { errors: [{ message: REFUSAL }], data: null } };
const where = { resource: { _regex: '\\d+' } };

const groups = (clause?: Record<string, Record<string, unknown>>) =>
  apiTrace.traceGroupV2(
    'acc-1',
    '',
    '',
    '',
    '',
    '',
    50,
    0,
    '2026-01-01T00:00:00Z',
    '2026-01-01T01:00:00Z',
    '',
    '',
    '',
    'count',
    'desc',
    undefined,
    clause
  );

const spans = (clause?: Record<string, Record<string, unknown>>) =>
  apiTrace.traceV2({
    accountId: 'acc-1',
    namespace: [],
    workload: [],
    destinationNamespace: [],
    destinationWorkload: [],
    destinationName: '',
    limit: 50,
    offset: 0,
    startDate: '2026-01-01T00:00:00Z',
    endDate: '2026-01-01T01:00:00Z',
    selectedHttpStatus: '',
    selectedHttpSpan: '',
    resource: '',
    duration: null,
    sortCol: 'timestamp',
    sortOrder: 'desc',
    header: '',
    selectedStatusCode: '',
    cols: ['span_name'],
    where: clause,
  } as Parameters<typeof apiTrace.traceV2>[0]);

describe('a trace query the server refused', () => {
  beforeEach(() => queryGraphQL.mockReset());

  it('is raised to a caller that wrote its own filter, with the reason', async () => {
    queryGraphQL.mockResolvedValue(refused);
    await expect(groups(where)).rejects.toThrow(REFUSAL);
    await expect(spans(where)).rejects.toThrow(REFUSAL);
  });

  it('stays an empty answer for the listings that pass no filter of their own', async () => {
    queryGraphQL.mockResolvedValue(refused);
    await expect(groups()).resolves.toEqual({});
    await expect(groups({})).resolves.toEqual({});
    await expect(spans()).resolves.toBeNull();
  });

  it('does not get in the way of an answer', async () => {
    queryGraphQL.mockResolvedValue({ data: { data: { traces_grouping_v3: [{ workload_name: 'edge' }] } } });
    await expect(groups(where)).resolves.toEqual({ traces_grouping_v3: [{ workload_name: 'edge' }] });
  });
});
