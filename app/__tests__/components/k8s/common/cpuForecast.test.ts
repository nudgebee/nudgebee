import {
  forecastDailyLine,
  forecastStatus,
  forecastSummary,
  humanHours,
  isBreachedForecastSlo,
  isForecastSloName,
  isContainerScopedSloName,
  defaultMemoryContainer,
  memoryLimitedContainers,
  FORECAST_EVENT_KEYS,
  isStaleForecast,
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

  it('recognises the forecast SLO names, any case', () => {
    expect(isForecastSloName('cpu_throttling')).toBe(true);
    expect(isForecastSloName('CPU_Saturation')).toBe(true);
    expect(isForecastSloName('memory_saturation')).toBe(true);
    expect(isForecastSloName('latency')).toBe(false);
    expect(isForecastSloName(undefined)).toBe(false);
    expect(isContainerScopedSloName('memory_saturation')).toBe(true);
    expect(isContainerScopedSloName('cpu_throttling')).toBe(false);
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

  it('counts a forecast SLO as firing from its latest forecast, since it writes no SLO reports', () => {
    expect(isBreachedForecastSlo({ name: 'cpu_throttling', last_forecast: JSON.stringify({ status: 'breached' }) })).toBe(true);
    expect(isBreachedForecastSlo({ name: 'memory_saturation', last_forecast: { status: 'breached' } })).toBe(true);
    expect(isBreachedForecastSlo({ name: 'cpu_saturation', last_forecast: { status: 'projected' } })).toBe(false);
    expect(isBreachedForecastSlo({ name: 'latency', last_forecast: { status: 'breached' } })).toBe(false);
    expect(isBreachedForecastSlo({ name: 'cpu_throttling' })).toBe(false);
  });

  it('writes memory in MiB', () => {
    expect(quantity(704, 'MiB')).toBe('704 MiB');
  });

  it('lists the containers whose spec sets a memory limit, from the workload meta', () => {
    const meta = {
      config: {
        containers: [
          { name: 'api', resources: { limits: { cpu: '500m', memory: '512Mi' } } },
          { name: 'istio-proxy', resources: { limits: { memory: '128Mi' } } },
          { name: 'init-ish', resources: { limits: { cpu: '100m' } } },
        ],
      },
    };
    expect(memoryLimitedContainers(meta)).toEqual(['api', 'istio-proxy']);
    expect(memoryLimitedContainers(JSON.stringify(meta))).toEqual(['api', 'istio-proxy']);
    expect(memoryLimitedContainers('not json')).toEqual([]);
    expect(memoryLimitedContainers(null)).toEqual([]);
  });

  it('defaults the memory SLO to the only container, or the first that is not a known sidecar', () => {
    expect(defaultMemoryContainer([])).toBe('');
    expect(defaultMemoryContainer(['worker'])).toBe('worker');
    expect(defaultMemoryContainer(['istio-proxy', 'api'])).toBe('api');
    expect(defaultMemoryContainer(['istio-proxy', 'envoy'])).toBe('istio-proxy');
  });

  it('lists every forecast alert key, CPU and memory', () => {
    expect(FORECAST_EVENT_KEYS).toEqual(
      expect.arrayContaining(['CPUThrottlingPredicted', 'MemorySaturationPredicted', 'MemorySaturationSLOBreached'])
    );
    expect(FORECAST_EVENT_KEYS).toHaveLength(6);
  });

  describe('a stored forecast the hourly run has not refreshed', () => {
    const now = Date.parse('2026-10-22T08:50:00Z');
    const at = (minutesAgo: number) => new Date(now - minutesAgo * 60_000).toISOString();

    it('shows as out of date once an hourly run was missed, not as the old status', () => {
      const stale = { ...projected, evaluated_at: at(125) };
      expect(isStaleForecast(stale, now)).toBe(true);
      expect(forecastStatus(stale, now)).toEqual({ text: 'Out of date', tone: 'neutral' });
      expect(forecastSummary(stale, now)).toBe('Last evaluated 2h ago');
      expect(forecastSummary({ status: 'not_projected', evaluated_at: at(3 * 24 * 60) }, now)).toBe('Last evaluated 3 days ago');
    });

    it('keeps the status of a forecast from the latest run, even a late one', () => {
      const fresh = { ...projected, evaluated_at: at(110) };
      expect(isStaleForecast(fresh, now)).toBe(false);
      expect(forecastStatus(fresh, now).text).toBe('Breach projected');
      expect(forecastSummary(fresh, now)).toBe('Reaches 75% in ~35h (now 37.3%, rising 25.6 pp/day)');
    });

    it('treats a forecast without a timestamp as current', () => {
      expect(isStaleForecast(projected, now)).toBe(false);
      expect(isStaleForecast(null, now)).toBe(false);
    });

    it('never counts an out-of-date breach as firing', () => {
      const breached = (evaluated_at: string) => ({ name: 'cpu_throttling', last_forecast: { status: 'breached', evaluated_at } });
      expect(isBreachedForecastSlo(breached(at(125)), now)).toBe(false);
      expect(isBreachedForecastSlo(breached(at(10)), now)).toBe(true);
    });
  });
});
