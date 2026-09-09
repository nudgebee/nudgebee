import { normaliseTraceTimestamp, runTracePanel, toTraceWhere } from '../traceQuery';
import { defaultDraft, filterableColumns, findTable } from '../entityQuery';

jest.mock('@api1/kubernetes/trace', () => ({
  __esModule: true,
  default: { traceV2: jest.fn(async () => ({ traces_list: [] })), traceGroupV2: jest.fn(async () => ({ traces_grouping_v3: [] })) },
}));
// eslint-disable-next-line @typescript-eslint/no-var-requires
const apiTrace = require('@api1/kubernetes/trace').default;

const spans = findTable('traces_v2');
const groupings = findTable('traces_groupings_v2');

describe('toTraceWhere', () => {
  it('keeps the operator the author picked', () => {
    // The bug this replaced: every filter was flattened onto a named API
    // parameter whose operator was hard-coded, so "Span name is not X" ran as
    // `span_name = X` — the exact opposite of what the panel said.
    const { where, unsupported } = toTraceWhere(spans, [{ column: 'span_name', operator: '_neq', value: 'GET /orders' }]);
    expect(where).toEqual({ span_name: { _neq: 'GET /orders' } });
    expect(unsupported).toEqual([]);
  });

  it('coerces a value to what its operator and column type expect', () => {
    const { where } = toTraceWhere(spans, [
      { column: 'workload_namespace', operator: '_not_in', value: 'prod, staging' },
      { column: 'duration_ns', operator: '_lte', value: '5000000' },
    ]);
    expect(where.workload_namespace).toEqual({ _not_in: ['prod', 'staging'] });
    // A number, not the string "5000000": the store compares against a numeric column.
    expect(where.duration_ns).toEqual({ _lte: 5000000 });
  });

  it('ANDs two rows on one column into a single range', () => {
    const { where } = toTraceWhere(spans, [
      { column: 'duration_ns', operator: '_gte', value: '1000000' },
      { column: 'duration_ns', operator: '_lte', value: '5000000' },
    ]);
    expect(where.duration_ns).toEqual({ _gte: 1000000, _lte: 5000000 });
  });

  it('ignores a row with no value', () => {
    // An unfinished filter row is not a filter on the empty string.
    expect(toTraceWhere(spans, [{ column: 'workload_name', operator: '_eq', value: '   ' }]).where).toEqual({});
  });

  it('reports a column the table cannot filter on instead of dropping it', () => {
    // A grouping's aggregates need a HAVING, which the builder has no surface for.
    const { where, unsupported } = toTraceWhere(groupings, [{ column: 'p99_latency', operator: '_gte', value: '1' }]);
    expect(where).toEqual({});
    expect(unsupported).toEqual(['p99_latency']);
  });

  it('can express every column the builder lets you filter on', () => {
    // The builder offers `filterable` columns; this is the check that the two
    // lists have not drifted apart — a drift means a filter the panel shows and
    // then silently ignores.
    for (const table of [spans, groupings]) {
      for (const column of filterableColumns(table)) {
        const { unsupported } = toTraceWhere(table, [{ column: column.name, operator: '_eq', value: 'x' }]);
        expect([table.value, column.name, unsupported]).toEqual([table.value, column.name, []]);
      }
    }
  });
});

describe('runTracePanel', () => {
  beforeEach(() => jest.clearAllMocks());

  it('sends the grouping filters as a where clause and no list parameters', async () => {
    await runTracePanel(
      { ...defaultDraft('traces_groupings_v2'), filters: [{ column: 'span_name', operator: '_nlike', value: '%health%' }] },
      'acc-1',
      1,
      2
    );
    const args = apiTrace.traceGroupV2.mock.calls[0];
    expect(args[args.length - 1]).toEqual({ span_name: { _nlike: '%health%' } });
    // The four list parameters must be '' and not []: the grouping call branches
    // on Array.isArray with no length check, so an empty array still emits
    // `_in: []` — harmless on ClickHouse, a terms query matching nothing on ES.
    expect([args[1], args[2], args[3], args[4]]).toEqual(['', '', '', '']);
  });

  it('sends the span filters as a where clause', async () => {
    await runTracePanel(
      { ...defaultDraft('traces_v2'), filters: [{ column: 'status_code', operator: '_neq', value: 'STATUS_CODE_OK' }] },
      'acc-1',
      1,
      2
    );
    const params = apiTrace.traceV2.mock.calls[0][0];
    expect(params.where).toEqual({ status_code: { _neq: 'STATUS_CODE_OK' } });
    expect(params.selectedStatusCode).toBe('');
  });

  it('does not select a filter-only column', async () => {
    // `trace_source` narrows a grouping but is not in its fixed response, so
    // asking for it back would render a blank column.
    const result = await runTracePanel({ ...defaultDraft('traces_groupings_v2'), columns: ['workload_name', 'trace_source'] }, 'acc-1', 1, 2);
    expect(result.column_names).toEqual(['workload_name']);
  });
});

describe('normaliseTraceTimestamp', () => {
  it('squares up the store’s space-separated nanosecond timestamp', () => {
    // `2026-08-05 14:00:11.999703144` + the Z that Datetime appends is Invalid
    // Date, which rendered as the raw string in the panel.
    const iso = normaliseTraceTimestamp('2026-08-05 14:00:11.999703144');
    expect(iso).toBe('2026-08-05T14:00:11.999');
    expect(Number.isNaN(new Date(iso + 'Z').getTime())).toBe(false);
  });

  it('leaves a value that already parses alone', () => {
    expect(normaliseTraceTimestamp('2026-08-05T14:00:11Z')).toBe('2026-08-05T14:00:11Z');
    expect(normaliseTraceTimestamp(1785919468796)).toBe('1785919468796');
  });

  it('keeps something unparseable verbatim rather than blanking it', () => {
    expect(normaliseTraceTimestamp('not a date')).toBe('not a date');
    expect(normaliseTraceTimestamp(null)).toBe('');
    expect(normaliseTraceTimestamp('')).toBe('');
  });
});
