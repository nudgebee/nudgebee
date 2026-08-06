/**
 * `redis`, `rabbitmq` and `postgresql` are command datasources: they run a
 * read-only command through the relay and tabulate its output. They return a
 * snapshot rather than a series, so those panels are always tables and ignore
 * the time range.
 *
 * Each value is also the integration type looked up for the account, which is
 * why it is `postgresql` rather than `postgres`.
 */
export type PanelDatasource = 'metrics' | 'logs' | 'traces' | 'nudgebee' | 'redis' | 'rabbitmq' | 'postgresql';
export type PanelType = 'timeseries' | 'stat' | 'table' | 'bar' | 'text';

export const COMMAND_DATASOURCES: PanelDatasource[] = ['redis', 'rabbitmq', 'postgresql'];

export function isCommandDatasource(datasource: PanelDatasource): boolean {
  return COMMAND_DATASOURCES.includes(datasource);
}

/**
 * Datasources answered by the internal query engine. `nudgebee` reads the event
 * tables and `traces` the span tables — same builder, same action, different
 * allowlist (which the server enforces from the datasource, not the table).
 */
export const ENTITY_DATASOURCES: PanelDatasource[] = ['nudgebee', 'traces'];

export function isEntityDatasource(datasource: PanelDatasource): boolean {
  return ENTITY_DATASOURCES.includes(datasource);
}

export interface PanelTarget {
  ref_id: string;
  /** Raw provider expression (PromQL / Lucene / …) for metrics, logs, traces. */
  expr?: string;
  /** Internal query-engine request for `nudgebee` panels. */
  query?: Record<string, unknown>;
  /**
   * Column the dashboard's time range applies to on a `nudgebee` panel. A row
   * query has no inherent time axis, so without this the panel ignores the time
   * picker. Empty means no time filter.
   */
  time_column?: string;
  /**
   * Series naming, e.g. `{{route}}`. Not hand-editable — set by dashboard
   * import, which is where a legend format realistically comes from.
   */
  legend_format?: string;
  hide?: boolean;
}

export interface GridPos {
  x: number;
  y: number;
  /** Width in 12ths of the dashboard, mirroring the Grafana panel model. */
  w: number;
  h: number;
}

export interface Panel {
  id: number;
  title: string;
  description?: string;
  type: PanelType;
  datasource: PanelDatasource;
  /**
   * Accounts live on the PANEL, not the dashboard: every observability backend
   * resolves its provider integration from an account id, and one dashboard is
   * expected to compare several accounts side by side.
   *
   * Exactly one of `account_type` / `account_ids` is set on a non-text panel —
   * the backend rejects both-or-neither. `account_type` means "every account of
   * this provider", resolved at render against the accounts the viewer can see.
   */
  account_type?: string;
  account_ids?: string[];
  grid_pos: GridPos;
  targets?: PanelTarget[];
  unit?: string;
  /** Backs the `text` panel type. */
  content?: string;
  options?: Record<string, unknown>;
}

export interface DashboardDefinition {
  panels: Panel[];
  time_from?: string;
  refresh?: string;
}

export interface Dashboard {
  id: string;
  tenant_id?: string;
  slug?: string;
  title: string;
  description?: string;
  definition: DashboardDefinition;
  schema_version?: number;
  tags?: string[];
  status?: string;
  is_builtin?: boolean;
  created_by?: string;
  updated_by?: string;
  created_at?: string;
  updated_at?: string;
  version?: number;
}

export interface DashboardBinding {
  id?: string;
  dashboard_id?: string;
  scope_type: 'workload' | 'pod' | 'node' | 'namespace' | 'cluster' | 'cloud_resource' | 'service';
  match_kind: 'app_type' | 'name_regex' | 'label_selector' | 'resource_type' | 'all';
  match_value: Record<string, unknown>;
  priority: number;
}

export interface DashboardVersion {
  version: number;
  message?: string;
  created_by?: string;
  created_at?: string;
}

export interface DashboardSaveRequest {
  id?: string;
  title: string;
  description?: string;
  definition: DashboardDefinition;
  tags?: string[];
  status?: string;
  message?: string;
  bindings?: DashboardBinding[];
}

export interface DashboardListRequest {
  search?: string;
  limit?: number;
  offset?: number;
}

export interface DashboardResolveRequest {
  account_id: string;
  scope_type: string;
  name?: string;
  namespace?: string;
  app_type?: string;
}

export interface DashboardDeleteResult {
  id: string;
  deleted: boolean;
}

export interface PanelQueryRequest {
  account_id: string;
  datasource: PanelDatasource;
  /** The panel's command — arguments only; the credentialed prefix is server-side. */
  command: string;
}

export interface EntityQueryRequest {
  account_ids: string[];
  /** Decides which tables the query may name — events, or traces. */
  datasource: PanelDatasource;
  /** A query-engine QueryRequest, exactly as stored on the panel target. */
  query: Record<string, unknown>;
  time_column?: string;
  start_time?: number;
  end_time?: number;
}

export interface PanelQueryResult {
  columns: string[];
  rows: string[][];
  /** Set when the server capped the row count. */
  truncated?: boolean;
}

export const EMPTY_DEFINITION: DashboardDefinition = { panels: [] };

/**
 * One selectable account for a panel. `cloud_provider` comes straight off
 * GetCloudAccounts (K8S / AWS / GCP / AZURE …) and is what the panel editor's
 * Account type filter is built from — the set is derived from the connected
 * accounts, never hardcoded, so a new provider needs no frontend change.
 */
export interface AccountOption {
  label: string;
  value: string;
  cloud_provider: string;
}
