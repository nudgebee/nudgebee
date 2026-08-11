/** The rules behind the panel editor's live preview, kept pure so they can be tested. */
import { isCommandDatasource, type Panel } from '@api1/dashboards';
import { convertNumberToTimestamp } from 'src/utils/common';
import { tablesFor } from './entityQuery';
import type { PanelData } from './usePanelData';

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
/** Named so nobody reads them as their own data. */
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
export function sampleData(startTime: number, endTime: number): PanelData {
  const step = (endTime - startTime) / (SAMPLE_POINTS - 1);
  const axis = Array.from({ length: SAMPLE_POINTS }, (_v, i) => startTime + i * step);
  return {
    labels: axis.map(convertNumberToTimestamp),
    timestamps: axis,
    series: SAMPLE_LABELS.map((label, phase) => ({
      label,
      values: Array.from({ length: SAMPLE_POINTS }, (_v, i) => {
        const wave = Math.sin((i / SAMPLE_POINTS) * Math.PI * 3 + phase * 1.7) * 18 + 50 - phase * 12;
        return Math.round((wave + jitter(i, phase) * 12) * 10) / 10;
      }),
    })),
  };
}

/** Compact description of the window the preview ran over. */
export function previewRangeLabel(startTime: number, endTime: number): string {
  const minutes = Math.max(1, Math.round((endTime - startTime) / 60000));
  if (minutes < 90) return `Last ${minutes} min`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `Last ${hours} h`;
  return `Last ${Math.round(hours / 24)} d`;
}
