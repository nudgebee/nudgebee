import type { DashboardDefinition, Panel, PanelDatasource, PanelTarget, PanelType } from '@api1/dashboards';
import type { PanelScope } from './panelAccounts';
import type { GrafanaImportResult } from './grafanaImport';
import { referencedVariables } from './templating';

/**
 * Reads back what this app exports — a dashboard file from the listing, or a
 * single panel file from a panel's menu.
 *
 * Distinct from the Grafana importer because nothing needs translating: the
 * file already IS our panel model, so this validates and re-scopes rather than
 * converting. The two live side by side instead of one branching converter —
 * a Grafana panel and one of ours share a `type` field and almost nothing else.
 *
 * The preview shape is shared with the Grafana path so the modal renders either
 * result without caring which produced it.
 */

const PANEL_TYPES = new Set<PanelType>(['timeseries', 'stat', 'table', 'bar', 'text']);
const DATASOURCES = new Set<PanelDatasource>(['metrics', 'logs', 'traces', 'nudgebee', 'redis', 'rabbitmq', 'postgresql']);
/** Datasources answered by the query engine, which store a `query` object rather than an `expr`. */
const ENTITY = new Set<PanelDatasource>(['nudgebee', 'traces']);

/** Parses pasted text into a model, with a message aimed at whoever pasted it. */
export function parseImportJson(text: string): any {
  let parsed: any;
  try {
    parsed = JSON.parse(text);
  } catch (err) {
    throw new Error(`That is not valid JSON: ${(err as Error).message}`);
  }
  if (!parsed || typeof parsed !== 'object') {
    throw new Error('Expected a dashboard object.');
  }
  // Grafana's "Export for sharing externally" and its API both wrap the model.
  return parsed.dashboard && typeof parsed.dashboard === 'object' ? parsed.dashboard : parsed;
}

/**
 * Whether this model came from HERE rather than from Grafana.
 *
 * Two tells, and neither can occur in a Grafana export: a `definition.panels`
 * document (Grafana's panels are top-level), or a bare panel carrying
 * `grid_pos` / a string `datasource` where Grafana writes `gridPos` and an
 * object.
 */
export function isNativeDashboard(model: any): boolean {
  if (!model || typeof model !== 'object') return false;
  if (model.definition && Array.isArray(model.definition.panels)) return true;
  return isNativePanel(model);
}

function isNativePanel(value: any): boolean {
  if (!value || typeof value !== 'object' || typeof value.type !== 'string') return false;
  return value.grid_pos !== undefined || typeof value.datasource === 'string';
}

/**
 * One distinct account scope found in an imported file, with the panels that
 * share it. The importer maps each of these onto a scope in THIS tenant, so a
 * dashboard spanning four accounts keeps its structure instead of collapsing
 * every panel onto one account.
 */
export interface SourceScope {
  /** Stable identity for the mapping table. */
  key: string;
  /** What the file says this scope is, using its own account labels when it carries them. */
  label: string;
  accountType?: string;
  accountIds: string[];
  panelCount: number;
}

/** Target scope chosen for each source scope, keyed by `SourceScope.key`. */
export type ScopeMapping = Record<string, PanelScope>;

/**
 * The scope key for one panel, or '' for a panel that names no accounts (a text
 * panel, or a file hand-edited to drop them).
 *
 * Ids are sorted so two panels naming the same accounts in a different order
 * are one scope, not two.
 */
export function panelSourceKey(panel: any): string {
  const type = typeof panel?.account_type === 'string' ? panel.account_type.trim() : '';
  const ids = (Array.isArray(panel?.account_ids) ? panel.account_ids : []).filter((id: unknown) => typeof id === 'string' && id);
  if (ids.length > 0) return `ids:${[...ids].sort().join(',')}`;
  if (type) return `type:${type}`;
  return '';
}

/**
 * Every distinct scope in a file, in the order first encountered, so the
 * mapping rows read in panel order rather than alphabetically.
 *
 * Account ids belong to whichever tenant exported the file and are opaque here,
 * so the export carries an `accounts` block naming them. Without it the row
 * still renders — as ids — rather than not rendering at all.
 */
export function collectSourceScopes(model: any): SourceScope[] {
  const panels: any[] = isNativePanel(model) && !model.definition ? [model] : model?.definition?.panels || [];
  const labels: Record<string, any> = model?.accounts && typeof model.accounts === 'object' ? model.accounts : {};
  const byKey = new Map<string, SourceScope>();

  for (const panel of panels) {
    if (panel?.type === 'text') continue;
    const key = panelSourceKey(panel);
    if (!key) continue;
    const existing = byKey.get(key);
    if (existing) {
      existing.panelCount += 1;
      continue;
    }
    const accountType = typeof panel.account_type === 'string' ? panel.account_type : '';
    const accountIds = (Array.isArray(panel.account_ids) ? panel.account_ids : []).filter((id: unknown) => typeof id === 'string' && id);
    byKey.set(key, {
      key,
      label: accountIds.length > 0 ? accountIds.map((id: string) => labels[id]?.label || id).join(', ') : `All ${accountType}`,
      accountType: accountType || undefined,
      accountIds,
      panelCount: 1,
    });
  }

  return [...byKey.values()];
}

export function convertNativeDashboard(model: any, scope: PanelScope, mappings: ScopeMapping = {}): GrafanaImportResult {
  const warnings: string[] = [];
  const singlePanel = isNativePanel(model) && !model.definition;
  const sources: any[] = singlePanel ? [model] : model.definition?.panels || [];

  const panels: Panel[] = [];
  const usedIds = new Set<number>();
  // Reported once rather than per panel: a 15-panel export names the same
  // account on every one of them.
  let defaulted = false;

  for (const source of sources) {
    // A scope the importer mapped wins; anything unmapped falls back to the
    // default, which is what every panel got before mapping existed.
    const key = panelSourceKey(source);
    const mapped = key ? mappings[key] : undefined;
    if (!mapped && key) defaulted = true;
    const panel = convertPanel(source, mapped || scope, usedIds, warnings);
    if (panel) panels.push(panel);
  }

  if (panels.length === 0) {
    warnings.push('No panel in this file could be imported.');
  }
  if (defaulted) {
    // Account ids are tenant-local, so a file's own ids are meaningless here —
    // silently keeping them would produce panels that save and then render
    // "No account".
    warnings.push(
      'Panels whose accounts were not mapped use the default chosen above — the accounts named in the file belong to whichever tenant exported it.'
    );
  }

  const variables = new Set<string>();
  for (const panel of panels) {
    for (const target of panel.targets || []) {
      for (const name of referencedVariables(target.expr || '')) variables.add(name);
    }
  }
  if (variables.size > 0) {
    warnings.push(
      `Queries reference ${[...variables].map((v) => `$${v}`).join(', ')}. Those are filled in only when a dashboard is opened from a page that ` +
        'supplies them — edit the panels to replace them with literal values.'
    );
  }

  const definition: DashboardDefinition = { panels };
  return {
    title: importTitle(model, singlePanel),
    description: typeof model.description === 'string' && !singlePanel ? model.description : '',
    tags: Array.isArray(model.tags) ? model.tags.filter((t: unknown) => typeof t === 'string') : [],
    definition,
    warnings,
  };
}

function importTitle(model: any, singlePanel: boolean): string {
  const title = typeof model.title === 'string' ? model.title.trim() : '';
  if (!title) return singlePanel ? 'Imported panel' : 'Imported dashboard';
  // A single panel becomes a one-panel dashboard, and a dashboard named
  // "Registered Runners" reads as if it were the panel itself.
  return singlePanel ? `${title} (imported panel)` : title;
}

/**
 * Validates one panel against the same rules the server enforces on save.
 *
 * A panel that would fail validation is dropped with a warning rather than
 * carried through, because the save is all-or-nothing: one bad panel rejects
 * the entire import with a message about that panel, and the other fourteen are
 * lost with it.
 */
function convertPanel(source: any, scope: PanelScope, usedIds: Set<number>, warnings: string[]): Panel | null {
  if (!source || typeof source !== 'object') return null;
  const title = typeof source.title === 'string' && source.title.trim() ? source.title.trim() : 'Untitled panel';

  const type = source.type as PanelType;
  if (!PANEL_TYPES.has(type)) {
    warnings.push(`Panel "${title}" was skipped — ${source.type || 'unknown'} is not a visualisation this build renders.`);
    return null;
  }

  const id = nextPanelId(source.id, usedIds);
  const grid_pos = convertGridPos(source.grid_pos);
  const description = typeof source.description === 'string' ? source.description : '';

  // A text panel carries prose: no datasource, no account, no query.
  if (type === 'text') {
    return { id, title, description, type, datasource: 'metrics', grid_pos, content: typeof source.content === 'string' ? source.content : '' };
  }

  const datasource = source.datasource as PanelDatasource;
  if (!DATASOURCES.has(datasource)) {
    warnings.push(`Panel "${title}" was skipped — "${source.datasource || 'none'}" is not a data source this build queries.`);
    return null;
  }

  const targets = convertTargets(source.targets, datasource);
  if (targets.length === 0) {
    warnings.push(`Panel "${title}" was skipped — it has no ${ENTITY.has(datasource) ? 'query' : 'expression'}.`);
    return null;
  }

  return {
    id,
    title,
    description,
    type,
    datasource,
    // The file's own scope is dropped in favour of the importer's choice.
    ...scope,
    grid_pos,
    targets,
    unit: typeof source.unit === 'string' ? source.unit : '',
    ...(source.options && typeof source.options === 'object' ? { options: source.options } : {}),
  };
}

function convertTargets(targets: any, datasource: PanelDatasource): PanelTarget[] {
  const out: PanelTarget[] = [];
  for (const [i, target] of (Array.isArray(targets) ? targets : []).entries()) {
    if (!target || typeof target !== 'object') continue;
    const expr = typeof target.expr === 'string' ? target.expr.trim() : '';
    const query = target.query && typeof target.query === 'object' ? target.query : undefined;
    // An entity panel is driven by its query object and a text-query panel by
    // its expression; a target carrying neither runs nothing.
    if (ENTITY.has(datasource) ? !query : !expr) continue;
    out.push({
      ref_id: typeof target.ref_id === 'string' && target.ref_id ? target.ref_id : String.fromCharCode(65 + i),
      ...(expr ? { expr } : {}),
      ...(query ? { query } : {}),
      ...(typeof target.legend_format === 'string' && target.legend_format ? { legend_format: target.legend_format } : {}),
      ...(typeof target.time_column === 'string' && target.time_column ? { time_column: target.time_column } : {}),
      ...(target.hide ? { hide: true } : {}),
    });
  }
  return out;
}

/** Clamps a stored geometry back into the grid, in case the file was hand-edited. */
export function convertGridPos(gridPos: any) {
  const width = Number(gridPos?.w);
  const height = Number(gridPos?.h);
  return {
    x: Math.max(Number(gridPos?.x) || 0, 0),
    y: Math.max(Number(gridPos?.y) || 0, 0),
    w: Number.isFinite(width) && width > 0 ? Math.min(Math.round(width), 12) : 12,
    h: Number.isFinite(height) && height > 0 ? Math.round(height) : 8,
  };
}

/** Panel ids only have to be unique inside one dashboard; keep them when free. */
function nextPanelId(sourceId: unknown, used: Set<number>): number {
  const id = Number(sourceId);
  if (Number.isInteger(id) && id > 0 && !used.has(id)) {
    used.add(id);
    return id;
  }
  let next = 1;
  while (used.has(next)) next++;
  used.add(next);
  return next;
}
