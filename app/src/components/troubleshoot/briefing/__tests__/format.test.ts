import { formatDuration, formatPercent, formatShare, formatWindowLength } from '../format';

describe('share and percent', () => {
  it('shows one decimal only when it carries information', () => {
    expect(formatShare(558, 795)).toBe('70.2%');
    expect(formatShare(795, 795)).toBe('100%');
    expect(formatShare(0, 795)).toBe('0%');
  });

  it('never divides by zero', () => {
    expect(formatShare(5, 0)).toBe('0%');
    expect(formatPercent(5, 0)).toBe('0%');
  });

  it('rounds percent where a decimal would be false precision', () => {
    expect(formatPercent(237, 251)).toBe('94%');
  });
});

describe('durations', () => {
  it('formats hours and minutes', () => {
    expect(formatDuration((15 * 60 + 58) * 60000)).toBe('15h 58m');
    expect(formatDuration(12 * 60000)).toBe('12m');
    expect(formatDuration(0)).toBe('0m');
    expect(formatDuration(-5)).toBe('0m');
  });

  it('drops minutes once days are in play', () => {
    expect(formatDuration((3 * 24 * 60 + 4 * 60 + 30) * 60000)).toBe('3d 4h');
    expect(formatDuration(2 * 24 * 60 * 60000)).toBe('2d');
  });
});

describe('formatWindowLength', () => {
  const hours = (n: number) => n * 3600000;

  it('labels the window the way the header states it', () => {
    expect(formatWindowLength(0, hours(24))).toBe('24h');
    expect(formatWindowLength(0, hours(24 * 5))).toBe('5d');
    expect(formatWindowLength(0, 90 * 60000)).toBe('2h');
    expect(formatWindowLength(0, 30 * 60000)).toBe('30m');
  });
});
