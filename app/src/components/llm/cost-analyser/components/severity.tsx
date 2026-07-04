/**
 * severity — relative (percentile-based) outlier highlighting for the cost/latency
 * columns in the analyser's listings.
 *
 * Thresholds are NOT fixed numbers: LLM cost/latency span orders of magnitude
 * across models, so any absolute cutoff is wrong for most rows. Instead each
 * column is ranked against the values currently in view — top 10% = high (red),
 * top 25% = mid (amber). Self-calibrating, no config; "alert me when…" stays with
 * the anomaly subsystem, not the table.
 *
 * Pair the dot with a tooltip (done here) — colour alone isn't accessible.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type Severity = 'high' | 'mid' | 'none';

/** Below this row count, percentiles aren't meaningful — don't colour anything. */
const MIN_SAMPLE = 5;

function percentileOf(sortedAsc: number[], p: number): number {
  if (!sortedAsc.length) return 0;
  const idx = Math.min(sortedAsc.length - 1, Math.floor((p / 100) * sortedAsc.length));
  return sortedAsc[idx];
}

/**
 * Build a classifier from a column's values: `>= p90` → high, `>= p75` → mid.
 * Ignores non-positive/non-finite values, needs a minimum sample, and no-ops on a
 * flat distribution (so a column of equal values never lights up).
 */
export function makeSeverity(values: number[]): (v: number) => Severity {
  const nums = values.filter((v) => Number.isFinite(v) && v > 0).sort((a, b) => a - b);
  if (nums.length < MIN_SAMPLE) return () => 'none';
  const p75 = percentileOf(nums, 75);
  const p90 = percentileOf(nums, 90);
  if (p90 <= p75) return () => 'none'; // degenerate / flat
  return (v) => (v >= p90 ? 'high' : v >= p75 ? 'mid' : 'none');
}

/**
 * SeverityDot — a small colour dot flagging a relatively high value, with an
 * explanatory tooltip. Renders nothing for `none` (the common case), so the table
 * stays calm and only outliers draw the eye.
 */
function SeverityDot({ severity, metric }: { severity: Severity; metric: string }) {
  if (severity === 'none') return null;
  const high = severity === 'high';
  const color = high ? 'var(--ds-red-500)' : 'var(--ds-amber-500)';
  const title = high ? `High ${metric} — top 10% in this view` : `Elevated ${metric} — top 25% in this view`;
  return (
    <Box
      component='span'
      title={title}
      aria-label={title}
      sx={{ display: 'inline-block', width: 7, height: 7, borderRadius: '50%', backgroundColor: color, flexShrink: 0 }}
    />
  );
}

/** Wrap a numeric cell with a leading severity dot (no-op when severity is none). */
export function SeverityCell({ severity, metric, children }: { severity: Severity; metric: string; children: React.ReactNode }) {
  // Common case (normal rows): render children as-is — no extra wrapper node, so
  // cell alignment matches non-severity columns.
  if (severity === 'none') return <>{children}</>;
  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
      <SeverityDot severity={severity} metric={metric} />
      {children}
    </Box>
  );
}
