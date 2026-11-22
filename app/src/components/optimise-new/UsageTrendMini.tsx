import { Box, Typography } from '@mui/material';
import { ds, resolveColor } from '@utils/colors';
import Chart from '@ui/Chart';
import { baseChartOptions, withAlpha } from '@shared/charts/chartPlugins';
import type { TrendSeries, ResourceSeries } from './evidence/trendSeries';

/**
 * UsageTrendMini — the usage chart that carries the recommendation, drawn
 * directly under the change it justifies: per-pod usage over the drawer's range, with the
 * current request and the recommended request as lines on the same axis. The
 * gap between them is the argument for the change, read without a sentence.
 * The Evidence tab draws the same series at full size with the limit and the
 * busiest pod; this one keeps only what decides the apply.
 */
interface UsageTrendMiniProps {
  series: TrendSeries | null;
  loading: boolean;
  /** "per pod" or "all replicas" — what the y values are. */
  basis: string;
  /** Days the series covers. */
  rangeDays: number;
}

const CHART_HEIGHT = 128;

const formatCores = (v: number): string => (Math.abs(v) >= 1 ? `${v.toFixed(2)}` : `${Math.round(v * 1000)}m`);
const formatMi = (v: number): string => (Math.abs(v) >= 1024 ? `${(v / 1024).toFixed(1)} Gi` : `${Math.round(v)} Mi`);

// Three lines, in the order the legend lists them: what happened, what is
// proposed, what is set today. Colours match the Evidence tab's charts so the
// two read as one series at two sizes.
function datasets(res: ResourceSeries, usageColor: string) {
  const usageHex = resolveColor(usageColor);
  return [
    {
      label: 'Usage',
      data: res.usage,
      borderColor: usageColor,
      backgroundColor: withAlpha(usageHex, 0.1),
      fill: true,
      borderWidth: 2,
      pointRadius: 0,
      pointHitRadius: 12,
      tension: 0.3,
      spanGaps: true,
    },
    ...(res.recommended
      ? [
          {
            label: 'Recommended',
            data: res.recommended,
            borderColor: ds.green[600],
            backgroundColor: 'transparent',
            fill: false,
            borderWidth: 2,
            pointRadius: 0,
            pointHitRadius: 12,
          },
        ]
      : []),
    {
      label: 'Requested',
      data: res.request,
      borderColor: ds.gray[400],
      backgroundColor: 'transparent',
      fill: false,
      borderWidth: 1.5,
      pointRadius: 0,
      pointHitRadius: 12,
      spanGaps: true,
    },
  ];
}

function options(format: (v: number) => string) {
  const base: any = baseChartOptions((raw) => format(Number(raw)), { yTickFormat: format });
  // The shared base hides the legend; with three series the key is what makes
  // the lines readable without colour, so it comes back, as line keys.
  base.plugins.legend = {
    display: true,
    position: 'bottom' as const,
    align: 'start' as const,
    labels: { boxWidth: 12, boxHeight: 2, padding: 12, font: { size: 10 }, color: resolveColor(ds.gray[500]) },
  };
  // The shared external tooltip is built for stacked series: it adds a %-share
  // column and a Total row, which for usage against two reference lines would
  // read as nonsense ("Total 840512"). The built-in tooltip lists the three
  // values at the hovered time and nothing else.
  base.plugins.tooltip = {
    enabled: true,
    mode: 'index' as const,
    intersect: false,
    displayColors: true,
    boxWidth: 8,
    boxHeight: 8,
    padding: 8,
    titleFont: { size: 11 },
    bodyFont: { size: 11 },
    callbacks: {
      // A gap (null) or a value that does not read as a number shows a dash,
      // never "0m" pretending to be a reading.
      label: (item: any) => {
        const value = Number(item.raw);
        return ` ${item.dataset.label}: ${item.raw == null || !Number.isFinite(value) ? '—' : format(value)}`;
      },
    },
  };
  // Two date ticks, day only: the full "Sep 12, 05:30 PM" labels collide at
  // this width, and the hour is in the tooltip.
  base.scales.x.ticks.maxTicksLimit = 2;
  base.scales.x.ticks.callback = function (this: any, value: number) {
    const label = String(this.getLabelForValue(value) ?? '');
    return label.split(',')[0];
  };
  base.scales.y.ticks.maxTicksLimit = 3;
  base.scales.y.beginAtZero = true;
  base.animation = { duration: 300, easing: 'easeOutQuart' as const };
  return base;
}

const Placeholder = () => <Box sx={{ height: CHART_HEIGHT, borderRadius: ds.radius.md, backgroundColor: ds.gray[100] }} />;

const Panel = ({ title, basis, rangeDays, children }: { title: string; basis: string; rangeDays: number; children: React.ReactNode }) => (
  <Box sx={{ minWidth: 0, border: `1px solid ${ds.gray[200]}`, borderRadius: ds.radius.md, p: ds.space[2] }}>
    <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: ds.space[2], mb: ds.space[1] }}>
      <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{title}</Typography>
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], whiteSpace: 'nowrap' }}>{`${basis} · last ${rangeDays} days`}</Typography>
    </Box>
    {children}
  </Box>
);

const UsageTrendMini = ({ series, loading, basis, rangeDays }: UsageTrendMiniProps) => {
  if (!loading && !series) return null;
  return (
    <Box data-testid='usage-trend-mini' sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: ds.space[2] }}>
      <Panel title='CPU' basis={basis} rangeDays={rangeDays}>
        {series ? (
          <Box sx={{ height: CHART_HEIGHT }}>
            <Chart.Series
              id='usage-trend-mini-cpu'
              labels={series.labels}
              dataset={datasets(series.cpu, ds.blue[500])}
              options={options(formatCores)}
              maxHeight={CHART_HEIGHT}
            />
          </Box>
        ) : (
          <Placeholder />
        )}
      </Panel>
      <Panel title='Memory' basis={basis} rangeDays={rangeDays}>
        {series ? (
          <Box sx={{ height: CHART_HEIGHT }}>
            <Chart.Series
              id='usage-trend-mini-memory'
              labels={series.labels}
              dataset={datasets(series.memory, ds.purple[500])}
              options={options(formatMi)}
              maxHeight={CHART_HEIGHT}
            />
          </Box>
        ) : (
          <Placeholder />
        )}
      </Panel>
    </Box>
  );
};

export default UsageTrendMini;
