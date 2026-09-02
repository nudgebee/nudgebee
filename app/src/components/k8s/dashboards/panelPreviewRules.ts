/** The rules behind the panel editor's live preview, kept pure so they can be tested. */
import { isCommandDatasource, type Panel } from '@api1/dashboards';
import { convertNumberToTimestamp } from 'src/utils/common';
import { draftFromQuery, findColumn, findTable, tablesFor, type EntityColumn } from './entityQuery';
import { labelEntityColumns, type PanelData, type PanelTable } from './usePanelData';

/**
 * How long the author has to stop typing before the preview queries. Provider
 * calls are billed per call, so a burst of typing must cost one request.
 */
export const PREVIEW_DEBOUNCE_MS = 700;

/** The window the preview runs over when the host has no range of its own. */
export const PREVIEW_FALLBACK_RANGE_MS = 60 * 60 * 1000;

/**
 * Whether a draft has enough on it to be worth running — the editor's `canSave`
 * rule MINUS the title, since a query is previewable long before it is named.
 */
export function isRunnable(panel: Panel): boolean {
  if (panel.type === 'text') return true;
  const scoped = Boolean(panel.account_type) || (panel.account_ids || []).length > 0;
  if (!scoped) return false;
  const target = panel.targets?.[0];
  // Entity datasources are authored as a structured query; the rest carry text.
  if (tablesFor(panel.datasource).length > 0) return Boolean(target?.query);
  return (target?.expr || '').trim().length > 0;
}

/** Whether this datasource waits for an explicit Run. */
export function usesManualRun(datasource: Panel['datasource']): boolean {
  return isCommandDatasource(datasource);
}

/**
 * What the preview depends on apart from the query text. These change by a
 * click, so they apply at once instead of waiting out the debounce.
 */
export function discreteSignature(panel: Panel): string {
  return JSON.stringify([panel.datasource, panel.type, panel.account_type, panel.account_ids]);
}

/** Everything the preview query depends on. Equal signatures cannot differ in result. */
export function fetchSignature(panel: Panel): string {
  return JSON.stringify([discreteSignature(panel), panel.targets]);
}

const SAMPLE_POINTS = 40;
/** Named so nobody reads them as their own data. Also the fallback when nothing more specific is known. */
const SAMPLE_LABELS = ['Sample A', 'Sample B'];

/** Deterministic stand-in for `Math.random`, which would redraw on every keystroke. */
function jitter(i: number, phase: number): number {
  const x = Math.sin(i * 12.9898 + phase * 78.233) * 43758.5453;
  return x - Math.floor(x);
}

/**
 * Stand-in data for a panel that has no query yet, so the editor opens on the chosen visualisation rather
 * than an empty box.
 */
export function sampleData(startTime: number, endTime: number, labels: string[] = SAMPLE_LABELS): PanelData {
  const step = (endTime - startTime) / (SAMPLE_POINTS - 1);
  const axis = Array.from({ length: SAMPLE_POINTS }, (_v, i) => startTime + i * step);
  return {
    labels: axis.map(convertNumberToTimestamp),
    timestamps: axis,
    series: labels.map((label, phase) => ({
      label,
      values: Array.from({ length: SAMPLE_POINTS }, (_v, i) => {
        const wave = Math.sin((i / SAMPLE_POINTS) * Math.PI * 3 + phase * 1.7) * 18 + 50 - phase * 12;
        return Math.round((wave + jitter(i, phase) * 12) * 10) / 10;
      }),
    })),
  };
}

function promqlGroupField(expr: string): string | undefined {
  const field = expr
    .match(/\bby\s*\(([^)]+)\)/i)?.[1]
    ?.split(',')[0]
    ?.trim();
  return field || undefined;
}

function humanize(field: string): string {
  const words = field.replace(/_/g, ' ').trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function sampleSeriesLabels(panel: Panel, primaryAccountLabel?: string): string[] {
  if (panel.datasource !== 'metrics') return SAMPLE_LABELS;
  const expr = panel.targets?.[0]?.expr;
  if (!expr) return SAMPLE_LABELS;
  const field = promqlGroupField(expr);
  // Ungrouped — a real result would be exactly one line, from one real account.
  if (!field) return [primaryAccountLabel || 'Sample'];
  const label = humanize(field);
  return [`${label} A`, `${label} B`];
}

const SAMPLE_TABLE_ROWS = 4;
const SAMPLE_WORDS = ['Alpha', 'Beta', 'Gamma', 'Delta', 'Epsilon', 'Zeta'];

function wordSeed(name: string): number {
  let seed = 0;
  for (let i = 0; i < name.length; i++) seed = (seed * 31 + name.charCodeAt(i)) % SAMPLE_WORDS.length;
  return seed;
}

function sampleCell(column: EntityColumn, row: number, startTime: number, endTime: number): string {
  const phase = wordSeed(column.name);
  switch (column.type) {
    case 'number': {
      const base =
        column.format === 'currency'
          ? 800
          : column.format === 'memory'
          ? 8192
          : column.format === 'cpu'
          ? 8
          : column.format === 'duration'
          ? 2e9
          : 40;
      return String(Math.round(base * (0.3 + jitter(row, phase)) * 100) / 100);
    }
    case 'boolean':
      return row % 2 === 0 ? 'true' : 'false';
    case 'datetime':
      return new Date(startTime + jitter(row, phase) * (endTime - startTime)).toISOString();
    case 'json':
      return '';
    default:
      return SAMPLE_WORDS[(row + phase) % SAMPLE_WORDS.length];
  }
}

export function sampleTableData(panel: Panel, startTime: number, endTime: number): PanelTable | undefined {
  if (tablesFor(panel.datasource).length === 0) return undefined;
  const stored = panel.targets?.[0]?.query;
  if (!stored) return undefined;
  const draft = draftFromQuery(stored);
  const table = findTable(draft.table);
  const columns = draft.columns.map((name) => findColumn(table, name)).filter((c): c is EntityColumn => Boolean(c));
  if (columns.length === 0) return undefined;

  const rows = Array.from({ length: SAMPLE_TABLE_ROWS }, (_v, row) => columns.map((c) => sampleCell(c, row, startTime, endTime)));
  return labelEntityColumns({ columns: columns.map((c) => c.name), rows }, draft);
}

/** Compact description of the window the preview ran over. */
export function previewRangeLabel(startTime: number, endTime: number): string {
  const minutes = Math.max(1, Math.round((endTime - startTime) / 60000));
  if (minutes < 90) return `Last ${minutes} min`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `Last ${hours} h`;
  return `Last ${Math.round(hours / 24)} d`;
}
