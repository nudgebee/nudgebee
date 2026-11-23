import { useState } from 'react';
import { Box } from '@mui/material';
import { Toggle } from '@ui/Toggle';

/**
 * The time range a right-sizing recommendation's usage charts cover. The
 * recommender decides on a longer lookback than a week by default, so a fixed
 * seven-day chart hides the very spikes the verdict was weighed against.
 */
export const TREND_RANGE_DAYS = [7, 14, 30] as const;
export type TrendRangeDays = (typeof TREND_RANGE_DAYS)[number];

const DEFAULT_RANGE: TrendRangeDays = 7;

/**
 * The shortest range that covers the window the recommendation was computed
 * over, so the default chart shows everything the verdict saw. Without a
 * stamped window the chart keeps the week it always drew.
 */
export function trendRangeFor(windowHours: number | null | undefined): TrendRangeDays {
  if (typeof windowHours !== 'number' || !(windowHours > 0)) return DEFAULT_RANGE;
  return TREND_RANGE_DAYS.find((days) => days * 24 >= windowHours) ?? TREND_RANGE_DAYS[TREND_RANGE_DAYS.length - 1];
}

/**
 * The range picked for one recommendation. A pick never carries over to the
 * next recommendation the drawer shows: that one starts from its own window.
 */
export function useTrendRange(recommendationId: unknown, defaultDays: TrendRangeDays): [TrendRangeDays, (days: TrendRangeDays) => void] {
  const [picked, setPicked] = useState<{ id: unknown; days: TrendRangeDays } | null>(null);
  const days = picked && picked.id === recommendationId ? picked.days : defaultDays;
  return [days, (next) => setPicked({ id: recommendationId, days: next })];
}

const OPTIONS = TREND_RANGE_DAYS.map((days) => ({ value: `${days}d`, label: `${days}d` }));

export const TrendRangeToggle = ({ value, onChange }: { value: TrendRangeDays; onChange: (days: TrendRangeDays) => void }) => (
  <Box data-testid='trend-range-toggle' sx={{ display: 'flex', justifyContent: 'flex-end' }}>
    <Toggle size='sm' options={OPTIONS} activeValue={`${value}d`} onChange={(v) => onChange(parseInt(v, 10) as TrendRangeDays)} />
  </Box>
);
