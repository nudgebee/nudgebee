import React from 'react';
import { ButtonBase, Box, Typography } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import type { VmFleetMetric } from '@api1/vm';
import { ds } from '@utils/colors';

/** Load at or above these shares reads as busy / critical. */
const WARN_AT = 70;
const CRITICAL_AT = 90;

export const gaugeTone = (pct: number) => (pct >= CRITICAL_AT ? ds.red[500] : pct >= WARN_AT ? ds.amber[500] : ds.green[500]);

// A half-circle from 180° to 0°; pathLength=100 makes the dash length read as a percentage.
const ARC = 'M 6 26 A 20 20 0 0 1 46 26';

/** One half-circle gauge: the share in the middle, what it measures underneath. */
export const Gauge = ({ label, value, scale = 1 }: { label: string; value?: number; scale?: number }) => {
  const known = value !== undefined;
  const clamped = known ? Math.min(100, Math.max(0, value as number)) : 0;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', lineHeight: 1 }} data-testid={`gauge-${label.toLowerCase()}`}>
      <svg
        width={52 * scale}
        height={30 * scale}
        viewBox='0 0 52 30'
        role='img'
        aria-label={known ? `${label} ${Math.round(clamped)}%` : `${label} unknown`}
      >
        <path d={ARC} fill='none' stroke={ds.gray[200]} strokeWidth='5' strokeLinecap='round' />
        {known && clamped > 0 ? (
          <path
            d={ARC}
            fill='none'
            stroke={gaugeTone(clamped)}
            strokeWidth='5'
            strokeLinecap='round'
            pathLength='100'
            strokeDasharray={`${clamped} 100`}
          />
        ) : null}
        <text x='26' y='24' textAnchor='middle' style={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, fill: ds.gray[700] }}>
          {known ? `${Math.round(clamped)}%` : '—'}
        </text>
      </svg>
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{label}</Typography>
    </Box>
  );
};

interface VmMetricsCellProps {
  vmName: string;
  /** Undefined when no node-agent reports for this VM (or it has not loaded yet). */
  metric?: VmFleetMetric;
  loading?: boolean;
  onOpen: () => void;
}

/**
 * The Metrics column: CPU and RAM gauges for a VM. The whole cell is a button that opens the
 * history. A VM with nothing to show is still clickable, because the history view says why
 * (no agent, store unreachable, ...), which is the thing an empty cell cannot.
 */
export const VmMetricsCell = ({ vmName, metric, loading, onOpen }: VmMetricsCellProps) => {
  if (!metric) {
    return (
      <Tooltip title={loading ? 'Loading metrics' : 'No metrics for this VM. Click to see why.'}>
        <ButtonBase
          onClick={onOpen}
          aria-label={`Show metrics for ${vmName}`}
          sx={{ color: ds.gray[400], fontSize: ds.text.small, padding: ds.space[1], borderRadius: ds.radius.sm }}
        >
          {loading ? '…' : '—'}
        </ButtonBase>
      </Tooltip>
    );
  }
  return (
    <ButtonBase
      onClick={onOpen}
      aria-label={`Show CPU and memory history for ${vmName}`}
      sx={{ display: 'flex', gap: ds.space[3], padding: ds.space[1], borderRadius: ds.radius.sm, '&:hover': { backgroundColor: ds.gray[100] } }}
    >
      <Gauge label='CPU' value={metric.cpuPct} />
      <Gauge label='RAM' value={metric.memPct} />
    </ButtonBase>
  );
};
