import apiTrace, { toFilterList } from '../trace';

/*
 * The Traces tab's `Service` filter is a single-select, so it sets its state to a bare
 * string where the multi-select dropdowns set a list. `_in` takes a list of values: sent
 * a scalar, Splunk Enterprise answered 400 and the list emptied the moment a service was
 * picked (#41493), while SolarWinds and Cloud Trace (#41419) dropped the predicate and
 * returned every span. traceV2 normalises both shapes to a list before building the query.
 */

const queryGraphQL = jest.fn();

jest.mock('@lib/HttpService', () => ({
  __esModule: true,
  queryGraphQL: (...args: unknown[]) => queryGraphQL(...args),
  gqlStringify: (value: unknown) => JSON.stringify(value),
}));

jest.mock('@api1/mock', () => ({ __esModule: true, default: jest.fn() }));

type Overrides = Partial<Parameters<typeof apiTrace.traceV2>[0]>;

const spans = (overrides: Overrides) =>
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
    ...overrides,
  } as Parameters<typeof apiTrace.traceV2>[0]);

// The GraphQL text carries the serialised request, so it is what the backend will read.
const sentQuery = () => queryGraphQL.mock.calls[0][0] as string;

describe('the Service filter value traceV2 sends', () => {
  beforeEach(() => {
    queryGraphQL.mockReset();
    queryGraphQL.mockResolvedValue({ data: { data: { traces_list: [] } } });
  });

  it('is a one-element list when the single-select picked one service', async () => {
    await spans({ workload: 'qa-payment-s3' });
    expect(sentQuery()).toContain('"workload_name":{"_in":["qa-payment-s3"]}');
  });

  it('is the list itself when a multi-select picked several', async () => {
    await spans({ workload: ['qa-payment-s3', 'qa-ledger'], namespace: 'qa' });
    expect(sentQuery()).toContain('"workload_name":{"_in":["qa-payment-s3","qa-ledger"]}');
    expect(sentQuery()).toContain('"workload_namespace":{"_in":["qa"]}');
  });

  it('is absent when nothing is picked, in either shape', async () => {
    await spans({ workload: '', namespace: [] });
    expect(sentQuery()).not.toContain('workload_name');
    expect(sentQuery()).not.toContain('workload_namespace');
  });

  /*
   * `?.[0]` on a string is its first CHARACTER, so this test used to pass the
   * same-source-and-destination branch on a shared leading `q` and send an `_or` of two
   * clauses instead of filtering source and destination independently.
   */
  it('does not treat two different services as the same one on a shared first letter', async () => {
    await spans({
      workload: 'qa-payment-s3',
      destinationWorkload: 'qa-ledger',
      namespace: 'qa',
      destinationNamespace: 'qa',
      fromWorkload: true,
    });
    // An empty `"_or":[]` always rides along on the count request; what must not appear
    // is a populated one, which is what the same-source-and-destination branch builds.
    expect(sentQuery()).not.toContain('"_or":[{');
    expect(sentQuery()).toContain('"workload_name":{"_in":["qa-payment-s3"]}');
    expect(sentQuery()).toContain('"destination_workload_name":{"_in":["qa-ledger"]}');
  });
});

describe('toFilterList', () => {
  it('normalises every shape a filter dropdown produces', () => {
    expect(toFilterList('one')).toEqual(['one']);
    expect(toFilterList(['one', 'two'])).toEqual(['one', 'two']);
    expect(toFilterList([])).toEqual([]);
    expect(toFilterList(undefined)).toEqual([]);
    expect(toFilterList(null)).toEqual([]);
  });

  // "Nothing selected" must read the same in both shapes and become no predicate, not a
  // filter for the empty string, which would narrow the list to nothing.
  it('drops blanks the same way whether they arrive as a scalar or in a list', () => {
    expect(toFilterList('')).toEqual([]);
    expect(toFilterList([''])).toEqual([]);
    expect(toFilterList(['one', ''])).toEqual(['one']);
  });
});
