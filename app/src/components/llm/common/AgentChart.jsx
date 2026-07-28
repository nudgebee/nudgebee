import PropTypes from 'prop-types';
import React from 'react';
import { Box, Typography } from '@mui/material';
import { ds } from '@utils/colors';
import Chart from '@ui/Chart';
import { SERIES_PALETTE } from '@shared/charts/chartPlugins';

/**
 * AgentChart renders a chart from an agent-emitted ```nb-chart spec embedded in a
 * chat answer. The agent chooses the chart `type` and supplies already-derived
 * numbers (e.g. provisioned vs used per resource, spend share by service), so the
 * spec stays small and dynamic per question — nothing about the chart is fixed.
 *
 * It is deliberately defensive: any malformed, oversized, or non-numeric spec
 * renders nothing (returns null) so a bad chart can never break the surrounding
 * answer. The table the agent renders alongside the chart remains the source of
 * truth; the chart is an aid.
 *
 * Spec shape:
 *   {
 *     "type": "bar" | "line" | "area" | "doughnut",
 *     "title": "Provisioned vs Used (Gi)",
 *     "labels": ["storage-loki-0", ...],
 *     "series": [{ "key": "Provisioned", "data": [100, ...] }, ...],  // bar/line/area
 *     "values": [3.4, 23.2, ...],                                     // doughnut (alt to series)
 *     "format": "gi" | "usd" | "percent" | "number"
 *   }
 */

// Bounds keep an LLM from emitting a runaway spec that would clutter or stall the UI.
const MAX_LABELS = 16;
const MAX_SERIES = 6;
const ALLOWED_TYPES = ['bar', 'line', 'area', 'doughnut', 'pie'];

// Types that render as a circular share chart (Chart.Doughnut). `pie` is the same
// component with no center cutout.
const CIRCULAR_TYPES = ['doughnut', 'pie'];

const numberFmt = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 });

const FORMATTERS = {
  usd: (v) => `$${numberFmt.format(v)}`,
  gi: (v) => `${numberFmt.format(v)} Gi`,
  percent: (v) => `${numberFmt.format(v)}%`,
  number: (v) => numberFmt.format(v),
};

const isFiniteNumber = (v) => typeof v === 'number' && Number.isFinite(v);

/**
 * Validate and normalize the raw spec. Returns null when the spec is unusable so
 * the caller renders nothing instead of throwing.
 */
export const parseChartSpec = (raw) => {
  if (!raw || typeof raw !== 'object') {
    return null;
  }
  const type = typeof raw.type === 'string' ? raw.type.toLowerCase() : '';
  if (!ALLOWED_TYPES.includes(type)) {
    return null;
  }
  const labels = Array.isArray(raw.labels) ? raw.labels.map((l) => String(l)) : [];
  if (labels.length === 0 || labels.length > MAX_LABELS) {
    return null;
  }
  const format = FORMATTERS[raw.format] ? raw.format : 'number';
  const title = typeof raw.title === 'string' ? raw.title : '';

  if (CIRCULAR_TYPES.includes(type)) {
    // Doughnut/pie accept either `values` or the first series' data.
    const values = Array.isArray(raw.values)
      ? raw.values
      : Array.isArray(raw.series) && raw.series[0] && Array.isArray(raw.series[0].data)
      ? raw.series[0].data
      : null;
    if (!values || values.length !== labels.length || !values.every(isFiniteNumber)) {
      return null;
    }
    return { type, title, labels, values, format };
  }

  // bar / line / area need one or more numeric series aligned to labels.
  if (!Array.isArray(raw.series) || raw.series.length === 0 || raw.series.length > MAX_SERIES) {
    return null;
  }
  const series = [];
  for (const s of raw.series) {
    if (!s || typeof s.key !== 'string' || !Array.isArray(s.data)) {
      return null;
    }
    if (s.data.length !== labels.length || !s.data.every(isFiniteNumber)) {
      return null;
    }
    series.push({ key: s.key, data: s.data });
  }
  return { type, title, labels, series, format };
};

const AgentChart = ({ spec }) => {
  const parsed = parseChartSpec(spec);
  if (!parsed) {
    return null;
  }

  const fmt = FORMATTERS[parsed.format];

  let chart;
  if (CIRCULAR_TYPES.includes(parsed.type)) {
    // Chart on the left, a full legend on the right — uses the horizontal space a
    // doughnut otherwise leaves empty and lists every slice (the built-in Chart.js
    // legend clips when there are many labels) with its value and share.
    const colors = parsed.labels.map((_, i) => SERIES_PALETTE[i % SERIES_PALETTE.length]);
    const total = parsed.values.reduce((a, b) => a + b, 0);
    chart = (
      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: ds.space[4] }}>
        <Box sx={{ flex: '0 0 auto' }}>
          {/* Pass a blank center to suppress the component's default "0" text.
              We intentionally do NOT show a "Total" here: a chart often plots only
              a top-N subset, so a centered sum would understate and contradict the
              true total stated in the answer text. Per-slice values live in the
              legend. */}
          <Chart.Doughnut
            values={parsed.values}
            labels={parsed.labels}
            size={190}
            colors={colors}
            cutout={parsed.type === 'pie' ? '0%' : '62%'}
            enableTooltip
            externalTooltip
            formatValue={(raw) => fmt(raw)}
            centerValue={' '}
          />
        </Box>
        <Box sx={{ flex: '1 1 260px', minWidth: 220, display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
          {parsed.labels.map((label, i) => (
            <Box key={label + i} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], fontFamily: ds.font.sans }}>
              <Box sx={{ flex: '0 0 auto', width: 10, height: 10, borderRadius: '2px', backgroundColor: colors[i] }} />
              <Box
                sx={{
                  flex: 1,
                  minWidth: 0,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  fontSize: 'var(--ds-text-small)',
                  color: 'var(--ds-gray-700)',
                }}
                title={label}
              >
                {label}
              </Box>
              <Box sx={{ flex: '0 0 auto', fontSize: 'var(--ds-text-small)', fontWeight: 600, color: 'var(--ds-gray-800)' }}>
                {fmt(parsed.values[i])}
              </Box>
              <Box sx={{ flex: '0 0 auto', fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', minWidth: 44, textAlign: 'right' }}>
                {total > 0 ? `${((parsed.values[i] / total) * 100).toFixed(1)}%` : '—'}
              </Box>
            </Box>
          ))}
        </Box>
      </Box>
    );
  } else {
    chart = (
      <Chart.TimeSeries
        labels={parsed.labels}
        series={parsed.series}
        shape={parsed.type}
        format={fmt}
        compactFormat={fmt}
        integerY={parsed.format === 'number'}
        height={260}
      />
    );
  }

  return (
    <Box
      data-testid='agent-chart'
      sx={{
        width: '100%',
        my: ds.space[3],
        p: ds.space[3],
        borderRadius: ds.radius.lg,
        border: `1px solid var(--ds-gray-200)`,
        backgroundColor: 'var(--ds-gray-50)',
      }}
    >
      {parsed.title ? (
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontWeight: 600,
            color: 'var(--ds-gray-700)',
            fontFamily: ds.font.sans,
            mb: ds.space[2],
          }}
        >
          {parsed.title}
        </Typography>
      ) : null}
      {chart}
    </Box>
  );
};

AgentChart.propTypes = {
  spec: PropTypes.object,
};

export default AgentChart;
