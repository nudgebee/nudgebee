import { matchRange, RANGE_OPTIONS } from '../BriefingFilters';

const MINUTE = 60000;
const windowOf = (minutes: number) => ({ startMs: 0, endMs: minutes * MINUTE });

describe('matchRange', () => {
  it('recognises every preset it offers', () => {
    RANGE_OPTIONS.forEach((option) => {
      const { startMs, endMs } = windowOf(option.minutes);
      expect(matchRange(startMs, endMs)).toBe(option.value);
    });
  });

  it('tolerates the small drift between a click and the next render', () => {
    // The window is stamped from Date.now() at click time, so by the time it is
    // read back it is a few seconds short of the nominal duration.
    expect(matchRange(0, 24 * 60 * MINUTE - 3000)).toBe('24h');
    expect(matchRange(0, 30 * MINUTE - 1000)).toBe('30m');
  });

  it('claims no preset for a genuinely custom range', () => {
    // Set from the table's own date picker — mislabelling it as one of ours
    // would show an active preset the user never chose.
    expect(matchRange(0, 5 * 60 * MINUTE)).toBe('');
    expect(matchRange(0, 45 * 24 * 60 * MINUTE)).toBe('');
  });

  it('does not crash on a zero-length or inverted window', () => {
    expect(typeof matchRange(0, 0)).toBe('string');
    expect(typeof matchRange(100, 0)).toBe('string');
  });
});
