import apiDashboards, { type Panel } from '@api1/dashboards';
import apiTrace from '@api1/kubernetes/trace';
import { draftFromQuery, filterableColumns, findTable, renderEntityQuery, tablesFor, type EntityColumn, type EntityTable } from './entityQuery';
import { renderTemplate, type VariableValues } from './templating';

/**
 * Filters a VIEWER adds to one panel, beside its Account filter.
 *
 * The author's filters are part of the saved query; these are not. They live in
 * the panel's own state the way its Account pick does — narrowing what this
 * viewer sees right now, never what the panel is — and they only ever narrow:
 * every one is AND-ed onto the author's query, so a viewer cannot widen a panel
 * past what it was built to show.
 *
 * Offered on the traces and `nudgebee` datasources, whose columns are known up
 * front (entityQuery.ts). A metrics or logs panel holds a free-form PromQL or
 * Lucene expression, and filtering one would mean rewriting it.
 */
export interface ViewerFilter {
  column: string;
  /** Matched as "is one of". Empty means the filter is shown but not applied. */
  values: string[];
}

/**
 * The most values one filter's dropdown lists. Bounds the GROUP BY behind it, so
 * a high-cardinality column — a trace id — cannot pull a window's every row.
 */
export const VIEWER_FILTER_VALUE_LIMIT = 500;

/** The stored query a panel runs — the first target it does not hide, as usePanelData reads it. */
function storedQuery(panel: Panel): unknown {
  return (panel.targets || []).find((t) => !t.hide)?.query;
}

/** The table an entity panel reads, or undefined when the panel has no columns to offer. */
export function viewerFilterTable(panel: Panel): EntityTable | undefined {
  // A datasource with builder tables is an entity one — the editor's own test.
  if (tablesFor(panel.datasource).length === 0) return undefined;
  const stored = storedQuery(panel);
  if (!stored) return undefined;
  return findTable(draftFromQuery(stored).table);
}

/**
 * Columns a table can offer a viewer to filter by: text columns it can filter on.
 *
 * Text only, because the value comes from a list of what the column holds — a
 * duration or a count is a range question, which a list of values cannot ask.
 * Never an aggregate either: a count belongs in a HAVING, and the trace store
 * has no surface for one at all. The editor offers the author this same list.
 */
export function filterColumnsOf(table: EntityTable): EntityColumn[] {
  return filterableColumns(table).filter((c) => c.type === 'string' && !c.aggregate);
}

/** The columns the author chose for the menu, as saved. Empty means they chose none. */
export function savedFilterColumns(panel: Panel): string[] {
  const saved = panel.options?.filter_columns;
  return Array.isArray(saved) ? saved.filter((name): name is string => typeof name === 'string' && name !== '') : [];
}

/**
 * The columns a panel's "Filter by column" menu offers: the author's choice, in
 * their order, or every column the table can filter by when they made none.
 *
 * A saved name the table does not have — the panel was moved onto another table
 * by an import or a JSON edit — is skipped. When that leaves nothing, the full
 * list stands, so a stale choice cannot take the menu away.
 */
export function viewerFilterColumns(panel: Panel): EntityColumn[] {
  const table = viewerFilterTable(panel);
  if (!table) return [];
  const offered = filterColumnsOf(table);
  const chosen = savedFilterColumns(panel)
    .map((name) => offered.find((c) => c.name === name))
    .filter((c): c is EntityColumn => Boolean(c));
  return chosen.length > 0 ? chosen : offered;
}

/** The filters that actually narrow anything — one with no values picked is only on screen. */
export function appliedViewerFilters(filters: ViewerFilter[]): ViewerFilter[] {
  return filters.filter((f) => f.column && f.values.length > 0);
}

/**
 * Narrows a traces where clause (column → operator → value) by the viewer's
 * filters, in place. Returns false when nothing can match.
 *
 * The clause nests per column then per operator, so a viewer's `_in` on a column
 * the author already filtered with `_in` would OVERWRITE the author's list and
 * widen the panel. The two lists are intersected instead. An empty intersection
 * cannot be sent as `_in: []`: ClickHouse folds that to true and answers with
 * every row — the opposite of what it means — so the caller skips the query.
 */
export function narrowTraceWhere(where: Record<string, Record<string, unknown>>, filters: ViewerFilter[]): boolean {
  for (const filter of appliedViewerFilters(filters)) {
    const existing = where[filter.column]?._in;
    const values = Array.isArray(existing) ? filter.values.filter((v) => existing.some((e) => String(e) === v)) : filter.values;
    if (values.length === 0) return false;
    where[filter.column] = { ...where[filter.column], _in: values };
  }
  return true;
}

/**
 * Adds the viewer's filters to a `nudgebee` panel's rendered query.
 *
 * The where clause is a list AND-ed by the engine, so each filter is one more
 * clause beside the author's and both always apply. A where of any other shape
 * — a hand-edited import — is kept whole as the first clause rather than read
 * into.
 */
export function withViewerFilters(query: Record<string, unknown>, filters: ViewerFilter[]): Record<string, unknown> {
  const applied = appliedViewerFilters(filters);
  if (applied.length === 0) return query;
  const clauses = applied.map((f) => ({ _binary: { [f.column]: { _in: f.values } } }));
  const base = query.where as Record<string, unknown> | undefined;
  const flat = base && Object.keys(base).length === 1 && Array.isArray(base._and);
  const existing = flat ? (base._and as unknown[]) : base ? [base] : [];
  return { ...query, where: { _and: [...existing, ...clauses] } };
}

/** Distinct, non-empty, in reading order — numbers by value, so status 200 comes before 1000. */
function distinctValues(values: unknown[]): string[] {
  const seen = new Set<string>();
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = String(value);
    if (text !== '') seen.add(text);
  }
  return [...seen].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
}

interface ValuesRequest {
  panel: Panel;
  column: string;
  /** The accounts the panel is querying right now — its scope less any Account pick. */
  accountIds: string[];
  variables: VariableValues;
  startTime: number;
  endTime: number;
}

/**
 * The values a column holds in the panel's window, for its filter's dropdown.
 *
 * Traces ask the traces service for the column's label values on the panel's
 * one account. A `nudgebee` panel asks the query engine for that column alone,
 * under the panel's own where clause, so the list is what the panel could show.
 * On a grouping table the engine groups by the one column it was handed, which
 * makes the answer the column's distinct values; a row table takes no GROUP BY,
 * so the answer is the column across the most recent rows, de-duplicated — a
 * partial list, which is why the dropdown also takes a typed value.
 */
export async function loadViewerFilterValues({ panel, column, accountIds, variables, startTime, endTime }: ValuesRequest): Promise<string[]> {
  if (accountIds.length === 0) return [];

  if (panel.datasource === 'traces') {
    const values = await apiTrace.traceLabelValues(accountIds[0], column, startTime, endTime, VIEWER_FILTER_VALUE_LIMIT);
    return distinctValues(values);
  }

  const target = (panel.targets || []).find((t) => !t.hide);
  if (!target?.query) return [];
  const rendered = renderEntityQuery(target.query, (v) => renderTemplate(v, variables));
  const table = findTable(String(rendered.table || ''));
  const grouping = table.columns.some((c) => c.aggregate);
  const response = await apiDashboards.executeEntityQuery({
    account_ids: accountIds,
    datasource: panel.datasource,
    query: {
      table: rendered.table,
      columns: [{ name: column }],
      ...(rendered.where ? { where: rendered.where } : {}),
      // Newest first on a row table, so a capped list leans to what is current.
      order_by: [grouping || !table.defaultSort ? { column, order: 'asc' } : { column: table.defaultSort, order: 'desc' }],
      limit: VIEWER_FILTER_VALUE_LIMIT,
    },
    time_column: target.time_column,
    start_time: startTime,
    end_time: endTime,
  });
  if (response.errors || !response.data) {
    const first = Array.isArray(response.errors) ? (response.errors[0] as { message?: string }) : null;
    throw new Error(first?.message || 'Could not list the values.');
  }
  return distinctValues(response.data.rows.map((row) => row[0]));
}
