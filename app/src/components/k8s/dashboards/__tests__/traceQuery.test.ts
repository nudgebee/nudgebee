import { normaliseTraceTimestamp, toTraceParams } from '../traceQuery';
import { filterableColumns, findTable } from '../entityQuery';

describe('toTraceParams', () => {
  it('maps a filter row onto the named parameter the traces API takes', () => {
    const { params, unsupported } = toTraceParams([
      { column: 'workload_namespace', operator: '_in', value: 'prod, staging' },
      { column: 'workload_name', operator: '_eq', value: 'api' },
      { column: 'span_name', operator: '_eq', value: 'GET /orders' },
      { column: 'duration_ns', operator: '_gte', value: '5000000' },
    ]);
    expect(params.namespace).toEqual(['prod', 'staging']);
    expect(params.workload).toEqual(['api']);
    expect(params.selectedHttpSpan).toBe('GET /orders');
    expect(params.duration).toBe(5000000);
    expect(unsupported).toEqual([]);
  });

  it('sends one HTTP status as a string and several as a list', () => {
    // The API branches on Array.isArray, using _eq or _in accordingly.
    expect(toTraceParams([{ column: 'http_status_code', operator: '_eq', value: '500' }]).params.selectedHttpStatus).toBe('500');
    expect(toTraceParams([{ column: 'http_status_code', operator: '_in', value: '500, 503' }]).params.selectedHttpStatus).toEqual(['500', '503']);
  });

  it('ignores rows with no value', () => {
    // An unfinished filter row is not a filter on the empty string.
    const { params } = toTraceParams([{ column: 'workload_name', operator: '_eq', value: '   ' }]);
    expect(params.workload).toEqual([]);
  });

  it('reports a column it cannot express instead of dropping it', () => {
    const { unsupported } = toTraceParams([{ column: 'span_id', operator: '_eq', value: 'abc' }]);
    expect(unsupported).toEqual(['span_id']);
  });

  it('can express every column the builder lets you filter on', () => {
    // The builder offers `filterable` columns; this is the check that the two
    // lists have not drifted apart — a drift means a filter the panel shows and
    // then silently ignores.
    for (const table of ['traces_v2', 'traces_groupings_v2']) {
      for (const column of filterableColumns(findTable(table))) {
        const { unsupported } = toTraceParams([{ column: column.name, operator: '_eq', value: 'x' }]);
        expect([table, column.name, unsupported]).toEqual([table, column.name, []]);
      }
    }
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
