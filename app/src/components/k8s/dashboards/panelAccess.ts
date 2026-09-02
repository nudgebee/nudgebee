/**
 * What a panel needs the viewer to be granted before it can render.
 *
 * Every panel datasource resolves to a different backend read, and each one is
 * gated by a different dynamic-RBAC permission:
 *
 *   metrics                     metrics_list          → metrics:Read
 *   logs                        logs_list             → logs:Read
 *   traces                      traces_grouping_v3    → traces:Read
 *   redis / rabbitmq / postgresql  dashboards_execute_query        → dashboards:Execute
 *   nudgebee                    dashboards_execute_entity_query → dashboards:Execute,
 *                               then the query engine's own per-table module
 *                               (`PermissionModule` in query/metadata.go, mirrored
 *                               as `EntityTable.permissionModule`).
 *
 * Only ONE kind of user can be refused any of them: a grants-only custom-role
 * holder. A tenant-wide admin passes everything, and a user with a built-in
 * account/namespace role is gated by account access — which the panel already
 * reports ("No accounts are available to you"), not by a module grant. That is
 * why every check here is a no-op unless `isGrantsOnlyUser()`, exactly as the
 * Troubleshoot tabs and the nav gate do.
 *
 * ADVISORY only: the gateway and every handler re-check per request. The job
 * here is to stop an author building a panel that can only ever render Access
 * Denied, and to name the grant to ask for instead of a bare 403 later.
 */
import { hasPermission, isGrantsOnlyUser, missingPermissionMessage } from '@lib/auth';
import { findTable, type EntityTable } from './entityQuery';

/**
 * The permission each datasource's read path is gated on, as
 * `<module>:<Class>`.
 *
 * These are the classifications `@lib/permissionCatalog` gives the action each
 * datasource actually calls, which is what the in-app gateway checks — so a
 * grant named here is one that genuinely unblocks the panel. `nudgebee` needs
 * `dashboards:Execute` like the command datasources AND its table's own module;
 * `missingPanelGrant` reports the coarser one first, since that is the one to
 * ask for.
 */
const DATASOURCE_PERMISSION: Record<string, string> = {
  metrics: 'metrics:Read',
  logs: 'logs:Read',
  traces: 'traces:Read',
  redis: 'dashboards:Execute',
  rabbitmq: 'dashboards:Execute',
  postgresql: 'dashboards:Execute',
  nudgebee: 'dashboards:Execute',
  // `text` renders authored prose and reads nothing, so it is never gated.
};

/** Does the viewer hold `<module>:<class>`? Write implies Read, as every backend gate has it. */
function holds(permission: string): boolean {
  const [module, cls] = permission.split(':');
  if (hasPermission(module, cls as 'Read' | 'Write' | 'Execute')) return true;
  // Read is implied by Write everywhere this is checked server-side
  // (query/service.go, CanReadAccountData). Demanding the literal Read would be
  // stricter than the server and would grey a control out for someone it allows.
  return cls === 'Read' && hasPermission(module, 'Write');
}

/**
 * The permission the viewer is missing for this datasource, ignoring anything
 * table-specific. Undefined when nothing blocks them.
 */
export function missingDatasourceGrant(datasource: string): string | undefined {
  if (!isGrantsOnlyUser()) return undefined;
  const required = DATASOURCE_PERMISSION[datasource];
  if (!required) return undefined;
  return holds(required) ? undefined : required;
}

/**
 * The `<module>:Read` grant the viewer is missing for this query-engine table,
 * or undefined when nothing blocks them.
 *
 * Covers only the table dimension — a `nudgebee` panel also needs the
 * datasource's own grant, which `missingPanelGrant` checks first.
 */
export function missingTableGrant(table: EntityTable): string | undefined {
  // A table with no declared module is a registry gap, not a permission signal
  // — the Go-side invariant is pinned by TestEveryExecutableTableNamesAPermissionModule
  // and this one by entityQuery.test.ts. Gate nothing on it.
  if (table.datasource !== 'nudgebee' || !table.permissionModule) return undefined;
  if (!isGrantsOnlyUser()) return undefined;
  return holds(`${table.permissionModule}:Read`) ? undefined : `${table.permissionModule}:Read`;
}

/** True when the viewer may query this table (datasource grant aside). */
export function canQueryTable(table: EntityTable): boolean {
  return missingTableGrant(table) === undefined;
}

/** The tables of `tables` the viewer may query, in the order given. */
export function queryableTables(tables: EntityTable[]): EntityTable[] {
  return tables.filter(canQueryTable);
}

/** The query-engine table a stored panel target names, if it has one. */
function tableOfQuery(query: unknown): string {
  const table = (query as { table?: unknown } | null | undefined)?.table;
  return typeof table === 'string' ? table : '';
}

/**
 * The permission a panel's viewer is missing, or undefined when the panel can
 * render.
 *
 * Takes the panel shape both a saved `Panel` and a library widget's
 * `TemplatePanel` satisfy, so one function serves the editor, the widget library
 * and the template gallery.
 */
export function missingPanelGrant(panel: { datasource: string; targets?: { query?: Record<string, unknown> }[] }): string | undefined {
  const datasourceGrant = missingDatasourceGrant(panel.datasource);
  if (datasourceGrant) return datasourceGrant;
  if (panel.datasource !== 'nudgebee') return undefined;
  const name = tableOfQuery(panel.targets?.[0]?.query);
  if (!name) return undefined;
  // findTable falls back to the first table for an unknown name, which would
  // report the WRONG module — check the fallback actually is the table asked for.
  const table = findTable(name);
  if (table.value !== name) return undefined;
  return missingTableGrant(table);
}

/**
 * The tooltip for a control disabled by any of the checks above.
 *
 * Takes a list for the case where one control stands for several panels (a
 * dashboard template), where naming only the first would send the author back to
 * their admin twice.
 */
export function grantTooltip(permissions: string | string[]): string {
  const list = Array.isArray(permissions) ? permissions : [permissions];
  if (list.length === 1) return missingPermissionMessage(list[0]);
  return `You need these permissions: ${list.join(', ')}. Ask an admin to grant them.`;
}
