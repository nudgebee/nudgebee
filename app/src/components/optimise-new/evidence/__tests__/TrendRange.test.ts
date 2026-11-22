import { trendRangeFor } from '../TrendRange';

describe('trendRangeFor', () => {
  it('covers the whole window the recommendation was computed over', () => {
    expect(trendRangeFor(336)).toBe(14);
    expect(trendRangeFor(168)).toBe(7);
    expect(trendRangeFor(24)).toBe(7);
    expect(trendRangeFor(200)).toBe(14);
    expect(trendRangeFor(400)).toBe(30);
  });

  it('caps at the longest range on offer', () => {
    expect(trendRangeFor(24 * 90)).toBe(30);
  });

  it('keeps the week when no window was stamped', () => {
    expect(trendRangeFor(null)).toBe(7);
    expect(trendRangeFor(undefined)).toBe(7);
    expect(trendRangeFor(0)).toBe(7);
    expect(trendRangeFor(NaN)).toBe(7);
  });
});
