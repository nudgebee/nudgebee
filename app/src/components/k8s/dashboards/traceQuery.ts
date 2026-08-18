import apiTrace from '@api1/kubernetes/trace';
import { formatDurationInTrace } from 'src/utils/common';
import type { PanelQueryResult } from '@api1/dashboards';
import { findTable, type EntityFilter, type EntityQueryDraft, type EntityTable } from './entityQuery';

/**
 * Runs a traces panel through the traces service (`/rpc/traces`) rather than
 * the generic query engine.
 *
 * The engine can read `traces_v2`, but the traces service is what knows how to
 * reach a given account's span store — the per-account Elasticsearch index, the
 * ClickHouse source, the span-vs-root-span views. Going around it worked only
 * for accounts whose defaults happened to be right.
 *
 * The cost is that filters are NAMED parameters, not a free where clause, so
 * the builder offers exactly the columns these two calls accept — see
 * `filterable` in entityQuery.ts.
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

/** One filter row, reduced to the named parameter the traces API expects. */
interface TraceFilterParams {
  namespace: string[];
  workload: string[];
  destinationNamespace: string[];
  destinationWorkload: string[];
  destinationName: string;
  selectedHttpStatus: string | string[];
  selectedHttpSpan: string;
  selectedStatusCode: string;
  resource: string;
  duration: number | null;
  traceSource: string;
  traceId: string | string[];
}

function emptyParams(): TraceFilterParams {
  return {
    namespace: [],
    workload: [],
    destinationNamespace: [],
    destinationWorkload: [],
    destinationName: '',
    selectedHttpStatus: '',
    selectedHttpSpan: '',
    selectedStatusCode: '',
    resource: '',
    duration: null,
    traceSource: '',
    traceId: '',
  };
}

/** Splits a filter value into the list form the `_in` parameters take. */
function asList(value: string): string[] {
  return value
    .split(',')
    .map((v) => v.trim())
    .filter(Boolean);
}

/**
 * Maps the builder's filter rows onto the traces API's named parameters.
 *
 * Unmappable rows are reported rather than dropped silently — the builder only
 * offers filterable columns, so a leftover means the two lists drifted.
 */
export function toTraceParams(filters: EntityFilter[]): { params: TraceFilterParams; unsupported: string[] } {
  const params = emptyParams();
  const unsupported: string[] = [];

  for (const filter of filters) {
    const value = filter.value.trim();
    if (!value) continue;
    switch (filter.column) {
      case 'workload_namespace':
        params.namespace = asList(value);
        break;
      case 'workload_name':
        params.workload = asList(value);
        break;
      case 'destination_workload_namespace':
        params.destinationNamespace = asList(value);
        break;
      case 'destination_workload_name':
        params.destinationWorkload = asList(value);
        break;
      case 'destination_name':
        params.destinationName = value;
        break;
      case 'http_status_code':
        params.selectedHttpStatus = asList(value).length > 1 ? asList(value) : value;
        break;
      case 'span_name':
        params.selectedHttpSpan = value;
        break;
      case 'status_code':
        params.selectedStatusCode = value;
        break;
      case 'resource':
        // The API wraps this in %…% itself.
        params.resource = value;
        break;
      case 'duration_ns': {
        // `Number(value) || null` folded a legitimate 0 into "no filter", since
        // 0 is falsy — a duration filter of 0 asked for everything instead.
        const duration = Number(value);
        params.duration = Number.isFinite(duration) ? duration : null;
        break;
      }
      case 'trace_source':
        params.traceSource = value;
        break;
      case 'trace_id':
        params.traceId = asList(value).length > 1 ? asList(value) : value;
        break;
      default:
        unsupported.push(filter.column);
    }
  }
  return { params, unsupported };
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
  const { params, unsupported } = toTraceParams(draft.filters);
  // The API parses these back with `new Date(x).getTime()`.
  const startDate = new Date(startMs).toISOString();
  const endDate = new Date(endMs).toISOString();
  const sortOrder = draft.sortDesc ? 'desc' : 'asc';
  const columns = draft.columns.filter((name) => table.columns.some((c) => c.name === name));

  if (draft.table === 'traces_groupings_v2') {
    const response = await apiTrace.traceGroupV2(
      accountId,
      params.namespace,
      params.workload,
      params.destinationNamespace,
      params.destinationWorkload,
      params.destinationName,
      draft.limit,
      0,
      startDate,
      endDate,
      Array.isArray(params.selectedHttpStatus) ? params.selectedHttpStatus[0] || '' : params.selectedHttpStatus,
      params.resource,
      '',
      draft.sortColumn,
      sortOrder
    );
    // The grouping call returns a fixed field list, so a column the builder
    // offers but the response omits would render as a blank column.
    const selected = columns.filter((name) => GROUPING_FIELDS.includes(name));
    return { ...toResult(table, selected, response?.traces_grouping_v3 || []), unsupported };
  }

  const response = await apiTrace.traceV2({
    accountId,
    namespace: params.namespace,
    workload: params.workload,
    destinationNamespace: params.destinationNamespace,
    destinationWorkload: params.destinationWorkload,
    destinationName: params.destinationName,
    limit: draft.limit,
    offset: 0,
    startDate,
    endDate,
    selectedHttpStatus: params.selectedHttpStatus,
    selectedHttpSpan: params.selectedHttpSpan,
    resource: params.resource,
    duration: params.duration,
    sortCol: draft.sortColumn,
    sortOrder,
    header: '',
    selectedStatusCode: params.selectedStatusCode,
    traceSource: params.traceSource,
    traceId: params.traceId,
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
