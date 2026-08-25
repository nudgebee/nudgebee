import { formatStat, seriesStats } from '../legendChips';

describe('seriesStats', () => {
  it('summarises a series', () => {
    expect(seriesStats([1, 5, 3, 2, 4])).toEqual({ min: 1, max: 5, p99: 5, avg: 3 });
  });

  it('treats a gap as absent, not as zero', () => {
    // A pod that reported 4 and 6 for two scrapes and nothing for the rest has a
    // min of 4 — counting the gaps would pin min at 0 and halve the average.
    expect(seriesStats([null, 4, undefined, 6, ''])).toEqual({ min: 4, max: 6, p99: 6, avg: 5 });
  });

  it('parses the string values the providers send', () => {
    expect(seriesStats(['2', '8'])?.max).toBe(8);
  });

  it('is null for a series with nothing in it', () => {
    expect(seriesStats([])).toBeNull();
    expect(seriesStats([null, '', undefined])).toBeNull();
    expect(seriesStats(undefined)).toBeNull();
  });
});

describe('formatStat', () => {
  it('keeps two decimals in the ordinary range', () => {
    expect(formatStat(3.14159)).toBe('3.14');
    expect(formatStat(999.5)).toBe('999.50');
  });

  it('suffixes large magnitudes instead of printing every digit', () => {
    expect(formatStat(1234)).toBe('1.23K');
    expect(formatStat(4_500_000)).toBe('4.50M');
    expect(formatStat(2_000_000_000)).toBe('2.00B');
  });

  it('keeps three significant figures below one, where two decimals would say 0.00', () => {
    expect(formatStat(0.00123)).toBe('0.00123');
    expect(formatStat(0.5)).toBe('0.5');
  });

  it('writes an exact zero as 0', () => {
    expect(formatStat(0)).toBe('0');
  });

  it('appends the panel unit', () => {
    expect(formatStat(12.5, 'cores')).toBe('12.50 cores');
  });

  it('is an em dash when there is no number', () => {
    expect(formatStat(undefined)).toBe('—');
    expect(formatStat(NaN)).toBe('—');
  });
});
