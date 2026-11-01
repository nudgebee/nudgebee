import React from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import SeriesChart from '@shared/charts/SeriesChart';
import { resolveDatasetColor } from '@shared/charts/chartPlugins';
import { ds } from '@utils/colors';
import { formatDateTime } from '@lib/datetime';

// What the tune form's numbers are based on: how much memory the container
// actually used, next to the limit it has. Without it the form shows a current
// value and a proposal with nothing to judge the proposal against.

const MIB = 1024 * 1024;

const formatMb = (mb) => `${Math.round(mb).toLocaleString()} MB`;

// Samples arrive in bytes; the event form works in MiB (labelled MB).
const usageInMb = (usage) => (usage?.samples || []).map((v) => Number(v) / MIB).filter((v) => Number.isFinite(v));

const median = (values) => {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
};

// A dashed horizontal line at the current limit, labelled, so the curve reads
// against the ceiling it hit.
const limitLine = (limitMb, color) => ({
  id: 'memoryLimitLine',
  afterDatasetsDraw(chart) {
    const { ctx, chartArea, scales } = chart;
    if (!scales.y || !(limitMb > 0)) return;
    const y = scales.y.getPixelForValue(limitMb);
    if (y < chartArea.top || y > chartArea.bottom) return;
    ctx.save();
    ctx.strokeStyle = color;
    ctx.setLineDash([4, 3]);
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(chartArea.left, y);
    ctx.lineTo(chartArea.right, y);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.font = '10px sans-serif';
    ctx.fillStyle = color;
    ctx.textAlign = 'right';
    ctx.textBaseline = 'bottom';
    ctx.fillText('limit', chartArea.right - 2, y - 2);
    ctx.restore();
  },
});

export const MemoryUsageFacts = ({ usage, limitMb }) => {
  const values = usageInMb(usage);
  if (values.length === 0) return null;
  const peak = Math.max(...values);
  const times = usage.times || [];
  const spanMinutes = times.length > 1 ? Math.round((times[times.length - 1] - times[0]) / 60) : 0;
  const ceiling = Math.max(peak, Number(limitMb) || 0) * 1.08;

  return (
    <Box sx={{ width: '100%', display: 'flex', flexDirection: 'column', gap: ds.space[1] }} data-testid='memory-usage-facts'>
      <Box sx={{ height: ds.space.mul(0, 40), width: '100%' }}>
        <SeriesChart
          id='tune-memory-usage'
          maxHeight={40}
          labels={times.map(formatDateTime)}
          dataset={[
            {
              label: 'Memory in use',
              data: values,
              borderColor: ds.blue[500],
              borderWidth: 2,
              pointRadius: 0,
              fill: false,
            },
          ]}
          options={{
            responsive: true,
            maintainAspectRatio: false,
            animation: false,
            interaction: { intersect: false, mode: 'index' },
            plugins: {
              legend: { display: false },
              tooltip: { callbacks: { label: (item) => formatMb(item.parsed.y) } },
            },
            scales: { x: { display: false }, y: { display: false, min: 0, max: ceiling } },
          }}
          customPlugins={[limitLine(Number(limitMb), resolveDatasetColor(ds.red[500]))]}
        />
      </Box>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>
        Highest <b>{formatMb(peak)}</b> · typical {formatMb(median(values))}
        {spanMinutes > 0 ? ` · over ${spanMinutes} min` : ''}
      </Typography>
      {usage.scope === 'pod' && (
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}>
          Whole pod, all containers — nothing was recorded for this container on its own.
        </Typography>
      )}
    </Box>
  );
};

MemoryUsageFacts.propTypes = {
  usage: PropTypes.shape({
    samples: PropTypes.array,
    times: PropTypes.array,
    scope: PropTypes.oneOf(['container', 'pod']),
  }),
  limitMb: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
};

const CheckLine = ({ ok, children }) => (
  <Typography sx={{ fontSize: ds.text.small, color: ok ? ds.green[700] : ds.red[600], display: 'flex', gap: ds.space[1] }}>
    <span aria-hidden>{ok ? '✓' : '✕'}</span>
    <span>{children}</span>
  </Typography>
);

CheckLine.propTypes = { ok: PropTypes.bool, children: PropTypes.node };

// The proposal measured against what was actually used, recomputed as the
// user changes the buffer or types a value.
export const MemoryHeadroomCheck = ({ usage, request, limit }) => {
  const values = usageInMb(usage);
  // The event form takes plain MB numbers and refuses anything else on submit
  // (e.g. "1Gi"), so read the value the same way: not a number, no check.
  const limitMb = limit === '' || limit == null ? NaN : Number(limit);
  const requestMb = request === '' || request == null ? NaN : Number(request);
  // Needs no usage to be true, so it is not gated on the usage series below.
  const requestAboveLimit = Number.isFinite(requestMb) && Number.isFinite(limitMb) && requestMb > limitMb;
  if (values.length === 0 && !requestAboveLimit) return null;
  const peak = values.length > 0 ? Math.max(...values) : NaN;
  const whose = usage?.scope === 'pod' ? 'the pod' : 'it';

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }} data-testid='memory-headroom-check'>
      {requestAboveLimit && <CheckLine ok={false}>Request is above the limit ({formatMb(limitMb)}) — Kubernetes will refuse it.</CheckLine>}
      {Number.isFinite(limitMb) &&
        Number.isFinite(peak) &&
        (limitMb >= peak ? (
          <CheckLine ok>
            Limit is {formatMb(limitMb - peak)} above the most {whose} has used ({formatMb(peak)}).
          </CheckLine>
        ) : (
          <CheckLine ok={false}>
            Limit is below the most {whose} has used ({formatMb(peak)}) — it would run out of memory again.
          </CheckLine>
        ))}
      {Number.isFinite(requestMb) && Number.isFinite(peak) && requestMb < peak && (
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
          Reserves {formatMb(requestMb)} while {whose} has used up to {formatMb(peak)}. Room on the node is not checked.
        </Typography>
      )}
    </Box>
  );
};

MemoryHeadroomCheck.propTypes = {
  usage: MemoryUsageFacts.propTypes.usage,
  request: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  limit: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
};
