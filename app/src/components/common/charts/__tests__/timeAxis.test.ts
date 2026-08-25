import { axisTimeFormatter, fullTimeLabel, parseAxisTimestamps } from '../timeAxis';
import { convertNumberToTimestamp, convertNumberToTimestampPromFormat } from '@utils/common';

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** A local-time instant, so the assertions don't move with the runner's zone. */
const at = (y: number, m: number, d: number, hh = 0, mm = 0, ss = 0) => new Date(y, m - 1, d, hh, mm, ss).getTime();

/** `count` points, `step` apart, starting at `start`. */
const axis = (start: number, step: number, count: number) => Array.from({ length: count }, (_v, i) => start + i * step);

describe('axisTimeFormatter', () => {
  it('shows clock time inside a day, with the date carried on the first tick only', () => {
    const points = axis(at(2026, 8, 12, 17, 0), 5 * MINUTE, 12);
    const format = axisTimeFormatter(points);

    expect(format(points[0], true)).toBe('12 Aug 17:00');
    expect(format(points[4], false)).toBe('17:20');
    expect(format(points[11], false)).toBe('17:55');
  });

  it('adds seconds only when the window is tight enough for them to mean something', () => {
    const tight = axis(at(2026, 8, 12, 17, 0, 0), 5 * 1000, 12);
    expect(axisTimeFormatter(tight)(tight[2], false)).toBe('17:00:10');

    const wide = axis(at(2026, 8, 12, 17, 0, 30), 5 * MINUTE, 12);
    expect(axisTimeFormatter(wide)(wide[2], false)).toBe('17:10');
  });

  it('dates every tick once the range runs past a day', () => {
    const points = axis(at(2026, 8, 12, 9, 0), 6 * HOUR, 12);
    const format = axisTimeFormatter(points);

    expect(format(points[0], true)).toBe('12 Aug 09:00');
    expect(format(points[5], false)).toBe('13 Aug 15:00');
  });

  it('drops the time on a multi-week range and the day on a multi-year one', () => {
    const weeks = axis(at(2026, 1, 1), 5 * DAY, 20);
    expect(axisTimeFormatter(weeks)(weeks[6], false)).toBe('31 Jan');

    const years = axis(at(2024, 1, 1), 90 * DAY, 12);
    expect(axisTimeFormatter(years)(years[4], false)).toBe('Dec 2024');
  });

  it('returns empty for a padded or missing point rather than an Invalid Date', () => {
    const points = axis(at(2026, 8, 12, 17, 0), 5 * MINUTE, 6);
    const format = axisTimeFormatter([null, ...points, null]);

    expect(format(null, true)).toBe('');
    expect(format(undefined, false)).toBe('');
    expect(format(NaN, false)).toBe('');
  });

  it('reads the span off the whole axis, so one point is not treated as a year', () => {
    const single = [at(2026, 8, 12, 17, 5, 30)];
    expect(axisTimeFormatter(single)(single[0], true)).toBe('12 Aug 17:05:30');
  });
});

describe('parseAxisTimestamps', () => {
  it('reads back the axis every chart in the app formats with convertNumberToTimestamp', () => {
    // The round trip is the whole point: this is what lets a caller that only
    // ever passed formatted strings get a real time axis.
    const instants = axis(at(2026, 8, 12, 17, 0), 5 * MINUTE, 4);
    expect(parseAxisTimestamps(instants.map(convertNumberToTimestamp))).toEqual(instants);
  });

  it('reads the Prometheus UTC form as UTC, not as local time', () => {
    const instants = axis(Date.UTC(2026, 7, 12, 17, 0), 5 * MINUTE, 4);
    expect(parseAxisTimestamps(instants.map(convertNumberToTimestampPromFormat))).toEqual(instants);
  });

  it('reads plain ISO labels, with or without seconds', () => {
    expect(parseAxisTimestamps(['2026-08-12T17:00:00', '2026-08-12T17:05:00'])).toEqual([at(2026, 8, 12, 17, 0), at(2026, 8, 12, 17, 5)]);
    expect(parseAxisTimestamps(['2026-08-12 17:00', '2026-08-12 17:05'])).toEqual([at(2026, 8, 12, 17, 0), at(2026, 8, 12, 17, 5)]);
  });

  it('leaves a categorical axis alone', () => {
    expect(parseAxisTimestamps(['Jan', 'Feb', 'Mar'])).toBeNull();
    expect(parseAxisTimestamps(['prod', 'staging'])).toBeNull();
    // "Minutes since pod creation" — numbers are never read as epochs, or this
    // axis would be relabelled to 1970.
    expect(parseAxisTimestamps([0, 1, 2, 3])).toBeNull();
    expect(parseAxisTimestamps(['0', '1', '2'])).toBeNull();
  });

  it('refuses a partly-parseable or unsorted axis rather than guessing at it', () => {
    expect(parseAxisTimestamps(['2026-08-12T17:00:00', 'n/a', '2026-08-12T17:10:00'])).toBeNull();
    // Backwards means it is not a timeline — some category that happens to be dated.
    expect(parseAxisTimestamps(['2026-08-12T17:10:00', '2026-08-12T17:00:00'])).toBeNull();
  });

  it('needs at least two points to call something an axis', () => {
    expect(parseAxisTimestamps(['2026-08-12T17:00:00'])).toBeNull();
    expect(parseAxisTimestamps([])).toBeNull();
    expect(parseAxisTimestamps(undefined)).toBeNull();
  });
});

describe('fullTimeLabel', () => {
  it('spells the month out, so the day/month order cannot be read backwards', () => {
    expect(fullTimeLabel(at(2026, 8, 12, 19, 20, 30))).toBe('12 Aug 2026, 19:20:30');
  });

  it('is empty for a missing instant', () => {
    expect(fullTimeLabel(undefined)).toBe('');
    expect(fullTimeLabel(null)).toBe('');
  });
});
