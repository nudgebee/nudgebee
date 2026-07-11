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

export function AgentLatencyBars({ profiles, thresholdSeconds, percentile, onSelectAgent, id }: AgentLatencyBarsProps) {
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
  const thresholdLine = React.useMemo(
    () => ({
      id: 'agentLatencyThreshold',
      afterDraw(chart: any) {
        if (!thresholdSeconds) return;
        const { ctx, chartArea, scales } = chart;
        const x = scales.x?.getPixelForValue(thresholdSeconds);
        if (x == null || x < chartArea.left || x > chartArea.right) return;
        ctx.save();
        ctx.strokeStyle = 'rgba(0,0,0,0.5)';
        ctx.setLineDash([4, 4]);
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(x, chartArea.top);
        ctx.lineTo(x, chartArea.bottom);
        ctx.stroke();
        ctx.setLineDash([]);
        ctx.fillStyle = 'rgba(0,0,0,0.6)';
        ctx.font = '10px sans-serif';
        ctx.fillText(`p${percentile} = ${fmtDuration(ms(thresholdSeconds))}`, Math.min(x + 4, chartArea.right - 60), chartArea.top + 10);
        ctx.restore();
      },
    }),
    [thresholdSeconds, percentile]
  );

  const options = React.useMemo(
    () => ({
      indexAxis: 'y' as const,
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: 450, easing: 'easeOutQuart' as const },
      interaction: { intersect: false, mode: 'index' as const },
      onClick: (_e: unknown, els: { index: number }[]) => {
        if (els?.length) onSelectAgent(profiles[els[0].index]?.agent_name ?? '');
      },
      plugins: {
        legend: { display: false },
        tooltip: { enabled: false, external: makeExternalTooltip((raw) => fmtDuration(ms(Number(raw)))) },
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
      <Box sx={{ position: 'relative', height, width: '100%', minWidth: 0, overflow: 'hidden' }}>
        <Chart.Bar labels={labels} dataset={datasets} options={options} customPlugins={[thresholdLine, valueLabels]} maxHeight={height} />
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
      </Box>
    </Box>
  );
}

export default AgentLatencyBars;
