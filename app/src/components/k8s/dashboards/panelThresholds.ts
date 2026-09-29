/**
 * A panel's thresholds: Grafana's idea — steps, each a colour for a value past a
 * line — narrowed to what this renderer can evaluate honestly.
 *
 * Two deliberate narrowings, both so the feature has no config that does
 * nothing:
 *
 *  1. Only `stat` and `gauge` carry thresholds. Those two render ONE number, so
 *     "the value crossed the line" has a single unambiguous answer. A chart or a
 *     table has a value per series, per point, per cell — colouring the whole
 *     panel from one of them would be picking a winner silently.
 *  2. Steps are absolute, and there is no base step. A percentage step needs a
 *     min and a max, which an aggregated number has neither of; and a value no
 *     step matches is drawn exactly as an unconfigured panel is, so a colour for
 *     it would never appear.
 *
 * A step is about one of three numbers (`applies_to`): the one the panel SHOWS —
 * statTotal's total, or one account's figure when the view is narrowed to it —
 * which is what every step meant before the field existed; each account's own
 * figure; or one named account's. The tint stays checkable against the screen
 * either way: the shown number is on the card, and an account's figure is in the
 * breakdown under it, with the badge naming the account that crossed.
 */
import { ds } from '@utils/colors';
import type { Panel, PanelThresholdAppliesTo, PanelThresholdColor, PanelThresholdOp, PanelThresholdStep, PanelType } from '@api1/dashboards';
import type { StatTotal } from './panelSeries';

/**
 * Every colour a step may name, in the order the editor offers them — which is
 * also their severity. When two crossed steps disagree and neither is further
 * past its line than the other (one is an upper bound, one a lower; or they are
 * on different accounts), the one earlier in this list decides the frame.
 */
export const THRESHOLD_COLORS: PanelThresholdColor[] = ['red', 'amber', 'green', 'blue'];

/** Every comparison a step may make, in the order the editor offers them. */
export const THRESHOLD_OPS: PanelThresholdOp[] = ['gte', 'gt', 'lte', 'lt'];

/** How a comparison is written on the badge and in the editor. */
export const OP_SYMBOL: Record<PanelThresholdOp, string> = { gte: '≥', gt: '>', lte: '≤', lt: '<' };

/** How a comparison reads in a sentence: "is {phrase} the 80 threshold". */
export const OP_PHRASE: Record<PanelThresholdOp, string> = { gte: 'at or above', gt: 'above', lte: 'at or below', lt: 'below' };

const APPLIES_TO: PanelThresholdAppliesTo[] = ['shown', 'every', 'account'];

/** A step's comparison. Absent is `gte`: every step written before the field was one. */
export function opOf(step: PanelThresholdStep): PanelThresholdOp {
  return step.op ?? 'gte';
}

/** A step's subject. Absent is `shown`: every step written before the field was about the shown number. */
export function appliesToOf(step: PanelThresholdStep): PanelThresholdAppliesTo {
  return step.applies_to ?? 'shown';
}

/** An upper bound — the value is in trouble going up. */
const isUpper = (op: PanelThresholdOp) => op === 'gte' || op === 'gt';

/** How one threshold colour is drawn. */
export interface ThresholdTone {
  /** The panel's border and its header's underline. */
  border: string;
  /** The header band behind the title. */
  tint: string;
  /** The matching `ds/Chip` tone for the badge that names the crossed step. */
  chip: 'critical' | 'warning' | 'success' | 'info';
  /** A breakdown row's figure, on the tint — dark enough to read as text. */
  text: string;
}

const TONES: Record<PanelThresholdColor, ThresholdTone> = {
  red: { border: ds.red[400], tint: ds.red[100], chip: 'critical', text: ds.red[700] },
  amber: { border: ds.amber[400], tint: ds.amber[100], chip: 'warning', text: ds.amber[700] },
  green: { border: ds.green[400], tint: ds.green[100], chip: 'success', text: ds.green[700] },
  blue: { border: ds.blue[400], tint: ds.blue[100], chip: 'info', text: ds.blue[700] },
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

/**
 * Whether a step can be drawn at all — what the editor holds Save for.
 *
 * An unknown comparison or subject is incomplete rather than read as the
 * default: an imported `op: 'between'` read as `gte` would colour the panel for
 * a reason nobody wrote.
 */
export function isCompleteStep(step: PanelThresholdStep): boolean {
  if (!Number.isFinite(step.value) || !THRESHOLD_COLORS.includes(step.color)) return false;
  if (step.op !== undefined && !THRESHOLD_OPS.includes(step.op)) return false;
  if (step.applies_to !== undefined && !APPLIES_TO.includes(step.applies_to)) return false;
  return step.applies_to !== 'account' || (typeof step.account_id === 'string' && step.account_id !== '');
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

/** Whether `value` is past `step`'s line. */
function crosses(step: PanelThresholdStep, value: number): boolean {
  switch (opOf(step)) {
    case 'gt':
      return value > step.value;
    case 'lt':
      return value < step.value;
    case 'lte':
      return value <= step.value;
    default:
      return value >= step.value;
  }
}

/** The more severe of two steps, per THRESHOLD_COLORS; `a` on a tie. */
function severer(a: PanelThresholdStep, b: PanelThresholdStep): PanelThresholdStep {
  return THRESHOLD_COLORS.indexOf(b.color) < THRESHOLD_COLORS.indexOf(a.color) ? b : a;
}

/**
 * The step a value lands in, or undefined when it crosses none of them — which
 * is the unconfigured look, not a colour.
 *
 * Among upper bounds the HIGHEST crossed wins, so a value over both 80 and 90
 * reads as the 90 step; among lower bounds, the LOWEST — under both 10 and 5
 * reads as the 5 step. A value past an upper AND a lower bound at once (≥ 80
 * and ≤ 100 both hold for 90) takes the more severe colour. `>=` is the default,
 * matching Grafana: a step is the value it starts at.
 */
export function breachedStep(steps: PanelThresholdStep[], value: number | null | undefined): PanelThresholdStep | undefined {
  if (value === null || value === undefined || !Number.isFinite(value)) return undefined;
  let upper: PanelThresholdStep | undefined;
  let lower: PanelThresholdStep | undefined;
  for (const step of steps) {
    if (!crosses(step, value)) continue;
    if (isUpper(opOf(step))) {
      if (!upper || step.value >= upper.value) upper = step;
    } else if (!lower || step.value <= lower.value) {
      lower = step;
    }
  }
  if (upper && lower) return severer(upper, lower);
  return upper || lower;
}

/**
 * The step this panel's shown number has crossed, if any — `shown` steps only.
 *
 * The value is taken RAW — a gauge pins its dial at 100, but 150 is what the
 * panel measured, and a threshold at 120 is about the measurement rather than
 * about where the needle stopped.
 */
export function panelBreach(panel: Panel, value: number | null | undefined): PanelThresholdStep | undefined {
  if (!hasThresholds(panel.type)) return undefined;
  return breachedStep(
    panelThresholdsOf(panel).filter((step) => appliesToOf(step) === 'shown'),
    value
  );
}

/** One crossed step, and whose number crossed it. */
export interface Breach {
  step: PanelThresholdStep;
  /** The account whose figure crossed; `null` for the panel's shown number. */
  account: string | null;
  value: number;
}

/** Everything a panel's thresholds say about what it is showing. */
export interface PanelBreaches {
  /** The breach the frame is drawn for: the most severe, the shown number first on a tie. */
  frame?: Breach;
  /** Every breach, the frame's first. */
  all: Breach[];
  /** The step each breakdown row crossed, keyed by the row's account. */
  rows: Map<string, PanelThresholdStep>;
}

const NO_BREACHES: PanelBreaches = { all: [], rows: new Map() };

/**
 * Evaluates every step against the number it is about.
 *
 * `accounts` are the ones the panel is showing, which is how a step naming an
 * account id finds that account's row — and why a step naming an account the
 * panel no longer covers (deleted, filtered out, imported from another tenant)
 * never fires rather than firing on the wrong row.
 *
 * On a single-account panel there is one number, so every step is about it and
 * no breach is attributed to an account: naming the only account on the card
 * says nothing the card does not.
 */
export function panelBreaches(panel: Panel, stat: StatTotal | null | undefined, accounts: { value: string; label: string }[]): PanelBreaches {
  if (!stat || !hasThresholds(panel.type)) return NO_BREACHES;
  const steps = panelThresholdsOf(panel);
  if (steps.length === 0) return NO_BREACHES;

  const all: Breach[] = [];
  const shown = panelBreach(panel, stat.total);
  if (shown && stat.total !== undefined) all.push({ step: shown, account: null, value: stat.total });

  const single = stat.rows.length <= 1;
  const idByLabel = new Map(accounts.map((a) => [a.label, a.value]));
  const rows = new Map<string, PanelThresholdStep>();
  for (const row of stat.rows) {
    if (row.value === undefined) continue;
    // A single-account panel's one row carries no account name.
    const id = row.account ? idByLabel.get(row.account) : accounts.length === 1 ? accounts[0].value : undefined;
    // Only accounts on screen. A refetch keeps the previous answer until the new
    // one lands, so right after the view is narrowed the rows can still hold an
    // account the viewer just filtered out — and it must not tint the panel.
    if (id === undefined) continue;
    const mine = steps.filter((step) => {
      const subject = appliesToOf(step);
      return subject === 'every' || (subject === 'account' && step.account_id === id);
    });
    const step = breachedStep(mine, row.value);
    if (!step) continue;
    rows.set(row.account, step);
    all.push({ step, account: single ? null : row.account, value: row.value });
  }

  // On a single-account panel a `shown` step and an `every` step are about the
  // same number, so two that draw the same line are one breach, not two.
  const distinct = all.filter(
    (b, i) =>
      all.findIndex(
        (o) =>
          o.account === b.account &&
          o.value === b.value &&
          opOf(o.step) === opOf(b.step) &&
          o.step.value === b.step.value &&
          o.step.color === b.step.color
      ) === i
  );
  const frame = distinct.reduce<Breach | undefined>((worst, b) => (!worst || severer(worst.step, b.step) !== worst.step ? b : worst), undefined);
  return { frame, all: frame ? [frame, ...distinct.filter((b) => b !== frame)] : distinct, rows };
}

/** Two of a panel's steps that apply to the same value, and which one wins there. */
export interface ThresholdOverlap {
  /** Positions in the list as authored — the editor's rows. */
  steps: [number, number];
  message: string;
}

const colorName = (color: PanelThresholdColor) => `${color[0].toUpperCase()}${color.slice(1)}`;

/**
 * The overlaps an author would not guess the outcome of — found so the editor
 * can say what happens, never so it can refuse the steps.
 *
 * Reported: an upper and a lower bound that meet (the more severe colour wins
 * between them); two steps starting at the same line (the later one wins); and
 * an "every account" step outranking an account's own step on that account.
 * Not reported: a plain scale — ≥ 80 amber, ≥ 90 red on the same number — which
 * is what thresholds are for, and would make the hint noise on every panel.
 * Steps of one colour never conflict, and an incomplete step is not a step yet.
 *
 * `accounts` are the ones the panel covers. With one or none, every subject is
 * the same number.
 */
export function thresholdOverlaps(steps: PanelThresholdStep[], accounts: { value: string; label: string }[]): ThresholdOverlap[] {
  const labelOf = new Map(accounts.map((a) => [a.value, a.label]));
  const describe = (step: PanelThresholdStep) => {
    const subject = appliesToOf(step);
    const who = subject === 'every' ? 'Every account' : subject === 'account' ? labelOf.get(step.account_id || '') || 'an account' : 'Shown number';
    return `${who} ${OP_SYMBOL[opOf(step)]} ${step.value} · ${colorName(step.color)}`;
  };

  /**
   * Whether two steps can ever be about the same number, and the account to
   * name when that is one account's figure. `undefined` means never.
   */
  const shared = (a: PanelThresholdStep, b: PanelThresholdStep): { account?: string; general?: PanelThresholdStep } | undefined => {
    // A step naming an account the panel does not cover never fires, so it overlaps nothing.
    const dangling = (s: PanelThresholdStep) => appliesToOf(s) === 'account' && !labelOf.has(s.account_id || '');
    if (dangling(a) || dangling(b)) return undefined;
    if (accounts.length <= 1) return {};
    const sa = appliesToOf(a);
    const sb = appliesToOf(b);
    if (sa === 'shown' || sb === 'shown') return sa === sb ? {} : undefined;
    if (sa === 'account' && sb === 'account') return a.account_id === b.account_id ? { account: labelOf.get(a.account_id || '') } : undefined;
    if (sa === 'every' && sb === 'every') return {};
    // Every account against one account's own step: they meet on that account.
    const own = sa === 'account' ? a : b;
    const label = labelOf.get(own.account_id || '');
    // A step naming an account the panel no longer covers never fires.
    return label ? { account: label, general: sa === 'every' ? a : b } : undefined;
  };

  const found: ThresholdOverlap[] = [];
  steps.forEach((a, i) => {
    if (!isCompleteStep(a)) return;
    steps.forEach((b, j) => {
      if (j <= i || !isCompleteStep(b) || a.color === b.color) return;
      const subject = shared(a, b);
      if (!subject) return;
      const prefix = subject.account ? `For ${subject.account}, steps` : 'Steps';
      const pair: [number, number] = [i, j];
      const upA = isUpper(opOf(a));

      if (upA !== isUpper(opOf(b))) {
        const [up, low] = upA ? [a, b] : [b, a];
        const inclusive = opOf(up) === 'gte' && opOf(low) === 'lte';
        if (up.value > low.value || (up.value === low.value && !inclusive)) return;
        const where = up.value === low.value ? `at ${up.value}` : `from ${up.value} to ${low.value}`;
        found.push({
          steps: pair,
          message: `${prefix} ${i + 1} and ${j + 1} both apply ${where} — ${colorName(severer(a, b).color)} wins there, as the more severe colour.`,
        });
        return;
      }

      // Same direction. Past both lines the further one wins; on the same line, the later.
      const winner = a.value === b.value ? b : upA === a.value > b.value ? a : b;
      const w = winner === a ? i : j;
      const edge = upA ? `from ${winner.value} up` : `from ${winner.value} down`;
      const why = a.value === b.value ? 'the later one' : upA ? 'the higher line' : 'the lower line';
      if (a.value === b.value && !subject.general) {
        found.push({
          steps: pair,
          message: `${prefix} ${i + 1} and ${j + 1} both start at ${a.value} — step ${w + 1} (${describe(winner)}) wins, as ${why}.`,
        });
      } else if (subject.general && winner === subject.general) {
        found.push({
          steps: pair,
          message: `${prefix} ${i + 1} and ${j + 1} both apply ${edge} — step ${w + 1} (${describe(winner)}) wins, as ${why}.`,
        });
      }
    });
  });
  return found;
}
