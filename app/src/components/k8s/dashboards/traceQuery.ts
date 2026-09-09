import apiTrace, { type TraceWhereClause } from '@api1/kubernetes/trace';
import { formatDurationInTrace } from 'src/utils/common';
import type { PanelQueryResult } from '@api1/dashboards';
import {
  coerceFilterValue,
  filterableColumns,
  findTable,
  operatorTakesValue,
  type EntityFilter,
  type EntityQueryDraft,
  type EntityTable,
} from './entityQuery';

/**
 * Runs a traces panel through the traces service (`/rpc/traces`) rather than
 * the generic query engine.
 *
 * The engine can read `traces_v2`, but the traces service is what knows how to
 * reach a given account's span store — the per-account Elasticsearch index, the
 * ClickHouse source, the span-vs-root-span views. Going around it worked only
 * for accounts whose defaults happened to be right.
 *
 * Filters travel as a WHERE CLAUSE, not as the traces API's named parameters.
 * Those parameters each hard-code an operator — `span_name` is always `_eq`,
 * `resource` always `_like '%…%'`, `duration_ns` always `_gte` — so routing the
 * builder through them meant the operator an author picked was read, shown, and
 * then thrown away: "Span name is not X" ran as `span_name = X`. The store
 * (both providers) accepts the full operator set, so the clause is built here
 * and merged in by the API layer.
 */

/** Fields `traces_grouping_v3` returns. The selection is fixed, unlike spans. */
const GROUPING_FIELDS = [
  'workload_name',
  'workload_namespace',
  'span_name',
  'resource',
  'destination_workload_name',
  'destination_workload_namespace',
  'destination_workload_zone',
  'http_status_code',
  'count',
  'error_count',
  'duration_ns',
  'p95_latency',
  'p99_latency',
  'max_latency',
];

/**
 * Compiles the builder's filter rows into the traces store's where clause.
 *
 * Every row keeps the operator the author picked. Rows are AND-ed, including
 * two rows on the same column (`duration_ns ≥ 1ms` and `duration_ns ≤ 5ms`
 * become one range), because the clause nests per column then per operator.
 *
 * A column the table does not mark filterable is reported rather than dropped
 * silently — the builder only offers filterable columns, so a leftover means a
 * stored panel names a column that has since stopped being one.
 */
export function toTraceWhere(table: EntityTable, filters: EntityFilter[]): { where: TraceWhereClause; unsupported: string[] } {
  const where: TraceWhereClause = {};
  const unsupported: string[] = [];
  const filterable = new Set(filterableColumns(table).map((c) => c.name));

  for (const filter of filters) {
    if (!filter.column || !filter.operator) continue;
    if (!filterable.has(filter.column)) {
      unsupported.push(filter.column);
      continue;
    }
    // An unfinished row is not a filter on the empty string.
    if (operatorTakesValue(filter.operator) && filter.value.trim() === '') continue;
    where[filter.column] = { ...where[filter.column], [filter.operator]: coerceFilterValue(table, filter) };
  }
  return { where, unsupported };
}

export interface TracePanelResult extends PanelQueryResult {
  /** Per column: 'time' cells render through the Datetime component. */
  column_kinds: ColumnKind[];
  /**
   * The query's own column names, since `columns` holds display labels. A
   * panel's links and hidden columns name these.
   */
  column_names: string[];
  /** Filters the traces API cannot express, if the builder ever offers one. */
  unsupported: string[];
}

export type ColumnKind = 'time' | 'text';

/**
 * Columns holding a nanosecond duration. The traces listings render these with
 * `formatDurationInTrace` — "1ms" rather than 1497105 — and a dashboard panel
 * showing the same data should read the same way.
 */
const DURATION_COLUMNS = new Set(['duration_ns', 'p50_latency', 'p95_latency', 'p99_latency', 'max_latency']);

/**
 * Makes a trace timestamp parseable.
 *
 * The store returns `2026-08-05 14:00:11.999703144` — space-separated, with
 * nanoseconds. `Datetime` appends a Z to a string with no timezone, and
 * `new Date('… 14:00:11.999703144Z')` is Invalid Date, so it is squared up to
 * ISO with millisecond precision first.
 */
export function normaliseTraceTimestamp(value: unknown): string {
  if (value === null || value === undefined || value === '') return '';
  if (typeof value === 'number') return String(value);
  const text = String(value);
  const iso = text.replace(' ', 'T').replace(/(\.\d{3})\d+/, '$1');
  return Number.isNaN(new Date(iso.endsWith('Z') ? iso : iso + 'Z').getTime()) ? text : iso;
}

/**
 * Fetches one traces panel for one account.
 *
 * Traces are per account — the API takes a single `accountId` — which is why a
 * traces panel resolves to exactly one, auto-selected or picked in the panel's
 * Account filter.
 */
export async function runTracePanel(draft: EntityQueryDraft, accountId: string, startMs: number, endMs: number): Promise<TracePanelResult> {
  const table = findTable(draft.table);
  const { where, unsupported } = toTraceWhere(table, draft.filters);
  // The API parses these back with `new Date(x).getTime()`.
  const startDate = new Date(startMs).toISOString();
  const endDate = new Date(endMs).toISOString();
  const sortOrder = draft.sortDesc ? 'desc' : 'asc';
  const columns = draft.columns.filter((name) => table.columns.some((c) => c.name === name && !c.filterOnly));

  if (draft.table === 'traces_groupings_v2') {
    // Every named parameter is passed empty: the filters are all in `where`.
    // '' rather than [] on the four list parameters is deliberate — the grouping
    // call branches on Array.isArray with no length check, so an empty array
    // still emits `_in: []`. ClickHouse folds that to `true`, but Elasticsearch
    // turns it into a terms query against nothing and the panel comes back empty.
    const response = await apiTrace.traceGroupV2(
      accountId,
      '',
      '',
      '',
      '',
      '',
      draft.limit,
      0,
      startDate,
      endDate,
      '',
      '',
      '',
      draft.sortColumn,
      sortOrder,
      undefined,
      where
    );
    // The grouping call returns a fixed field list, so a column the builder
    // offers but the response omits would render as a blank column.
    const selected = columns.filter((name) => GROUPING_FIELDS.includes(name));
    return { ...toResult(table, selected, response?.traces_grouping_v3 || []), unsupported };
  }

  const response = await apiTrace.traceV2({
    accountId,
    namespace: [],
    workload: [],
    destinationNamespace: [],
    destinationWorkload: [],
    destinationName: '',
    limit: draft.limit,
    offset: 0,
    startDate,
    endDate,
    selectedHttpStatus: '',
    selectedHttpSpan: '',
    resource: '',
    duration: null,
    sortCol: draft.sortColumn,
    sortOrder,
    header: '',
    selectedStatusCode: '',
    where,
    // Only the columns the panel shows: `cols` is spliced straight into the
    // GraphQL selection, so asking for fewer fetches less.
    cols: columns,
  });
  return { ...toResult(table, columns, response?.traces_list || []), unsupported };
}

/**
 * Rows keyed by column name → the positional table the panel renderer takes.
 *
 * Headers are the builder's LABELS ("Duration", not "duration_ns"), and each
 * column declares whether it is a timestamp, so the renderer can hand it to the
 * same Datetime component the traces listings use.
 */
function toResult(table: EntityTable, columns: string[], rows: any[]): PanelQueryResult & { column_kinds: ColumnKind[]; column_names: string[] } {
  const labels = columns.map((name) => table.columns.find((c) => c.name === name)?.label || name);
  const kinds: ColumnKind[] = columns.map((name) => (table.timeColumns.includes(name) ? 'time' : 'text'));
  return {
    columns: labels,
    column_kinds: kinds,
    // Headers are labels, so a panel's links and hidden columns — which name the
    // QUERY's columns — resolve against these instead.
    column_names: columns,
    rows: (rows || []).map((row) => columns.map((name) => formatCell(name, row?.[name], table))),
  };
}

function formatCell(column: string, value: unknown, table: EntityTable): string {
  if (value === null || value === undefined || value === '') return '';
  if (table.timeColumns.includes(column)) return normaliseTraceTimestamp(value);
  if (DURATION_COLUMNS.has(column)) {
    const ns = Number(value);
    return Number.isFinite(ns) ? formatDurationInTrace(ns) : String(value);
  }
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}
