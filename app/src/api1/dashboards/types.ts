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
export type PanelType = 'timeseries' | 'stat' | 'gauge' | 'table' | 'bar' | 'text';

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

/**
 * Where a row of a table panel can take you. An object rather than a bare URL
 * string so link behaviour can grow — a tooltip, a condition, a target — without
 * changing the shape of the column that carries it.
 */
export interface PanelColumnLink {
  /**
   * An IN-APP path with `{{column}}` placeholders filled from the row, e.g.
   * `/investigate?id={{id}}&accountId={{account_id}}`. A panel is authored by
   * one user and rendered by every viewer, so the path is restricted to a
   * relative one — see isSafeLinkUrl, and the matching check in the server's
   * ValidateDefinition.
   */
  url: string;
}

/**
 * One column's display settings, and the single place they go.
 *
 * Every attribute is optional and additive: formatting, truncation, alignment
 * and whatever comes next are one more key here, never a new parallel array
 * keyed by column name — which is what `link_columns` / `hidden_columns` were,
 * and why they are gone (upgradeDefinition folds them forward on read).
 *
 * An entry with `name` configures a column the QUERY returns. An entry without
 * one describes a column the panel ADDS, which is why it needs a `title`.
 */
export interface PanelColumn {
  /** Query column this configures. Absent = a column the panel adds. */
  name?: string;
  /** Header text. On an added column it is also every cell's text. */
  title?: string;
  /** Absent means visible. Hidden columns are still queried — a link built from an id needs it. */
  visibility?: 'hidden';
  /**
   * Overrides how the value renders. `nudgebee` panels get this from the query
   * builder's column registry already, so it is for the cases the registry
   * cannot know — a cost column out of a Postgres panel, say.
   */
  format?: 'currency' | 'memory' | 'cpu' | 'duration' | 'number' | 'time' | 'text';
  /** Makes the cell a link. */
  link?: PanelColumnLink;
}

/** `options` on a panel that renders as a table. */
export interface PanelTableOptions {
  columns?: PanelColumn[];
  /**
   * Superseded by `columns`. Still read so an OLD EXPORT can be imported —
   * the server folds them forward on read, but an import arrives as raw JSON
   * that has never been through it. Never written.
   */
  link_columns?: { column?: string; title?: string; url: string }[];
  hidden_columns?: string[];
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
  /**
   * Which observability backend this panel's expression is written for, on a
   * metrics/logs/traces panel — `prometheus`, `ES`, `datadog`, …
   *
   * A panel holds ONE expression in ONE query language, but each account
   * resolves its own provider server-side, so a panel spanning a Prometheus
   * account and an Elasticsearch one can only ever be valid for one of them.
   * Naming it here sends the provider on the query instead of letting each
   * account fall back to its own default.
   *
   * Empty means "each account's own default" — what every panel written before
   * this field did, and still does.
   */
  provider?: string;
  /**
   * The Elasticsearch index (or wildcard pattern) this panel queries, when its
   * `provider` is ES.
   *
   * Empty means each account's own configured index — the per-account mapping,
   * else the integration's top-level one — which is what every panel resolved
   * before this field. Only meaningful alongside `provider`.
   */
  provider_index?: string;
  grid_pos: GridPos;
  targets?: PanelTarget[];
  unit?: string;
  /** Backs the `text` panel type. */
  content?: string;
  options?: PanelTableOptions & Record<string, unknown>;
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

/** What a listing that only links to dashboards reads — see listDashboardsBrief. */
export type DashboardSummary = Pick<Dashboard, 'id' | 'title' | 'description' | 'tags'>;

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
  /**
   * `cloud_accounts.status`, straight off `accounts_list`. Anything other than
   * `active` is a disabled account.
   *
   * Carried because nothing downstream filters on it: `get_cloud_accounts_v2`
   * returns disabled accounts alongside live ones, and the observability read
   * path never checks the column — a disabled account whose agent row still
   * exists resolves a provider and looks perfectly healthy. Optional because it
   * is additive: a caller that has not been updated treats every account as
   * live, which is exactly what happened before this field.
   */
  status?: string;
  /**
   * What the account manages — `kubernetes`, `cloud` or `vm` — straight off
   * `accounts_list`'s `account_type`.
   *
   * Deliberately NOT named `account_type`: on a Panel that field holds the
   * PROVIDER ("K8S"), and having the two mean different things under one name
   * is how a scope ends up pointing at the wrong accounts.
   *
   * Optional because it is additive — a caller that has not been updated simply
   * cannot pre-select by kind, rather than pre-selecting wrongly.
   */
  kind?: string;
}
