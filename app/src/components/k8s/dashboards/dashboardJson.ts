/**
 * A dashboard edited as JSON — Grafana's "JSON model" — and read back into the
 * edit-mode draft.
 *
 * Both sides of the review diff are PROJECTED onto what the server stores: the
 * fields of its `dashboard.Panel` / `PanelTarget` / `Definition` models, in the
 * order its encoder writes them, empty values left out as its `omitempty` does,
 * and map-typed settings (`options`, `query`) with sorted keys as a Go map
 * encodes. Values are never rewritten. So the "current" side is the draft
 * exactly as saved, an unchanged dashboard reads back byte for byte, and key
 * order or whitespace is never a change.
 *
 * Projecting rather than re-building each panel matters: the importer's
 * converter reconstructs panels from what it recognises, and a panel it cannot
 * reconstruct — a text panel still carrying the accounts and options of the
 * table it used to be, a type this build does not render — would lose those
 * settings on BOTH sides of the diff at once, invisibly, and Apply would write
 * the loss back.
 *
 * The importer's panel rules still apply, to the panels the edit adds or
 * changes: a changed panel it would skip is refused rather than dropped. Panels
 * the edit leaves alone pass through as they are. What neither checks — a query
 * datasource on a non-table panel, a kubectl panel outside Kubernetes, the
 * command and entity allowlists, link URLs, a width past 12 — is left to the
 * server, whose message comes back into this editor when Save refuses.
 */
import type { AccountOption, DashboardDefinition, Panel } from '@api1/dashboards';
import { convertNativeDashboard, isNativeDashboard, panelSourceKey, parseImportJson } from './nativeImport';
import { referencedVariables } from './templating';

/** What the JSON editor reads and writes: the part of a dashboard its draft holds. */
export interface EditableDashboard {
  title: string;
  description: string;
  definition: DashboardDefinition;
}

/**
 * The server's `dashboard.Panel` fields (api-server/services/dashboard/model.go),
 * in declaration order, with whether each is `omitempty`. The legacy
 * `account_id` is not here: it is folded into `account_ids` before projecting,
 * as the server does on read.
 */
const PANEL_FIELDS: [string, boolean][] = [
  ['id', false],
  ['title', false],
  ['description', true],
  ['type', false],
  ['datasource', false],
  ['account_type', true],
  ['account_ids', true],
  ['grid_pos', false],
  ['provider', true],
  ['provider_index', true],
  ['targets', true],
  ['unit', true],
  ['content', true],
  ['options', true],
];
const TARGET_FIELDS: [string, boolean][] = [
  ['ref_id', false],
  ['expr', true],
  ['query', true],
  ['legend_format', true],
  ['time_column', true],
  ['hide', true],
];
const GRID_FIELDS = ['x', 'y', 'w', 'h'];
/** Fields the server keeps as a free-form map, and encodes with sorted keys. */
const MAP_FIELDS = new Set(['options', 'query']);

const PANEL_KEYS = new Set([...PANEL_FIELDS.map(([k]) => k), 'account_id']);
const TARGET_KEYS = new Set(TARGET_FIELDS.map(([k]) => k));
const DEFINITION_KEYS = new Set(['panels', 'time_from', 'refresh']);
/**
 * What the editor accepts at the top level. `accounts` is the Export's block of
 * account labels — accepted so an exported file can be pasted back, never shown
 * and never applied: it is derived from whoever exported, not stored.
 */
const TOP_KEYS = new Set(['title', 'description', 'definition', 'accounts']);

const isPlainObject = (v: unknown): v is Record<string, unknown> => Boolean(v) && typeof v === 'object' && !Array.isArray(v);

/** What Go's `omitempty` leaves out. */
function isEmpty(v: unknown): boolean {
  if (v === undefined || v === null || v === '' || v === false) return true;
  if (Array.isArray(v)) return v.length === 0;
  return isPlainObject(v) && Object.keys(v).length === 0;
}

/** A JSON value with every object's keys sorted, as a Go map encodes. */
function sortedDeep(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortedDeep);
  if (!isPlainObject(v)) return v;
  return Object.fromEntries(
    Object.keys(v)
      .sort()
      .map((k) => [k, sortedDeep(v[k])])
  );
}

function project(source: Record<string, unknown>, fields: [string, boolean][], shape: (key: string, value: unknown) => unknown) {
  const out: Record<string, unknown> = {};
  for (const [key, omitEmpty] of fields) {
    if (!(key in source)) continue;
    const value = shape(key, source[key]);
    if (omitEmpty && isEmpty(value)) continue;
    out[key] = value;
  }
  return out;
}

function projectTarget(target: unknown): unknown {
  if (!isPlainObject(target)) return target;
  return project(target, TARGET_FIELDS, (key, value) => (MAP_FIELDS.has(key) ? sortedDeep(value) : value));
}

/** One panel as the server stores it. Anything not panel-shaped is passed through for the validator to name. */
function projectPanel(panel: unknown): unknown {
  if (!isPlainObject(panel)) return panel;
  // The server's upgradeDefinition: a legacy single account becomes the list,
  // and blank ids are dropped, before anything is stored.
  let source: Record<string, unknown> = panel;
  if (typeof source.account_id === 'string' && source.account_id) {
    const { account_id: legacy, ...rest } = source;
    const scoped = (Array.isArray(rest.account_ids) && rest.account_ids.length > 0) || Boolean(rest.account_type);
    source = scoped ? rest : { ...rest, account_ids: [legacy] };
  }
  return project(source, PANEL_FIELDS, (key, value) => {
    if (key === 'account_ids' && Array.isArray(value)) return value.filter((id) => typeof id !== 'string' || id.trim() !== '');
    if (key === 'grid_pos' && isPlainObject(value)) return Object.fromEntries(GRID_FIELDS.filter((k) => k in value).map((k) => [k, value[k]]));
    if (key === 'targets' && Array.isArray(value)) return value.map(projectTarget);
    if (MAP_FIELDS.has(key)) return sortedDeep(value);
    return value;
  });
}

function projectDashboard(model: any): EditableDashboard {
  const definition = isPlainObject(model?.definition) ? model.definition : {};
  return {
    title: typeof model?.title === 'string' ? model.title : '',
    description: typeof model?.description === 'string' ? model.description : '',
    definition: {
      panels: (Array.isArray(definition.panels) ? definition.panels : []).map(projectPanel) as Panel[],
      ...(typeof definition.time_from === 'string' && definition.time_from ? { time_from: definition.time_from } : {}),
      ...(typeof definition.refresh === 'string' && definition.refresh ? { refresh: definition.refresh } : {}),
    },
  };
}

/** The dashboard as the editor shows it: what Save would store, in the order it is stored. */
export function dashboardJsonText(dashboard: EditableDashboard): string {
  return JSON.stringify(projectDashboard(dashboard), null, 2);
}

/** What reading the editor's text produced. */
export type DashboardJsonResult =
  | { ok: false; errors: string[] }
  | {
      ok: true;
      /** What Apply puts into the draft. */
      dashboard: EditableDashboard;
      /** The same, as the diff's "edited" side. */
      text: string;
      /** Things Apply will do that the author may not expect. None of them stop it. */
      warnings: string[];
      /** False when the edit changes nothing Save would store. */
      changed: boolean;
    };

const panelName = (panel: any, i: number) => (typeof panel?.title === 'string' && panel.title.trim() ? `"${panel.title.trim()}"` : `#${i + 1}`);

/**
 * Reads the editor's text against the dashboard it replaces.
 *
 * `accountOptions` are the accounts the viewer can see. A new or changed panel
 * naming any other account is refused here because Save would be refused for
 * it — the server checks read access on every account id — and here is where
 * the id can be fixed.
 */
export function readDashboardJson(text: string, accountOptions: Pick<AccountOption, 'value'>[], current: EditableDashboard): DashboardJsonResult {
  let model: any;
  try {
    model = parseImportJson(text);
  } catch (err) {
    return { ok: false, errors: [(err as Error).message] };
  }
  if (!isPlainObject(model.definition) || !Array.isArray(model.definition.panels)) {
    return {
      ok: false,
      errors: [
        isNativeDashboard(model)
          ? 'This is a single panel. Paste the whole dashboard here, or add the panel with Import.'
          : 'This is not a dashboard exported from here — it needs a "definition" with a "panels" list. A Grafana dashboard goes through Import.',
      ],
    };
  }

  const errors: string[] = [];
  const warnings: string[] = [];

  if (typeof model.title !== 'string' || !model.title.trim()) errors.push('The dashboard needs a "title".');
  for (const key of Object.keys(model)) {
    if (!TOP_KEYS.has(key)) warnings.push(`"${key}" is not a dashboard setting, so it is ignored.`);
  }
  for (const key of Object.keys(model.definition)) {
    if (!DEFINITION_KEYS.has(key)) warnings.push(`"definition.${key}" is not a dashboard setting, so it is ignored.`);
  }

  const dashboard = projectDashboard(model);
  const before = projectDashboard(current).definition.panels;
  const beforeById = new Map(before.map((p) => [p.id, JSON.stringify(p)]));

  const known = new Set(accountOptions.map((a) => a.value));
  const unknownAccounts = new Set<string>();
  const ids = new Map<number, number>();
  /** Panels the edit adds or changes — the only ones the panel rules are applied to. */
  const touched: any[] = [];

  model.definition.panels.forEach((raw: unknown, i: number) => {
    if (!isPlainObject(raw)) {
      errors.push(`Panel #${i + 1} is not a panel object.`);
      return;
    }
    for (const key of Object.keys(raw)) {
      if (!PANEL_KEYS.has(key)) warnings.push(`Panel ${panelName(raw, i)}: "${key}" is not a panel setting, so it is dropped.`);
    }
    for (const target of Array.isArray(raw.targets) ? raw.targets : []) {
      for (const key of isPlainObject(target) ? Object.keys(target) : []) {
        if (!TARGET_KEYS.has(key)) warnings.push(`Panel ${panelName(raw, i)}: "${key}" is not a query setting, so it is dropped.`);
      }
    }
    const panel = dashboard.definition.panels[i] as any;
    const id = Number(panel.id);
    if (Number.isInteger(id) && id > 0) ids.set(id, (ids.get(id) || 0) + 1);
    if (beforeById.get(panel.id) === JSON.stringify(panel)) return;
    touched.push(panel);

    if (panel.type === 'text') return;
    // The importer gives an unscoped panel its default scope; an edit has none
    // to give, and the server refuses a panel with no accounts.
    if (!panelSourceKey(panel)) errors.push(`Panel ${panelName(panel, i)} names no accounts — set "account_type" or "account_ids".`);
    for (const accountId of Array.isArray(panel.account_ids) ? panel.account_ids : []) {
      if (typeof accountId === 'string' && !known.has(accountId)) unknownAccounts.add(accountId);
    }
  });
  if (unknownAccounts.size > 0) {
    errors.push(
      `${unknownAccounts.size === 1 ? 'This account is' : 'These accounts are'} not available to you here: ${[...unknownAccounts].join(', ')}. ` +
        'Use ids from this tenant — the listing’s Export shows them.'
    );
  }
  // The id is what a link to a panel names, and what the draft keeps it by.
  for (const [id, count] of ids) {
    if (count > 1) errors.push(`${count} panels share the id ${id}. Give each panel its own.`);
  }
  if (errors.length > 0) return { ok: false, errors };

  // The importer's panel rules, on what the edit touched — a changed panel it
  // would skip is refused rather than dropped.
  if (touched.length > 0) {
    const checked = convertNativeDashboard({ title: 'check', definition: { panels: touched } }, {});
    if (checked.skipped && checked.skipped.length > 0) return { ok: false, errors: checked.skipped };
  }

  // The importer's note on template variables, when the edit is what brings them in.
  // A hand-typed `expr` can be anything JSON allows; only a string can reference a variable.
  const variables = (panels: any[]) =>
    new Set(
      panels.flatMap((p) =>
        (Array.isArray(p?.targets) ? p.targets : []).flatMap((t: any) => (typeof t?.expr === 'string' ? referencedVariables(t.expr) : []))
      )
    );
  const had = variables(before);
  const brought = [...variables(touched)].filter((v) => !had.has(v));
  if (brought.length > 0) {
    warnings.push(
      `Queries reference ${brought
        .map((v) => `$${v}`)
        .join(', ')}. Those are filled in only when a dashboard is opened from a page that supplies them.`
    );
  }

  const beforeIds = new Set(before.map((p) => p.id));
  for (const old of before) {
    if (dashboard.definition.panels.some((p) => p.id === old.id)) continue;
    const moved = dashboard.definition.panels.find((p) => p.title === old.title && !beforeIds.has(p.id));
    if (moved) warnings.push(`Panel "${old.title}" now has id ${moved.id} (was ${old.id}) — links to it will stop working.`);
  }

  const edited = JSON.stringify(dashboard, null, 2);
  return { ok: true, dashboard, text: edited, warnings, changed: edited !== dashboardJsonText(current) };
}
