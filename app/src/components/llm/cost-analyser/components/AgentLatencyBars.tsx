/**
 * AgentLatencyBars — agent-wise latency profile (horizontal grouped bars).
 *
 * One group per agent name (top-N by p90), three bars each: p50 / p90 / p99 of
 * that agent's per-invocation total model latency over the report window. A
 * dashed vertical reference line marks the global pXX threshold (the same number
 * the latency filter uses) — bars crossing it are the outlier agents. Clicking a
 * bar drills the table into that agent.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import Chart from '@ui/Chart';
import { Chip } from '@ui/Chip';
import { ProgressLinear } from '@ui/ProgressLinear';
import { PASTEL_PALETTE } from './palette';
import { makeExternalTooltip } from './chartKit';
import { fmtDuration } from '../format';
import type { AgentLatencyProfile } from '@api1/ai-cost';

interface AgentLatencyBarsProps {
  profiles: AgentLatencyProfile[];
  /** Global pXX threshold (seconds) for the reference line; 0 = no line. */
  thresholdSeconds: number;
  /** The pXX selected (for the reference-line label); 0 = none. */
  percentile: number;
  /** Drill the table into a single agent. */
  onSelectAgent: (agentName: string) => void;
  /** A refetch (filter/threshold change) is in flight — keep the stale chart visible, dimmed, with a progress cue. */
  loading?: boolean;
  id?: string;
}

const SERIES: { key: 'p50_seconds' | 'p90_seconds' | 'p99_seconds'; label: string; color: string }[] = [
  { key: 'p50_seconds', label: 'p50 (median)', color: PASTEL_PALETTE[1] }, // sage
  { key: 'p90_seconds', label: 'p90', color: PASTEL_PALETTE[0] }, // sky
  { key: 'p99_seconds', label: 'p99', color: PASTEL_PALETTE[3] }, // blush
];

const ms = (s: number) => s * 1000;

// Severity tint for bars at/over the threshold — outliers read red at a glance.
const OVER_THRESHOLD = '#D9534F';

export function AgentLatencyBars({ profiles, thresholdSeconds, percentile, onSelectAgent, loading = false, id }: AgentLatencyBarsProps) {
  // react-chartjs-2 registers inline plugins once, at chart construction, and never
  // re-registers them when the `plugins` prop changes — so a plugin that closes over
  // thresholdSeconds/percentile keeps drawing the mount-time values (the default p90
  // line) even after the latency filter changes. Keep the live values in a ref the
  // render updates, and have the (stable) reference-line plugin read from it at draw
  // time, so the dashed line always tracks the selected pXX.
  const refLine = React.useRef({ thresholdSeconds, percentile });
  refLine.current = { thresholdSeconds, percentile };

  // Ref to the threshold-line Chip overlay's DOM node. `afterDraw` runs on every
  // Chart.js animation frame (~60fps during a transition), so routing its position
  // through React state (setState per frame) would trigger a synchronous re-render
  // every frame — layout thrashing and animation lag. Instead the plugin below writes
  // directly to this node's style, bypassing React's render cycle entirely.
  const overlayRef = React.useRef<HTMLDivElement>(null);

  const labels = profiles.map((p) => p.agent_name || 'agent');
  const datasets = SERIES.map((s) => ({
    label: s.label,
    data: profiles.map((p) => Number((p[s.key] ?? 0).toFixed(2))),
    // Per-bar colour: a bar at/over the active pXX threshold turns red (severity).
    backgroundColor: profiles.map((p) => (thresholdSeconds > 0 && (p[s.key] ?? 0) >= thresholdSeconds ? OVER_THRESHOLD : s.color)),
    borderRadius: 3,
    maxBarThickness: 14,
  }));

  // Value labels at the end of each bar — read p50/p90/p99 without hovering.
  const valueLabels = React.useMemo(
    () => ({
      id: 'agentLatencyValueLabels',
      afterDatasetsDraw(chart: any) {
        const { ctx, chartArea } = chart;
        ctx.save();
        ctx.font = '9px sans-serif';
        ctx.fillStyle = 'rgba(0,0,0,0.55)';
        ctx.textAlign = 'left';
        ctx.textBaseline = 'middle';
        chart.data.datasets.forEach((ds: any, di: number) => {
          if (!chart.isDatasetVisible(di)) return;
          const meta = chart.getDatasetMeta(di);
          meta.data?.forEach((bar: any, i: number) => {
            const val = ds.data?.[i];
            if (!val) return;
            const x = Math.min(bar.x + 4, chartArea.right - 30);
            ctx.fillText(fmtDuration(ms(val)), x, bar.y);
          });
        });
        ctx.restore();
      },
    }),
    []
  );

  // Dashed reference line at x = threshold (value axis is x for horizontal bars).
  // Reads the live threshold/percentile from the ref (see above) rather than closing
  // over them, so it stays a single stable plugin that redraws to the selected pXX.
  const thresholdLine = React.useMemo(
    () => ({
      id: 'agentLatencyThreshold',
      afterDraw(chart: any) {
        const { thresholdSeconds } = refLine.current;
        const el = overlayRef.current;
        if (!thresholdSeconds) {
          if (el) el.style.display = 'none';
          return;
        }
        const { ctx, chartArea, scales } = chart;
        const x = scales.x?.getPixelForValue(thresholdSeconds);
        if (x == null || x < chartArea.left || x > chartArea.right) {
          if (el) el.style.display = 'none';
          return;
        }
        ctx.save();
        ctx.strokeStyle = 'rgba(0,0,0,0.5)';
        ctx.setLineDash([4, 4]);
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(x, chartArea.top);
        ctx.lineTo(x, chartArea.bottom);
        ctx.stroke();
        ctx.setLineDash([]);
        ctx.restore();
        // The label itself renders as a DS Chip DOM overlay (see `overlayRef` above)
        // rather than canvas text — position it directly, no React state involved.
        if (el) {
          el.style.display = 'block';
          el.style.left = `${x}px`;
          el.style.top = `${chartArea.top - 4}px`;
        }
      },
    }),
    []
  );

  const options = React.useMemo(
    () => ({
      indexAxis: 'y' as const,
      responsive: true,
      maintainAspectRatio: false,
      // Room above the plot area for the threshold Chip overlay, which would otherwise
      // sit on top of the first row's bar.
      layout: { padding: { top: 22 } },
      animation: { duration: 450, easing: 'easeOutQuart' as const },
      // Chart.js's bar controller only animates geometry (x/y/width/height) by default —
      // backgroundColor isn't a registered animation group, so a bar flipping red/normal
      // when the threshold changes snapped instantly. Register `colors` as an animated
      // group (inherits duration/easing from `animation` above) so it fades instead.
      animations: {
        colors: {
          type: 'color' as const,
          properties: ['backgroundColor'],
        },
      },
      // Chart.js's `index` mode defaults to finding the nearest item by X-axis proximity
      // regardless of `indexAxis` — for these horizontal bars (categories on Y) that means
      // hovering a new row wouldn't reliably swap the active index, so the tooltip kept
      // showing the previous row. Force it to index along Y instead.
      interaction: { intersect: false, mode: 'index' as const, axis: 'y' as const },
      onClick: (_e: unknown, els: { index: number }[]) => {
        if (els?.length) onSelectAgent(profiles[els[0].index]?.agent_name ?? '');
      },
      plugins: {
        legend: { display: false },
        // Chart.js tweens tooltip position/opacity over ~400ms by default, even with a
        // custom `external` renderer — with these densely-packed rows that tween never
        // catches up to the cursor and the tooltip visibly lags/slides across neighbouring
        // bars. The external DOM tooltip already has its own CSS opacity transition, so
        // disable Chart.js's internal one.
        tooltip: { enabled: false, animation: false, external: makeExternalTooltip((raw) => fmtDuration(ms(Number(raw)))) },
      },
      scales: {
        x: {
          grid: { color: 'rgba(0,0,0,0.06)', drawBorder: false },
          ticks: { color: 'rgba(0,0,0,0.45)', font: { size: 10 }, callback: (v: number) => `${v}s` },
        },
        y: {
          grid: { display: false, drawBorder: false },
          ticks: { color: 'rgba(0,0,0,0.6)', font: { size: 10 }, autoSkip: false },
        },
      },
    }),
    [profiles, onSelectAgent]
  );

  // Height scales with agent count so groups stay legible; clamp to a sane range.
  const height = Math.min(440, Math.max(160, profiles.length * 34));

  return (
    <Box id={id} sx={{ minWidth: 0 }}>
      {/* Refetch in flight (filter/threshold change) — keep the stale chart visible
          underneath rather than swapping to a Skeleton, since only the data changed. */}
      {loading && (
        <Box sx={{ mb: 'var(--ds-space-1)' }}>
          <ProgressLinear mode='indeterminate' tone='info' surface='section' aria-label='Refreshing agent latency data' />
        </Box>
      )}
      <Box
        sx={{
          position: 'relative',
          height,
          width: '100%',
          minWidth: 0,
          overflow: 'hidden',
          opacity: loading ? 0.75 : 1,
          pointerEvents: loading ? 'none' : 'auto',
          transition: 'opacity 200ms ease',
        }}
      >
        <Chart.Bar labels={labels} dataset={datasets} options={options} customPlugins={[thresholdLine, valueLabels]} maxHeight={height} />
        {thresholdSeconds > 0 && (
          <Box
            ref={overlayRef}
            sx={{
              position: 'absolute',
              left: 0,
              top: 0,
              transform: 'translate(-50%, -100%)',
              pointerEvents: 'none',
              transition: 'left 450ms ease, top 450ms ease',
              display: 'none',
            }}
          >
            <Chip size='2xs' tone='neutral'>{`p${percentile} = ${fmtDuration(ms(thresholdSeconds))}`}</Chip>
          </Box>
        )}
      </Box>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2) var(--ds-space-4)', mt: 'var(--ds-space-2)' }}>
        {SERIES.map((s) => (
          <Box
            key={s.key}
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--ds-space-1)',
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-600)',
            }}
          >
            <Box sx={{ width: 9, height: 9, borderRadius: 2, backgroundColor: s.color, flexShrink: 0 }} />
            {s.label}
          </Box>
        ))}
        {thresholdSeconds > 0 && (
          <Box
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--ds-space-1)',
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-600)',
            }}
          >
            <Box sx={{ width: 9, height: 9, borderRadius: 2, backgroundColor: OVER_THRESHOLD, flexShrink: 0 }} />
            {`≥ p${percentile} threshold`}
          </Box>
        )}
      </Box>
    </Box>
  );
}

export default AgentLatencyBars;
