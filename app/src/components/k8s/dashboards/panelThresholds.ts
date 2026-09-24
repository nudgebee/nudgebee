/**
 * A panel's thresholds: Grafana's idea — a scale of steps, each a colour that
 * applies from its value upwards — narrowed to what this renderer can evaluate
 * honestly.
 *
 * Two deliberate narrowings, both so the feature has no config that does
 * nothing:
 *
 *  1. Only `stat` and `gauge` carry thresholds. Those two render ONE number, so
 *     "the value crossed the line" has a single unambiguous answer. A chart or a
 *     table has a value per series, per point, per cell — colouring the whole
 *     panel from one of them would be picking a winner silently.
 *  2. Steps are absolute, and there is no base step. A percentage step needs a
 *     min and a max, which an aggregated number has neither of; and the range
 *     below the lowest threshold is drawn exactly as an unconfigured panel is,
 *     so a colour for it would never appear.
 *
 * The number compared is the one the panel SHOWS — statTotal's total, which on a
 * multi-account panel is the sum of the accounts that answered. That is on
 * purpose: the threshold has to be about the figure on screen, or a viewer
 * cannot check it against the tint.
 */
import { ds } from '@utils/colors';
import type { Panel, PanelThresholdColor, PanelThresholdStep, PanelType } from '@api1/dashboards';

/** Every colour a step may name, in the order the editor offers them. */
export const THRESHOLD_COLORS: PanelThresholdColor[] = ['red', 'amber', 'green', 'blue'];

/** How one threshold colour is drawn. */
export interface ThresholdTone {
  /** The panel's border and its header's underline. */
  border: string;
  /** The header band behind the title. */
  tint: string;
  /** The matching `ds/Chip` tone for the badge that names the crossed step. */
  chip: 'critical' | 'warning' | 'success' | 'info';
}

const TONES: Record<PanelThresholdColor, ThresholdTone> = {
  red: { border: ds.red[400], tint: ds.red[100], chip: 'critical' },
  amber: { border: ds.amber[400], tint: ds.amber[100], chip: 'warning' },
  green: { border: ds.green[400], tint: ds.green[100], chip: 'success' },
  blue: { border: ds.blue[400], tint: ds.blue[100], chip: 'info' },
};

export function thresholdTone(color: PanelThresholdColor): ThresholdTone {
  return TONES[color] || TONES.red;
}

/** Panel types that show one number, and so have something to compare against. */
export function hasThresholds(type: PanelType): boolean {
  return type === 'stat' || type === 'gauge';
}

/**
 * Every step the EDITOR should show: the list as authored, minus whatever is not
 * even a step-shaped object.
 *
 * A dashboard arrives as raw JSON that nothing has validated — `nativeImport`
 * passes `options` through whole and the server keeps it as an opaque map — so a
 * `null` in the list is a real thing to expect, and reading `.value` off one
 * threw before this guard existed, taking the whole editor modal down with it.
 *
 * Kept deliberately looser than `panelThresholdsOf`: a step being typed is
 * momentarily incomplete, and the render-time filter would delete the row out
 * from under the author mid-keystroke.
 */
export function panelThresholdStepsOf(panel: Panel): PanelThresholdStep[] {
  const raw = panel.options?.thresholds;
  if (!Array.isArray(raw)) return [];
  return raw.filter((step): step is PanelThresholdStep => Boolean(step) && typeof step === 'object');
}

/** Whether a step can be drawn at all — what the editor holds Save for. */
export function isCompleteStep(step: PanelThresholdStep): boolean {
  return Number.isFinite(step.value) && THRESHOLD_COLORS.includes(step.color);
}

/**
 * The steps a panel actually renders from, ascending. An unusable one is dropped
 * rather than drawn in a fallback colour.
 */
export function panelThresholdsOf(panel: Panel): PanelThresholdStep[] {
  return panelThresholdStepsOf(panel)
    .filter(isCompleteStep)
    .sort((a, b) => a.value - b.value);
}

/**
 * The step a value lands in, or undefined when it is below every one of them —
 * which is the unconfigured look, not a colour.
 *
 * The HIGHEST step crossed wins, so a value over both 80 and 90 reads as the 90
 * step. `>=`, matching Grafana: a step is the value it starts at.
 */
export function breachedStep(steps: PanelThresholdStep[], value: number | null | undefined): PanelThresholdStep | undefined {
  if (value === null || value === undefined || !Number.isFinite(value)) return undefined;
  let matched: PanelThresholdStep | undefined;
  for (const step of steps) {
    if (value >= step.value) matched = step;
  }
  return matched;
}

/**
 * The step this panel's value has crossed, if any.
 *
 * The value is taken RAW — a gauge pins its dial at 100, but 150 is what the
 * panel measured, and a threshold at 120 is about the measurement rather than
 * about where the needle stopped.
 */
export function panelBreach(panel: Panel, value: number | null | undefined): PanelThresholdStep | undefined {
  if (!hasThresholds(panel.type)) return undefined;
  return breachedStep(panelThresholdsOf(panel), value);
}
