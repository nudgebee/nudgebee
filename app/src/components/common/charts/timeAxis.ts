/** timeAxis — how a time x-axis is labelled, the way Grafana and Datadog label one. */

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

const pad2 = (n: number): string => String(n).padStart(2, '0');

/** "12 Aug" */
const dayMonth = (d: Date): string => `${d.getDate()} ${MONTHS[d.getMonth()]}`;

/** "19:20", or "19:20:30" with seconds. */
const clock = (d: Date, seconds: boolean): string => `${pad2(d.getHours())}:${pad2(d.getMinutes())}${seconds ? `:${pad2(d.getSeconds())}` : ''}`;

/**
 * Formats one tick. `isFirstTick` is the axis' leading tick — the only one
 * guaranteed to survive autoSkip, so it is where a same-day range shows its date.
 */
export type AxisTickFormatter = (timestamp: number | null | undefined, isFirstTick: boolean) => string;

/** Smallest and largest finite entry, in one pass — the array can hold thousands of points. */
function bounds(timestamps: readonly (number | null | undefined)[]): { first: number; last: number } | null {
  let first = Infinity;
  let last = -Infinity;
  for (const t of timestamps) {
    if (typeof t !== 'number' || !Number.isFinite(t)) continue;
    if (t < first) first = t;
    if (t > last) last = t;
  }
  return first === Infinity ? null : { first, last };
}

/** Builds the tick formatter for a set of epoch-millisecond timestamps. */
export function axisTimeFormatter(timestamps: readonly (number | null | undefined)[]): AxisTickFormatter {
  const range = bounds(timestamps);
  const span = range ? range.last - range.first : 0;

  const format = (ts: number, isFirstTick: boolean): string => {
    const d = new Date(ts);
    if (span > 400 * DAY) return `${MONTHS[d.getMonth()]} ${d.getFullYear()}`;
    if (span > 30 * DAY) return dayMonth(d);
    // Past a day the date changes between ticks, so every tick has to carry it.
    if (span > DAY) return `${dayMonth(d)} ${clock(d, false)}`;
    const withSeconds = span <= 2 * MINUTE;
    return isFirstTick ? `${dayMonth(d)} ${clock(d, withSeconds)}` : clock(d, withSeconds);
  };

  return (timestamp, isFirstTick) => {
    if (typeof timestamp !== 'number' || !Number.isFinite(timestamp)) return '';
    return format(timestamp, isFirstTick);
  };
}

/**
 * The label shapes this repo puts on a time axis. A label has to match one of these exactly to be read
 * as an instant — the point is to recognise OUR formatters, not to guess at arbitrary text.
 */
/** `convertNumberToTimestamp` — "12-08-26 19:20:30", day first, two-digit year, hour unpadded. */
const DAY_FIRST = /^(\d{2})-(\d{2})-(\d{2}) (\d{1,2}):(\d{2}):(\d{2})$/;
/** ISO-ish, as the providers send it and `convertNumberToTimestampPromFormat` writes it. */
const ISO_LIKE = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2})?(\.\d+)?(Z|[+-]\d{2}:?\d{2}| ?UTC)?$/;
/** `Date#toLocaleString` — "8/12/2026, 5:14:43 PM". */
const LOCALE_STRING = /^\d{1,2}\/\d{1,2}\/\d{4},? \d{1,2}:\d{2}(:\d{2})?( ?[AaPp]\.?[Mm]\.?)?$/;

/** One label as an instant, or null when it is not one of the shapes above. */
function parseLabel(label: unknown): number | null {
  // Numbers are deliberately NOT read as epochs.
  if (typeof label !== 'string') return null;
  const text = label.trim();

  const dayFirst = DAY_FIRST.exec(text);
  if (dayFirst) {
    const [, day, month, year, hour, minute, second] = dayFirst;
    return new Date(2000 + Number(year), Number(month) - 1, Number(day), Number(hour), Number(minute), Number(second)).getTime();
  }

  if (ISO_LIKE.test(text)) {
    // A trailing "UTC" is the same instruction as "Z"; without either, a date-time is local — which is what
    // the formatters that emit this shape built it from.
    const parsed = Date.parse(text.replace(/ ?UTC$/, 'Z').replace(' ', 'T'));
    return Number.isFinite(parsed) ? parsed : null;
  }

  if (LOCALE_STRING.test(text)) {
    const parsed = Date.parse(text);
    return Number.isFinite(parsed) ? parsed : null;
  }

  return null;
}

/** Reads an axis of pre-formatted labels back into instants, or null when they are not a time axis. */
export function parseAxisTimestamps(labels: readonly unknown[] | undefined): number[] | null {
  if (!labels || labels.length < 2) return null;
  const out: number[] = [];
  let previous = -Infinity;
  for (const label of labels) {
    const parsed = parseLabel(label);
    if (parsed === null || parsed < previous) return null;
    previous = parsed;
    out.push(parsed);
  }
  return out;
}

/** The unabbreviated instant, for a tooltip: "12 Aug 2026, 19:20:30". */
export function fullTimeLabel(timestamp: number | null | undefined): string {
  if (typeof timestamp !== 'number' || !Number.isFinite(timestamp)) return '';
  const d = new Date(timestamp);
  return `${dayMonth(d)} ${d.getFullYear()}, ${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
}
