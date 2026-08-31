// The "Engineer time saved" tile's arithmetic, kept out of the component so it
// can be tested. It shipped inline and silently rendered "0.0 hrs" for weeks:
// the backend's agent-time total was inflated by stale rows, the difference
// went negative, and a Math.max(0, ...) turned that into a confident zero.
// Nothing about the tile said the inputs were wrong.

export interface EffortAggregates {
  completed_count?: unknown;
  manual_baseline_minutes?: unknown;
  engineer_hourly_rate_usd?: unknown;
  total_agent_active_time_seconds?: unknown;
}

export interface TimeSaved {
  completed: number;
  savedHours: number;
  savedCost: number;
  // False whenever the tile has nothing honest to print — no completed work, no
  // server-side baseline, or a difference that came out negative or too small
  // to survive one decimal place. Callers render an em dash on false rather
  // than a number.
  measurable: boolean;
}

const num = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);

// A saved figure below 0.05 hrs rounds to the same "0.0 hrs" this exists to
// stop showing, so it counts as nothing to report rather than as a small win.
const MIN_REPORTABLE_HOURS = 0.05;

export const resolveTimeSaved = (effort: EffortAggregates | null | undefined): TimeSaved => {
  const completed = num(effort?.completed_count);
  const baselineMinutes = num(effort?.manual_baseline_minutes);
  const rate = num(effort?.engineer_hourly_rate_usd);
  const agentHours = num(effort?.total_agent_active_time_seconds) / 3600;

  // Deliberately not clamped at zero. The agents finish a first pass in minutes
  // against a baseline of tens of minutes, so a negative difference never means
  // "no time was saved" — it means an input is wrong, and the tile should say
  // nothing rather than dress bad data up as a measurement.
  const savedHours = (completed * baselineMinutes) / 60 - agentHours;

  return {
    completed,
    savedHours,
    savedCost: Math.round(savedHours * rate),
    measurable: completed > 0 && baselineMinutes > 0 && savedHours >= MIN_REPORTABLE_HOURS,
  };
};
