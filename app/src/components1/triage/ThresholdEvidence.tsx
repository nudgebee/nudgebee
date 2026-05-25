import React, { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  BarElement,
  PointElement,
  LineElement,
  Title,
  Tooltip as ChartTooltip,
  Legend,
} from 'chart.js';
import { Bar } from 'react-chartjs-2';
import { Text } from '@components1/common';
import { Chip } from '@components1/ds/Chip';

import LineChart from '@components1/common/charts/LineCharts';
import observability from '@api1/observability';
import k8sApi from '@api1/kubernetes';
import { getDateString } from '@lib/datetime';
import KubernetesEventsTable from '@components1/events/KubernetesEvents';
import { type ThresholdSuggestionItem } from '@api1/triage';
import WidgetCard from '@components1/ds/WidgetCard';
import { ds } from 'src/utils/colors';

ChartJS.register(CategoryScale, LinearScale, BarElement, PointElement, LineElement, Title, ChartTooltip, Legend);

interface ThresholdEvidenceProps {
  data: ThresholdSuggestionItem;
}

type ChipTone = 'neutral' | 'info' | 'success' | 'warning' | 'critical' | 'savings' | 'waste' | 'agent';

const RECOMMENDATION_LABELS: Record<string, { label: string; tone: ChipTone }> = {
  tune_threshold: { label: 'Tune Threshold', tone: 'info' },
  increase_duration: { label: 'Increase Duration', tone: 'warning' },
  tune_both: { label: 'Tune Both', tone: 'info' },
  disable: { label: 'Disable Alert', tone: 'critical' },
  none: { label: 'No Change', tone: 'success' },
  review_alert: { label: 'Review Alert', tone: 'warning' },
  not_eligible: { label: 'Not Eligible', tone: 'neutral' },
};

const CLASSIFICATION_INFO: Record<string, { tone: ChipTone; description: string }> = {
  false_positive: { tone: 'critical', description: 'Mostly noise — high transient rate, low engagement' },
  broken: { tone: 'critical', description: 'Misconfigured — fires constantly with no resolution' },
  noisy_but_real: { tone: 'warning', description: 'Fires too often but represents real conditions' },
  healthy: { tone: 'success', description: 'Firing at acceptable patterns, threshold tuning can reduce noise' },
};

// Tooltips for each stat — explains what the number means
const STAT_TOOLTIPS: Record<string, string> = {
  firings: 'Total number of times this alert fired in the analysis window',
  avg_day: 'Average alert firings per day',
  resolution: 'Percentage of alerts that were acknowledged or resolved by a user',
  engagement: 'Percentage of alerts that someone interacted with (commented, assigned, etc.)',
  transient: 'Percentage of alerts that auto-resolved within 10 minutes — often indicates flapping or overly sensitive thresholds',
  duration_p90: '90th percentile of how long each alert stayed in firing state before resolving',
  flapping: 'Percentage of alerts that rapidly toggled between firing and OK — indicates an unstable threshold boundary',
};

const formatDuration = (seconds: number): string => {
  if (seconds < 60) {
    return `${Math.round(seconds)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.round(seconds % 60);
  if (remainingSeconds === 0) {
    return `${minutes}m`;
  }
  return `${minutes}m ${remainingSeconds}s`;
};

const formatPercent = (value: number | undefined | null): string => {
  if (value == null || isNaN(value)) {
    return '–';
  }
  return `${Math.round(value * 100)}%`;
};

const formatNumber = (value: number): string => {
  const abs = Math.abs(value);
  if (abs >= 1e9) {
    return `${(value / 1e9).toFixed(1)}B`;
  }
  if (abs >= 1e6) {
    return `${(value / 1e6).toFixed(1)}M`;
  }
  if (abs >= 1e4) {
    return `${(value / 1e3).toFixed(1)}K`;
  }
  return value.toFixed(2);
};

const operatorSymbol = (operator?: string): string => {
  const op = (operator || '').toLowerCase();
  if (op.includes('greaterthanorequalto') || op === '>=') {
    return '>=';
  }
  if (op.includes('greaterthan') || op === '>') {
    return '>';
  }
  if (op.includes('lessthanorequalto') || op === '<=') {
    return '<=';
  }
  if (op.includes('lessthan') || op === '<') {
    return '<';
  }
  return '>';
};

const formatThreshold = (value?: number, operator?: string): string => {
  if (value == null) {
    return '-';
  }
  return `${operatorSymbol(operator)} ${formatNumber(value)}`;
};

const formatDurationReason = (reason: string, suggestedDuration?: number): string => {
  if (!suggestedDuration) {
    return reason;
  }
  return reason + ' — Suggested window: ' + suggestedDuration + ' min';
};

const TippedStat = ({ label, tooltip, value, sub }: { label: string; tooltip: string; value: string; sub?: string }): React.ReactElement => (
  <Tooltip title={tooltip} placement='top'>
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], cursor: 'help', minWidth: '56px' }}>
      <Text
        value={label}
        sx={{
          fontSize: ds.text.caption,
          textTransform: 'uppercase',
          letterSpacing: '0.3px',
          lineHeight: 1.3,
          borderBottom: `1px dashed ${ds.gray[300]}`,
          color: ds.gray[500],
          pb: ds.space[1],
        }}
      />
      <Text value={value} sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, lineHeight: 1.4 }} />
      {sub && <Text value={sub} sx={{ fontSize: ds.text.caption, lineHeight: 1.3, color: ds.gray[500] }} />}
    </Box>
  </Tooltip>
);

// ─── Metric Chart (live or percentile fallback) ──────────────────────────
const formatTimestamp = (ts: number): string => {
  const d = new Date(ts * 1000);
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
};

const thresholdLinePlugin = {
  id: 'thresholdLines',
  afterDatasetsDraw: function (chart: any): void {
    const { ctx, chartArea, scales } = chart;
    if (!chartArea || !scales?.y) {
      return;
    }
    const lines = chart.options.plugins?.thresholdLines?.lines || [];

    // First pass: draw the dashed lines.
    const drawn: Array<{ line: any; yPixel: number }> = [];
    for (const line of lines) {
      const yPixel = scales.y.getPixelForValue(line.value);
      if (yPixel < chartArea.top || yPixel > chartArea.bottom) {
        continue;
      }
      ctx.save();
      ctx.beginPath();
      ctx.setLineDash(line.dash || [6, 4]);
      ctx.strokeStyle = line.color;
      ctx.lineWidth = 2;
      ctx.moveTo(chartArea.left, yPixel);
      ctx.lineTo(chartArea.right, yPixel);
      ctx.stroke();
      ctx.restore();
      drawn.push({ line, yPixel });
    }

    // Second pass: place labels with overlap avoidance.
    const labelHeight = 14;
    const sorted = drawn.filter((d) => d.line.label).sort((a, b) => a.yPixel - b.yPixel);
    const placements = sorted.map((d, i) => {
      const next = sorted[i + 1];
      const prev = sorted[i - 1];
      const tooCloseToNext = next && next.yPixel - d.yPixel < labelHeight;
      const tooCloseToPrev = prev && d.yPixel - prev.yPixel < labelHeight;
      const placeBelow = tooCloseToPrev && !tooCloseToNext;
      return { ...d, placeBelow };
    });

    for (const { line, yPixel, placeBelow } of placements) {
      ctx.save();
      ctx.font = '11px sans-serif';
      const text = line.label as string;
      const metrics = ctx.measureText(text);
      const padX = 4;
      const textX = chartArea.right - 4;
      const textY = placeBelow ? yPixel + 11 : yPixel - 5;
      const pillX = textX - metrics.width - padX;
      const pillY = textY - 10;
      ctx.fillStyle = 'rgba(255, 255, 255, 0.85)';
      ctx.fillRect(pillX, pillY, metrics.width + padX * 2, labelHeight);
      ctx.fillStyle = line.color;
      ctx.textAlign = 'right';
      ctx.textBaseline = 'alphabetic';
      ctx.fillText(text, textX, textY);
      ctx.restore();
    }
  },
};

const parseMetricResponse = (response: any): { timestamps: number[]; values: number[]; errorMsg: string | null } => {
  const results = response?.data?.data?.metrics_query?.results || [];
  const firstResult = results[0];
  if (!firstResult) {
    return { timestamps: [], values: [], errorMsg: null };
  }
  if (firstResult.error) {
    return { timestamps: [], values: [], errorMsg: firstResult.error };
  }
  const firstItem = (firstResult.payload || [])[0];
  if (!firstItem) {
    return { timestamps: [], values: [], errorMsg: null };
  }
  return { timestamps: firstItem.timestamps || [], values: firstItem.values || [], errorMsg: null };
};

const buildPercentileChart = (
  metric_stats: NonNullable<ThresholdSuggestionItem['metric_stats']>,
  current_threshold?: number,
  suggested_threshold?: number
): {
  percentiles: Array<{ label: string; value: number }>;
  thresholdLines: Array<{ value: number; color: string; label: string }>;
  yMax: number;
} | null => {
  const percentiles = [
    { label: 'P50', value: metric_stats.p50 },
    { label: 'P90', value: metric_stats.p90 },
    { label: 'P95', value: metric_stats.p95 },
    { label: 'P99', value: metric_stats.p99 },
  ].filter((p) => p.value > 0);

  if (percentiles.length === 0) {
    return null;
  }

  // Chart.js uses canvas — keep resolved hex colors, not CSS vars
  const thresholdLines: Array<{ value: number; color: string; label: string }> = [];
  if (current_threshold !== undefined && current_threshold > 0) {
    thresholdLines.push({ value: current_threshold, color: '#c8323a', label: `Current: ${formatNumber(current_threshold)}` });
  }
  if (suggested_threshold !== undefined && suggested_threshold > 0 && suggested_threshold !== current_threshold) {
    thresholdLines.push({ value: suggested_threshold, color: '#2ca84c', label: `Suggested: ${formatNumber(suggested_threshold)}` });
  }

  const allValues = [...percentiles.map((p) => p.value), ...thresholdLines.map((l) => l.value)];
  const dataMax = Math.max(...allValues);
  const dataMin = Math.min(...percentiles.map((p) => p.value));
  const yPadding = (dataMax - dataMin) * 0.15 || dataMax * 0.1;

  return { percentiles, thresholdLines, yMax: dataMax + yPadding };
};

const MetricChart = ({ data }: { data: ThresholdSuggestionItem }): React.ReactElement => {
  const { query_metadata, current_threshold, suggested_threshold, metric_stats } = data;
  const [chartData, setChartData] = useState<{ labels: string[]; values: number[] } | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!query_metadata || !data.cloud_account_id) {
      return;
    }

    const isPrometheus = query_metadata.metric_provider === 'prometheus';

    if (isPrometheus && !query_metadata.promql) {
      return;
    }

    if (!isPrometheus && (!query_metadata.metric_names || query_metadata.metric_names.length === 0)) {
      return;
    }

    setLoading(true);
    setError(null);

    const endTime = Date.now() / 1000;
    const startTime = endTime - 7 * 24 * 60 * 60;

    const requestPayload: any = {
      account_id: data.cloud_account_id,
      metric_provider: query_metadata.metric_provider,
      metric_provider_source: 'user',
      queries: { A: isPrometheus ? query_metadata.promql || '' : '' },
      start_time: startTime,
      end_time: endTime,
      instant: false,
    };

    if (!isPrometheus) {
      requestPayload.request = {
        service_name: query_metadata.service_name,
        region: query_metadata.region,
        metric_names: query_metadata.metric_names,
        statistics: query_metadata.statistics,
        ...(query_metadata.resource_ids?.length ? { resource_ids: query_metadata.resource_ids } : {}),
        ...(query_metadata.dimensions?.length ? { dimensions: query_metadata.dimensions } : {}),
      };
    }

    observability
      .metricsQuery(requestPayload)
      .then((response: any) => {
        const { timestamps, values, errorMsg } = parseMetricResponse(response);
        if (errorMsg) {
          setError(errorMsg);
        }
        if (timestamps.length > 0) {
          setChartData({ labels: timestamps.map(formatTimestamp), values });
        }
      })
      .catch(() => {
        setError('Failed to fetch metric data');
      })
      .finally(() => {
        setLoading(false);
      });
  }, [data.cloud_account_id, query_metadata]);

  if (query_metadata) {
    if (loading) {
      return (
        <Box>
          <Text value='Metric History (7d)' sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1], color: ds.gray[700] }} />
          <div className='shimmer' style={{ height: 160 }} />
        </Box>
      );
    }

    if (error && !chartData) {
      return (
        <Box>
          <Text value='Metric History (7d)' sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1], color: ds.gray[700] }} />
          <Text value={`Could not load metric data: ${error}`} sx={{ fontSize: ds.text.small, color: ds.gray[600] }} />
        </Box>
      );
    }

    if (chartData && chartData.values.length > 0) {
      // Chart.js uses canvas — keep resolved hex colors, not CSS vars
      const thresholdDatasets: any[] = [];
      if (current_threshold !== undefined) {
        thresholdDatasets.push({
          label: `Current (${current_threshold.toFixed(2)})`,
          data: Array(chartData.values.length).fill(current_threshold),
          borderColor: '#c8323a',
          borderDash: [6, 4],
          borderWidth: 2,
          pointRadius: 0,
          fill: false,
        });
      }
      if (suggested_threshold !== undefined && suggested_threshold !== current_threshold) {
        thresholdDatasets.push({
          label: `Suggested (${suggested_threshold.toFixed(2)})`,
          data: Array(chartData.values.length).fill(suggested_threshold),
          borderColor: '#2ca84c',
          borderDash: [6, 4],
          borderWidth: 2,
          pointRadius: 0,
          fill: false,
        });
      }

      return (
        <Box>
          <Text value='Metric History (7d)' sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1], color: ds.gray[700] }} />
          <LineChart
            labels={chartData.labels}
            dataset={[
              {
                label: data.metric_name || 'Metric Value',
                data: chartData.values,
                borderColor: '#5b9eff',
                backgroundColor: 'rgba(91, 158, 255, 0.1)',
                borderWidth: 1.5,
                pointRadius: 0,
                fill: true,
              },
              ...thresholdDatasets,
            ]}
            chartLabel={[data.metric_name || 'Metric Value']}
            minHeight={160}
          />
        </Box>
      );
    }
  }

  const chartInfo = metric_stats ? buildPercentileChart(metric_stats, current_threshold, suggested_threshold) : null;
  if (!chartInfo) {
    return (
      <Box>
        <Text value='Metric Distribution' sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1], color: ds.gray[700] }} />
        <Text
          value='Metric visualization is not available for this alert source. The suggestion is based on statistical analysis of alert firing patterns.'
          sx={{ fontSize: ds.text.small, lineHeight: 1.4, color: ds.gray[600] }}
        />
      </Box>
    );
  }

  const barChartData = {
    labels: chartInfo.percentiles.map((p) => p.label),
    datasets: [
      {
        label: 'Metric Value',
        data: chartInfo.percentiles.map((p) => p.value),
        // Chart.js canvas — resolved hex values for DS blue scale
        backgroundColor: ['#d6ebff', '#5b9eff', '#0c64d6', '#0a4ea3'].slice(0, chartInfo.percentiles.length),
        borderRadius: 4,
        barPercentage: 0.6,
      },
    ],
  };

  const pValues = chartInfo.percentiles.map((p) => p.value);
  const pMin = Math.min(...pValues);
  const pMax = Math.max(...pValues);
  const isConstantMetric = pMax - pMin < pMin * 0.05;
  const thresholdFarBelow = current_threshold !== undefined && current_threshold > 0 && current_threshold < pMin * 0.6;
  const shouldStartAtZero = isConstantMetric || thresholdFarBelow;

  const options: any = {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { intersect: false, mode: 'index' as const },
    scales: {
      x: { ticks: { font: { size: 11 } } },
      y: {
        beginAtZero: shouldStartAtZero,
        max: chartInfo.yMax,
        ticks: { font: { size: 10 } },
      },
    },
    plugins: {
      legend: { display: false },
      title: { display: false },
      thresholdLines: { lines: chartInfo.thresholdLines },
      tooltip: {
        callbacks: {
          label: (item: any) => `Value: ${Number(item.raw).toFixed(2)}`,
          afterBody: () => chartInfo.thresholdLines.map((l) => l.label),
        },
      },
    },
  };

  return (
    <Box>
      <Text
        value='Metric Distribution (Percentiles)'
        sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1], color: ds.gray[700] }}
      />
      <Box sx={{ height: '160px', position: 'relative' }}>
        <Bar data={barChartData} options={options} plugins={[thresholdLinePlugin]} />
      </Box>
    </Box>
  );
};

// ─── Event Trend Chart ───────────────────────────────────────────────
const EventTrendChart = ({ data }: { data: ThresholdSuggestionItem }): React.ReactElement => {
  const [trendData, setTrendData] = useState<{ data: number[]; labels: string[] }>({ data: [], labels: [] });
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const aggregationKey = data.event_aggregation_key || data.alert_rule_key || data.alert_name || data.id;
    if (!data.cloud_account_id || !aggregationKey) {
      return;
    }
    setLoading(true);
    const endDate = new Date();
    const startDate = new Date();
    startDate.setDate(startDate.getDate() - 30);

    k8sApi
      .getK8sEventGroupings(
        1000,
        0,
        {
          account_id: data.cloud_account_id,
          aggregation_key: aggregationKey,
          source: [data.source],
          start_date: startDate,
          end_date: endDate,
          onlyGroupingCount: true,
        },
        ['created_at'],
        ['created_at', 'event_count'],
        { name: 'created_at', order: 'asc' }
      )
      .then((res: any) => {
        const counts: number[] = [];
        const labels: string[] = [];
        (res?.data?.event_groupings || []).forEach((item: any) => {
          counts.push(item.event_count);
          labels.push(getDateString(item.created_at));
        });
        setTrendData({ data: counts, labels });
      })
      .catch((e) => {
        console.error('Failed to fetch event trend data', e);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [data.cloud_account_id, data.alert_rule_key, data.source]);

  if (!loading && trendData.data.length === 0) {
    return (
      <Box>
        <Text
          value='Alert Firing Trend (30d)'
          sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1], color: ds.gray[700] }}
        />
        <Text value='No event data available.' sx={{ fontSize: ds.text.small, color: ds.gray[600] }} />
      </Box>
    );
  }

  return (
    <Box>
      <Text value='Alert Firing Trend (30d)' sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1], color: ds.gray[700] }} />
      <LineChart data={trendData.data} labels={trendData.labels} loading={loading} chartLabel='Firings per day' minHeight={160} />
    </Box>
  );
};

// ─── Recent Events Tab ──────────────────────────────────────────────
export const RecentEventsTab: React.FC<ThresholdEvidenceProps> = ({ data }) => {
  const endDate = Date.now();
  const startDate = endDate - 30 * 24 * 60 * 60 * 1000;

  const aggregationKey = data.event_aggregation_key || data.alert_rule_key || data.alert_name || data.id;

  return (
    <KubernetesEventsTable
      accountId={data.cloud_account_id}
      recordsPerPage={10}
      defaultQuery={{ aggregation_key: aggregationKey, source: [data.source], startTime: startDate, endTime: endDate, startDate, endDate }}
      enableFilters={false}
      showTimeFilter={false}
    />
  );
};

// ─── Risk Warning Banner ─────────────────────────────────────────────
type RiskLevel = 'dangerous' | 'review';

const RISK_BANNER_STYLES: Record<RiskLevel, { bg: string; border: string; color: string }> = {
  dangerous: { bg: ds.red[100], border: ds.red[300], color: ds.red[600] },
  review: { bg: ds.amber[100], border: ds.amber[200], color: ds.amber[500] },
};

const EMOJI_PREFIX_RE = /^[☀-➰︀-️\u{1F300}-\u{1F5FF}\u{1F600}-\u{1F9FF}\u{1F900}-\u{1F9FF}⚠⚡✓]+\s*/u;
const stripLeadingEmoji = (text: string): string => text.replace(EMOJI_PREFIX_RE, '').trim();

const RiskWarningBanner = ({ riskLevel, warnings }: { riskLevel?: string; warnings?: string[] }): React.ReactElement | null => {
  if (!riskLevel || riskLevel === 'safe' || !warnings || warnings.length === 0) {
    return null;
  }
  const style = RISK_BANNER_STYLES[riskLevel as RiskLevel] || RISK_BANNER_STYLES.review;
  return (
    <Box
      sx={{
        backgroundColor: style.bg,
        border: `1px solid ${style.border}`,
        borderRadius: ds.radius.sm,
        p: `${ds.space[2]} ${ds.space[3]}`,
      }}
    >
      {warnings.map((warning) => (
        <Text
          key={warning}
          value={stripLeadingEmoji(warning)}
          sx={{
            fontSize: ds.text.small,
            color: style.color,
            lineHeight: 1.5,
            display: 'block',
            fontWeight: ds.weight.medium,
            '&:not(:last-child)': { mb: ds.space[1] },
          }}
        />
      ))}
    </Box>
  );
};

// Plain-English guidance for each recommendation type
const RECOMMENDATION_GUIDANCE: Record<string, string> = {
  tune_threshold: 'The current threshold is too sensitive for the observed metric range. Adjusting it can reduce alert noise.',
  increase_duration:
    'The threshold appears correct, but the alert triggers on short-lived spikes. A longer evaluation window would filter transient noise.',
  tune_both:
    'Both the threshold and evaluation window can be improved. The threshold is slightly sensitive and the alert also reacts to brief spikes.',
  disable: 'This alert fires constantly without meaningful engagement. It may be misconfigured or monitoring a condition that no longer applies.',
  none: 'This alert appears to be tuned correctly. No changes are recommended at this time.',
};

const FULL_SUPPRESSION_GUIDANCE =
  'This suggestion would suppress all recent firings for this rule. Verify the new threshold still detects real incidents before applying.';

// ─── Main Component ──────────────────────────────────────────────────
const ThresholdEvidence: React.FC<ThresholdEvidenceProps> = ({ data }) => {
  const { firing_analysis, alert_quality, metric_stats, recommendation_type, reason } = data;
  const recType = metric_stats?.recommendation_type || recommendation_type || '';
  const recStyle = RECOMMENDATION_LABELS[recType];
  const classificationInfo = CLASSIFICATION_INFO[alert_quality?.classification || ''] || {
    tone: 'neutral' as ChipTone,
    description: '',
  };

  const riskLevel = metric_stats?.risk_level;
  const riskWarnings = metric_stats?.risk_warnings;

  const showReduction =
    data.estimated_reduction != null &&
    data.estimated_reduction > 0 &&
    recType !== 'increase_duration' &&
    recType !== 'disable' &&
    recType !== 'none';
  let reductionText: string | null = null;
  if (showReduction) {
    reductionText = data.estimated_reduction === 100 ? 'Suppresses all alerts' : `Est. ${Math.round(data.estimated_reduction!)}% fewer alerts`;
  }
  const reductionTone: ChipTone = data.estimated_reduction === 100 ? 'critical' : 'success';

  const hasRiskBanner = riskWarnings && riskWarnings.length > 0 && riskLevel && riskLevel !== 'safe';
  let guidance = RECOMMENDATION_GUIDANCE[recType] || '';
  if (data.estimated_reduction === 100 && hasRiskBanner) {
    guidance = '';
  } else if (data.estimated_reduction === 100 && (recType === 'tune_threshold' || recType === 'tune_both')) {
    guidance = FULL_SUPPRESSION_GUIDANCE;
  }

  return (
    <WidgetCard sx={{ mt: 0, mb: 0, p: `${ds.space[4]} ${ds.space[5]}` }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], overflow: 'hidden', maxWidth: '100%' }}>
        {/* ─── Risk warnings (top, prominent) ─── */}
        <RiskWarningBanner riskLevel={riskLevel} warnings={riskWarnings} />

        {/* ─── Metric name + Recommendation summary ─── */}
        <Box>
          {data.metric_name && (
            <Text
              showAutoEllipsis
              value={`Metric: ${data.metric_name}`}
              sx={{ fontSize: ds.text.caption, fontFamily: ds.font.mono, mb: ds.space[2], color: ds.gray[600] }}
            />
          )}
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
            {recStyle && (
              <Chip variant='tag' size='xs' tone={recStyle.tone}>
                {recStyle.label}
              </Chip>
            )}
            <Text value={formatThreshold(data.current_threshold, data.operator)} sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold }} />
            {data.current_threshold !== data.suggested_threshold &&
              recType !== 'disable' &&
              recType !== 'none' &&
              recType !== 'increase_duration' &&
              recType !== 'review_alert' &&
              recType !== 'not_eligible' && (
                <>
                  <Text value='→' sx={{ fontSize: ds.text.bodyLg, mx: `-${ds.space[1]}`, color: ds.gray[600] }} />
                  <Text
                    value={formatThreshold(data.suggested_threshold, data.operator)}
                    sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.green[600] }}
                  />
                </>
              )}
            {reductionText && (
              <Tooltip title='Estimated percentage reduction in alert firings if this threshold change is applied' placement='top'>
                <Chip variant='tag' size='xs' tone={reductionTone}>
                  {reductionText}
                </Chip>
              </Tooltip>
            )}
            {alert_quality && (
              <Tooltip title={classificationInfo.description} placement='top'>
                <Chip variant='tag' size='xs' tone={classificationInfo.tone}>
                  {alert_quality.classification.replace(/_/g, ' ')}
                </Chip>
              </Tooltip>
            )}
          </Box>
        </Box>

        {/* ─── Guidance + Technical reason ─── */}
        {(guidance || reason || metric_stats?.duration_reason) && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            {guidance && <Text value={guidance} sx={{ fontSize: ds.text.small, lineHeight: 1.6, fontStyle: 'italic', color: ds.gray[700] }} />}
            {reason &&
              (() => {
                let cleaned = reason;
                cleaned = cleaned.replace(/^(?:Adjust|Raise|Lower) threshold from [^.]+\.\s*/i, '');
                cleaned = cleaned.replace(/^Threshold appears correct[^.]*\.\s*/i, '');
                cleaned = cleaned.replace(/^This alert appears misconfigured[^.]*\.\s*/i, '');
                cleaned = cleaned.replace(/^This alert threshold appears correctly tuned\.\s*/i, '');
                cleaned = cleaned.replace(/Expected to reduce alert volume by ~?\d+%\.\s*/i, '');
                cleaned = cleaned.replace(/This would suppress all recent alerts[^.]*\.\s*/i, '');
                cleaned = cleaned.replace(/⚠[^.(]+(\.|\s*$)\s*/g, '');
                cleaned = cleaned.replace(/\(MAD:[^)]*\)\.\s*/g, '');
                cleaned = cleaned.replace(/Metric values:[^.]+\.\s*/gi, '');
                cleaned = cleaned.trim();
                if (!cleaned) {
                  return null;
                }
                return <Text value={cleaned} sx={{ fontSize: ds.text.small, lineHeight: 1.6, color: ds.gray[600], wordBreak: 'break-word' }} />;
              })()}
            {metric_stats?.duration_reason &&
              (() => {
                let durationText = formatDurationReason(metric_stats.duration_reason, metric_stats.suggested_duration);
                if (guidance && recType === 'increase_duration') {
                  durationText = durationText.replace(/^Threshold appears correct,?\s*but\s*/i, '');
                }
                return (
                  <Text value={durationText} sx={{ fontSize: ds.text.small, lineHeight: 1.6, color: ds.blue[700], fontWeight: ds.weight.medium }} />
                );
              })()}
          </Box>
        )}

        {/* ─── Charts (2-column) ─── */}
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '3fr 2fr' }, gap: ds.space[5] }}>
          <Box sx={{ minWidth: 0, overflow: 'hidden' }}>
            <MetricChart data={data} />
          </Box>
          <Box sx={{ minWidth: 0, overflow: 'hidden' }}>
            <EventTrendChart data={data} />
          </Box>
        </Box>

        {/* ─── Firing stats ─── */}
        {(firing_analysis || alert_quality) && (
          <Box sx={{ borderTop: `1px solid ${ds.gray[200]}`, pt: ds.space[3] }}>
            <Text
              value='Alert Behavior (last 30 days)'
              sx={{
                fontSize: ds.text.caption,
                fontWeight: ds.weight.semibold,
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
                mb: ds.space[2],
                color: ds.gray[700],
              }}
            />
            <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[5], flexWrap: 'wrap' }}>
              {firing_analysis && (
                <>
                  <TippedStat
                    label='Firings'
                    tooltip={STAT_TOOLTIPS.firings}
                    value={String(firing_analysis.total_occurrences)}
                    sub={`over ${firing_analysis.time_range_days} days`}
                  />
                  <TippedStat label='Avg/Day' tooltip={STAT_TOOLTIPS.avg_day} value={(firing_analysis.avg_firings_per_day ?? 0).toFixed(1)} />
                </>
              )}
              {alert_quality && (
                <>
                  <TippedStat label='Resolution' tooltip={STAT_TOOLTIPS.resolution} value={formatPercent(alert_quality.resolution_rate)} />
                  <TippedStat label='Engagement' tooltip={STAT_TOOLTIPS.engagement} value={formatPercent(alert_quality.engagement_rate)} />
                  <TippedStat label='Transient' tooltip={STAT_TOOLTIPS.transient} value={formatPercent(alert_quality.transient_rate)} />
                  {alert_quality.duration_p90 > 0 && (
                    <TippedStat label='Duration P90' tooltip={STAT_TOOLTIPS.duration_p90} value={formatDuration(alert_quality.duration_p90)} />
                  )}
                  <TippedStat label='Flapping' tooltip={STAT_TOOLTIPS.flapping} value={formatPercent(alert_quality.flapping_rate)} />
                </>
              )}
            </Box>
          </Box>
        )}
      </Box>
    </WidgetCard>
  );
};

export default ThresholdEvidence;
