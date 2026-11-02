/**
 * A panel's thresholds: Grafana's idea — steps, each a colour for a value past a
 * line — narrowed to what this renderer can evaluate honestly.
 *
 * Two deliberate narrowings, both so the feature has no config that does
 * nothing:
 *
 *  1. `stat` and `gauge` carry thresholds about the ONE number they render, so
 *     "the value crossed the line" has a single unambiguous answer. A
 *     `timeseries` panel carries them too, but judges every drawn LINE on its
 *     own and names the line that crossed — never the chart from one unnamed
 *     series, which would be picking a winner silently. A table has a value per
 *     cell and carries none.
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
import type { RawSeries, StatTotal } from './panelSeries';
import type { PanelSeries } from './usePanelData';

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

/** The shortest hold a step may ask for: two minutes, so any provider's minute-level data holds two samples. */
export const FOR_MIN_SECONDS = 120;
/** The longest: an hour, which is the range a dashboard opens on. */
export const FOR_MAX_SECONDS = 3600;

/** A step's hold in seconds, or undefined for a step that fires on the latest sample alone. */
export function forOf(step: PanelThresholdStep): number | undefined {
  return typeof step.for === 'number' ? step.for : undefined;
}

/** Whether a hold is one the renderer will honour: a whole number of minutes, two to sixty. */
export function isValidFor(seconds: number): boolean {
  return Number.isInteger(seconds) && seconds % 60 === 0 && seconds >= FOR_MIN_SECONDS && seconds <= FOR_MAX_SECONDS;
}

/** "5m", "1h", "90m" — how a hold is written on the badge and in the editor. */
export function holdLabel(seconds: number): string {
  return seconds % 3600 === 0 ? `${seconds / 3600}h` : `${Math.round(seconds / 60)}m`;
}

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

/** The panel types that show ONE number — and so add their accounts up, and judge thresholds against it. */
export function showsOneNumber(type: PanelType): boolean {
  return type === 'stat' || type === 'gauge';
}

/** Panel types that evaluate thresholds: the two that show one number, and the chart that judges each line. */
export function hasThresholds(type: PanelType): boolean {
  return showsOneNumber(type) || isLinePanel(type);
}

/**
 * Panel types whose thresholds are about LINES rather than the one shown number.
 * The two groups evaluate different things, so steps do not carry across them.
 */
export function isLinePanel(type: PanelType): boolean {
  return type === 'timeseries';
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
  if (step.for !== undefined && !isValidFor(step.for)) return false;
  return step.applies_to !== 'account' || (typeof step.account_id === 'string' && step.account_id !== '');
}

/**
 * The steps a panel actually renders from, ascending. An unusable one is dropped
 * rather than drawn in a fallback colour.
 */
export function panelThresholdsOf(panel: Panel): PanelThresholdStep[] {
  return (
    panelThresholdStepsOf(panel)
      .filter(isCompleteStep)
      // A stat or gauge judges one sample, so it cannot honour a hold. Firing
      // such a step at once would break what it says; it is left undrawn, and
      // the editor offers to remove the hold (see PanelEditorModal).
      .filter((step) => isLinePanel(panel.type) || step.for === undefined)
      .sort((a, b) => a.value - b.value)
  );
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
  return resolveCrossed(steps.filter((step) => crosses(step, value)));
}

/** The one step that decides, among steps the value has ALREADY been found to cross. See breachedStep. */
function resolveCrossed(crossed: PanelThresholdStep[]): PanelThresholdStep | undefined {
  let upper: PanelThresholdStep | undefined;
  let lower: PanelThresholdStep | undefined;
  for (const step of crossed) {
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
  if (!hasThresholds(panel.type) || isLinePanel(panel.type)) return undefined;
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
  /** On a chart, the line that crossed — its legend label. */
  line?: string;
  /** On a chart, the hold the line satisfied: epoch ms of its first and last sample. */
  from?: number;
  to?: number;
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
  if (!stat || !hasThresholds(panel.type) || isLinePanel(panel.type)) return NO_BREACHES;
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
          o.step.color === b.step.color &&
          forOf(o.step) === forOf(b.step)
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

/** A hold one line satisfied, as positions on the chart's axis — what the chart shades. */
export interface HoldSpan {
  /** Index into the drawn series. */
  series: number;
  step: PanelThresholdStep;
  /** First and last axis index of the run. */
  from: number;
  to: number;
}

/** Everything a chart's thresholds say about the lines it is drawing. */
export interface LineBreaches extends PanelBreaches {
  spans: HoldSpan[];
  /**
   * Steps whose hold the chart's sampling is too coarse to judge: fewer than two
   * samples would fall inside it. Reported so the panel can say "zoom in" rather
   * than stay silently plain.
   */
  tooCoarse: PanelThresholdStep[];
  /** Steps whose hold is longer than the time range, so no line could ever have held it. */
  tooLong: PanelThresholdStep[];
}

const NO_LINE_BREACHES: LineBreaches = { all: [], rows: new Map(), spans: [], tooCoarse: [], tooLong: [] };

/**
 * How far behind the range's end a line's last sample may be and still count as
 * current. Providers publish late — CloudWatch by minutes — and Prometheus keeps
 * a vanished series for five minutes itself, so this is the floor.
 */
const STALE_AFTER_MS = 5 * 60 * 1000;

/** The median gap between consecutive entries, in the entries' unit; 0 with fewer than two. */
function medianSpacing(points: number[]): number {
  if (points.length < 2) return 0;
  const gaps: number[] = [];
  for (let i = 1; i < points.length; i++) gaps.push(points[i] - points[i - 1]);
  gaps.sort((a, b) => a - b);
  return gaps[Math.floor(gaps.length / 2)];
}

/**
 * Evaluates a chart's steps against every line it draws.
 *
 * Each line is judged on the chart's own samples — the ones the viewer is
 * looking at — so the verdict is about the picture, and the hover says which
 * samples it rests on. The rules that keep that honest:
 *
 *  - A line counts only while it is CURRENT: its last sample within two of its
 *    own steps, and never less than five minutes, of the end of the range the
 *    viewer asked for. A pod that died forty minutes ago at 95% is history on
 *    the chart, not a breach — also when it is the only line on it.
 *  - A step with no hold fires on the line's latest sample, as a stat does.
 *  - A step with a hold fires when the line's latest samples crossed the line
 *    without a break for at least the hold: from the first sample of that run
 *    to the last. A line younger than the hold cannot have held it. A missing
 *    sample breaks the run only when the gap is longer than twice the line's
 *    own spacing — the shared axis writes `null` wherever a coarser line had no
 *    sample of its own, and that is not the line going quiet.
 *  - A hold shorter than two of the chart's steps cannot be judged and is
 *    reported in `tooCoarse`; one longer than the range, in `tooLong`. Neither
 *    is fired or silently ignored.
 *  - The consolidated total is drawn but never judged: it is a sum of whichever
 *    accounts reported at each instant, a sawtooth on a fleet that misses the
 *    odd scrape, not a measurement.
 *
 * `accounts` are the ones on screen, which is how an `account` step finds its
 * lines; `shown` and `every` both mean every line, since a chart has no single
 * shown number for `shown` to be about. `range` is the requested window in
 * epoch ms; without it the axis's own first and last instants stand in.
 */
export function lineBreaches(
  panel: Panel,
  series: PanelSeries[],
  timestamps: number[] | undefined,
  accounts: { value: string; label: string }[],
  range?: { start: number; end: number }
): LineBreaches {
  if (!isLinePanel(panel.type) || !timestamps || timestamps.length === 0) return NO_LINE_BREACHES;
  const steps = panelThresholdsOf(panel);
  if (steps.length === 0) return NO_LINE_BREACHES;

  const axisStep = medianSpacing(timestamps);
  const rangeStart = range?.start ?? timestamps[0];
  const rangeEnd = range?.end ?? timestamps[timestamps.length - 1];
  // The longest run any line could show: the range, less the step the first
  // sample may sit inside it.
  const longestRun = rangeEnd - rangeStart - axisStep;
  const idByLabel = new Map(accounts.map((a) => [a.label, a.value]));
  const all: Breach[] = [];
  const spans: HoldSpan[] = [];
  const tooCoarse = new Set<PanelThresholdStep>();
  const tooLong = new Set<PanelThresholdStep>();

  series.forEach((line, index) => {
    if (line.consolidated) return;
    const values = line.values;
    const has = (i: number) => values[i] !== null && values[i] !== undefined && Number.isFinite(values[i] as number);
    let lastIdx = -1;
    for (let i = values.length - 1; i >= 0; i--) {
      if (has(i)) {
        lastIdx = i;
        break;
      }
    }
    if (lastIdx < 0) return;

    const own: number[] = [];
    for (let i = 0; i < values.length; i++) if (has(i)) own.push(timestamps[i]);
    const ownStep = medianSpacing(own) || axisStep;
    // Not current: the line ended well before the range did.
    if (timestamps[lastIdx] < rangeEnd - Math.max(2 * Math.max(ownStep, axisStep), STALE_AFTER_MS)) return;

    const accountId = line.accountLabel ? idByLabel.get(line.accountLabel) : accounts.length === 1 ? accounts[0].value : undefined;
    const mine = steps.filter((step) => appliesToOf(step) !== 'account' || (accountId !== undefined && step.account_id === accountId));
    if (mine.length === 0) return;

    const tolerance = 2 * ownStep;
    const latest = values[lastIdx] as number;

    const crossed: { step: PanelThresholdStep; from: number }[] = [];
    for (const step of mine) {
      const hold = forOf(step);
      if (hold === undefined) {
        if (crosses(step, latest)) crossed.push({ step, from: lastIdx });
        continue;
      }
      const holdMs = hold * 1000;
      if (axisStep <= 0 || holdMs < 2 * axisStep) {
        tooCoarse.add(step);
        continue;
      }
      if (holdMs > longestRun) {
        tooLong.add(step);
        continue;
      }
      // The run of crossing samples the line ends on, back to the first sample
      // that did not cross or the first real gap.
      let from = -1;
      let previous = timestamps[lastIdx];
      for (let i = lastIdx; i >= 0; i--) {
        if (!has(i)) continue;
        if (previous - timestamps[i] > tolerance || !crosses(step, values[i] as number)) break;
        previous = timestamps[i];
        from = i;
      }
      if (from >= 0 && timestamps[lastIdx] - timestamps[from] >= holdMs) crossed.push({ step, from });
    }

    const decided = resolveCrossed(crossed.map((c) => c.step));
    if (!decided) return;
    const run = crossed.find((c) => c.step === decided)!;
    all.push({
      step: decided,
      account: null,
      value: latest,
      line: line.label,
      ...(forOf(decided) !== undefined ? { from: timestamps[run.from], to: timestamps[lastIdx] } : {}),
    });
    if (forOf(decided) !== undefined) spans.push({ series: index, step: decided, from: run.from, to: lastIdx });
  });

  const frame = all.reduce<Breach | undefined>((worst, b) => (!worst || severer(worst.step, b.step) !== worst.step ? b : worst), undefined);
  return {
    frame,
    all: frame ? [frame, ...all.filter((b) => b !== frame)] : all,
    rows: new Map(),
    spans,
    tooCoarse: [...tooCoarse],
    tooLong: [...tooLong],
  };
}

/** One line's finished breaches inside the range, at the most severe step it reached. */
export interface PastBreach {
  line: string;
  step: PanelThresholdStep;
  /** How many separate times the line crossed it. */
  count: number;
  /** The worst value across those times, in the step's direction. */
  worst: number;
  /** When the latest of them ended, epoch ms. */
  lastAt: number;
}

/**
 * The breaches a chart's range holds that are already over: what `lineBreaches`
 * rightly leaves out, because it answers "is this breached now?". They never
 * colour the frame — a panel that recovered an hour ago is not on fire.
 *
 *  - A breach is a run of crossing samples. A sample that does not cross ends
 *    it, and so does a gap longer than twice the line's spacing: a scrape outage
 *    says nothing either way, so it splits rather than joins.
 *  - A step with a hold counts a run only if it lasted the hold. A hold too
 *    short for the chart's step is skipped here; `lineBreaches` already says so.
 *  - A run still going at the end of a current line is today's breach, not
 *    history, and is the badge's to report. A line that ended early has no such
 *    run: a pod that died at 95% is exactly the history this is for.
 *  - The consolidated total is not judged, for the reason `lineBreaches` gives.
 *  - A line reports its most severe step only, or a red spike inside an amber
 *    one would count twice.
 *
 * What it can see is what the chart sampled: a spike between two samples leaves
 * no trace at a wide range.
 */
export function pastBreaches(
  panel: Panel,
  series: PanelSeries[],
  timestamps: number[] | undefined,
  accounts: { value: string; label: string }[],
  range?: { start: number; end: number }
): PastBreach[] {
  if (!isLinePanel(panel.type) || !timestamps || timestamps.length === 0) return [];
  const steps = panelThresholdsOf(panel);
  if (steps.length === 0) return [];

  const axisStep = medianSpacing(timestamps);
  const rangeEnd = range?.end ?? timestamps[timestamps.length - 1];
  const idByLabel = new Map(accounts.map((a) => [a.label, a.value]));
  const out: PastBreach[] = [];

  for (const line of series) {
    if (line.consolidated) continue;
    const samples: { at: number; value: number }[] = [];
    line.values.forEach((value, i) => {
      if (value !== null && value !== undefined && Number.isFinite(value)) samples.push({ at: timestamps[i], value });
    });
    if (samples.length === 0) continue;

    const accountId = line.accountLabel ? idByLabel.get(line.accountLabel) : accounts.length === 1 ? accounts[0].value : undefined;
    const ownStep = medianSpacing(samples.map((p) => p.at)) || axisStep;
    const tolerance = 2 * ownStep;
    const last = samples[samples.length - 1];
    const current = last.at >= rangeEnd - Math.max(2 * Math.max(ownStep, axisStep), STALE_AFTER_MS);

    let found: PastBreach | undefined;
    for (const step of steps) {
      if (appliesToOf(step) === 'account' && (accountId === undefined || step.account_id !== accountId)) continue;
      const hold = forOf(step);
      if (hold !== undefined && (axisStep <= 0 || hold * 1000 < 2 * axisStep)) continue;

      const worse = (a: number, b: number) => (isUpper(opOf(step)) ? Math.max(a, b) : Math.min(a, b));
      let count = 0;
      let worst = 0;
      let lastAt = 0;
      let run: { from: number; to: number; worst: number } | undefined;
      const close = (active: boolean) => {
        if (run && !active && (hold === undefined || run.to - run.from >= hold * 1000)) {
          worst = count === 0 ? run.worst : worse(worst, run.worst);
          lastAt = run.to;
          count += 1;
        }
        run = undefined;
      };
      for (const { at, value } of samples) {
        if (run && at - run.to > tolerance) close(false);
        if (crosses(step, value)) {
          run = run ? { ...run, to: at, worst: worse(run.worst, value) } : { from: at, to: at, worst: value };
        } else {
          close(false);
        }
      }
      close(current);

      if (count > 0 && (!found || severer(step, found.step) === step)) found = { line: line.label, step, count, worst, lastAt };
    }
    if (found) out.push(found);
  }
  return out.sort((a, b) => b.lastAt - a.lastAt);
}

/**
 * Which series a chart with thresholds must keep when the series cap trims it:
 * those whose latest value already crosses one of the steps that applies to
 * them. A "ready replicas < 1" step breaches on the quietest line, which a peak
 * ranking would drop first. Undefined when the panel has nothing to prefer.
 *
 * `accountOf` names a series' account id, so a step about one account does not
 * keep another account's lines.
 */
export function breachingLinePreference(
  panel: Panel,
  accountOf: (series: RawSeries) => string | undefined
): ((series: RawSeries) => boolean) | undefined {
  if (!isLinePanel(panel.type)) return undefined;
  const steps = panelThresholdsOf(panel);
  if (steps.length === 0) return undefined;
  return (series) => {
    const account = accountOf(series);
    const mine = steps.filter((step) => appliesToOf(step) !== 'account' || (account !== undefined && step.account_id === account));
    let last: number | undefined;
    for (let i = series.values.length - 1; i >= 0; i--) {
      const v = series.values[i];
      if (v !== null && v !== undefined) {
        last = v;
        break;
      }
    }
    return breachedStep(mine, last) !== undefined;
  };
}

/** What the series cap's preference depends on — a fresh one re-ranks lines already fetched. */
export function linePreferenceKey(panel: Panel): string {
  if (!isLinePanel(panel.type)) return '';
  return JSON.stringify(panelThresholdsOf(panel).map((s) => [s.value, s.op ?? '', s.applies_to ?? '', s.account_id ?? '']));
}
