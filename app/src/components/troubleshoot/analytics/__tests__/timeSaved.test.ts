import { resolveTimeSaved } from '../timeSaved';

describe('resolveTimeSaved', () => {
  // The defect this tile shipped with: the backend rolled up agent time by
  // summing (updated_at - created_at) over an updated_at window, so a single
  // months-old conversation re-touched inside the window contributed thousands
  // of hours. The difference went negative and a clamp printed "0.0 hrs".
  it('reports nothing when measured agent time exceeds the manual baseline', () => {
    const result = resolveTimeSaved({
      completed_count: 1470,
      manual_baseline_minutes: 25,
      engineer_hourly_rate_usd: 30,
      total_agent_active_time_seconds: 4542 * 3600,
    });

    expect(result.savedHours).toBeLessThan(0);
    expect(result.measurable).toBe(false);
  });

  it('reports the difference when the numbers are sane', () => {
    const result = resolveTimeSaved({
      completed_count: 1447,
      manual_baseline_minutes: 25,
      engineer_hourly_rate_usd: 30,
      total_agent_active_time_seconds: 46.5 * 3600,
    });

    expect(result.savedHours).toBeCloseTo(556.4, 1);
    expect(result.savedCost).toBe(16693);
    expect(result.measurable).toBe(true);
  });

  // A missing baseline is server config that never arrived, not a measurement
  // of zero saving — the case that produced "0.0 hrs" before the clamp was
  // even reached.
  it('reports nothing when the server sends no baseline', () => {
    const result = resolveTimeSaved({
      completed_count: 186,
      manual_baseline_minutes: undefined,
      total_agent_active_time_seconds: 60000,
    });

    expect(result.measurable).toBe(false);
  });

  it('reports nothing when no investigation completed', () => {
    expect(resolveTimeSaved({ completed_count: 0, manual_baseline_minutes: 25 }).measurable).toBe(false);
    expect(resolveTimeSaved(null).measurable).toBe(false);
  });

  // Anything under 0.05 hrs renders as "0.0 hrs" at one decimal place, which is
  // the exact string this change exists to stop printing.
  it('reports nothing when the saving would round to zero', () => {
    const result = resolveTimeSaved({
      completed_count: 1,
      manual_baseline_minutes: 25,
      total_agent_active_time_seconds: 25 * 60 - 60,
    });

    expect(result.savedHours).toBeGreaterThan(0);
    expect(result.measurable).toBe(false);
  });

  // No rate configured means no dollar figure, not a $0 one.
  it('yields no cost when the server sends no hourly rate', () => {
    const result = resolveTimeSaved({
      completed_count: 100,
      manual_baseline_minutes: 25,
      total_agent_active_time_seconds: 3600,
    });

    expect(result.measurable).toBe(true);
    expect(result.savedCost).toBe(0);
  });
});
