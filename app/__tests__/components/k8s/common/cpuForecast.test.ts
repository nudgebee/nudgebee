import {
  forecastDailyLine,
  forecastStatus,
  forecastSummary,
  humanHours,
  isBreachedCpuSlo,
  isCpuSloName,
  parseForecast,
  quantity,
} from '@components/k8s/common/cpuForecast';

describe('cpuForecast', () => {
  const projected = {
    status: 'projected' as const,
    threshold_pct: 75,
    current_pct: 37.33,
    slope_pct_per_day: 25.61,
    eta_hours: 35.4,
    level: 'HIGH' as const,
    daily: [
      { date: '2026-10-18', value_pct: 21.1, per_pod_usage: 0.077 },
      { date: '2026-10-19', value_pct: 52.1 },
      { date: '2026-10-20', value_pct: 79.5, projected: true },
    ],
  };

  it('recognises the two CPU SLO names, any case', () => {
    expect(isCpuSloName('cpu_throttling')).toBe(true);
    expect(isCpuSloName('CPU_Saturation')).toBe(true);
    expect(isCpuSloName('latency')).toBe(false);
    expect(isCpuSloName(undefined)).toBe(false);
  });

  it('parses last_forecast from a JSON string or an object, and tolerates junk', () => {
    expect(parseForecast(JSON.stringify(projected))?.status).toBe('projected');
    expect(parseForecast(projected)?.eta_hours).toBe(35.4);
    expect(parseForecast('not json')).toBeNull();
    expect(parseForecast(null)).toBeNull();
  });

  it('words each status', () => {
    expect(forecastStatus(projected)).toEqual({ text: 'Breach projected', tone: 'critical' });
    expect(forecastStatus({ ...projected, level: 'MEDIUM' }).tone).toBe('warning');
    expect(forecastStatus({ status: 'breached' }).text).toBe('Breached');
    expect(forecastStatus({ status: 'not_projected' })).toEqual({ text: 'OK', tone: 'success' });
    expect(forecastStatus({ status: 'insufficient_history' }).text).toBe('Not enough data');
    expect(forecastStatus(null).text).toBe('Not evaluated yet');
  });

  it('summarises a projection with ETA, current value and trend', () => {
    expect(forecastSummary(projected)).toBe('Reaches 75% in ~35h (now 37.3%, rising 25.6 pp/day)');
    expect(forecastSummary({ status: 'breached', threshold_pct: 75, current_pct: 81.7, slope_pct_per_day: -2 })).toBe(
      'Above 75%: now 81.7%, falling 2 pp/day'
    );
    expect(forecastSummary({ status: 'insufficient_history', reason: 'fewer than 24 hours of data' })).toBe('fewer than 24 hours of data');
    expect(forecastSummary(null)).toBe('Evaluated hourly once saved');
  });

  it('lists the climb day by day, marking projected days', () => {
    expect(forecastDailyLine(projected)).toBe('10-18: 21.1%, 77m · 10-19: 52.1% · 10-20: 79.5% (projected)');
  });

  it('shows hours under two days, days after', () => {
    expect(humanHours(0.2)).toBe('1h');
    expect(humanHours(47)).toBe('47h');
    expect(humanHours(60)).toBe('2.5 days');
  });

  it('writes cores as millicores', () => {
    expect(quantity(0.35, 'cores')).toBe('350m');
    expect(quantity(0.35)).toBe('350m');
  });

  it('counts a CPU SLO as firing from its latest forecast, since it writes no SLO reports', () => {
    expect(isBreachedCpuSlo({ name: 'cpu_throttling', last_forecast: JSON.stringify({ status: 'breached' }) })).toBe(true);
    expect(isBreachedCpuSlo({ name: 'cpu_saturation', last_forecast: { status: 'projected' } })).toBe(false);
    expect(isBreachedCpuSlo({ name: 'latency', last_forecast: { status: 'breached' } })).toBe(false);
    expect(isBreachedCpuSlo({ name: 'cpu_throttling' })).toBe(false);
  });
});
