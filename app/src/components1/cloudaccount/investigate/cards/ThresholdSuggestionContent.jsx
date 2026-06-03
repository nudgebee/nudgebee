import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Chip } from '@components1/ds/Chip';
import { Divider } from '@components1/ds/Divider';
import { ds } from '@utils/colors';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, LineElement, PointElement, Title, Tooltip, Legend } from 'chart.js';
import { Bar } from 'react-chartjs-2';

ChartJS.register(CategoryScale, LinearScale, BarElement, LineElement, PointElement, Title, Tooltip, Legend);

const CONFIDENCE_COLORS = {
  high: { hue: 'green' },
  medium: { hue: 'amber' },
  low: { hue: 'red' },
};

const formatValue = (val) => {
  if (val == null) {
    return '-';
  }
  if (Math.abs(val) >= 1000000) {
    return `${(val / 1000000).toFixed(1)}M`;
  }
  if (Math.abs(val) >= 1000) {
    return `${(val / 1000).toFixed(1)}K`;
  }
  return val.toFixed(2);
};

const RECOMMENDATION_LABELS = {
  tune_threshold: { label: 'Tune Threshold', hue: 'blue' },
  increase_duration: { label: 'Increase Duration', hue: 'amber' },
  tune_both: { label: 'Tune Both', hue: 'violet' },
  disable: { label: 'Consider Disabling', hue: 'red' },
  none: { label: 'No Change Needed', hue: 'green' },
  review_alert: { label: 'Review Alert', hue: 'amber' },
  not_eligible: { label: 'Not Eligible', hue: 'slate' },
};

const CLASSIFICATION_LABELS = {
  false_positive: { label: 'False Positive', hue: 'red' },
  noisy_but_real: { label: 'Noisy but Real', hue: 'amber' },
  broken: { label: 'Broken', hue: 'red' },
  healthy: { label: 'Healthy', hue: 'green' },
};

const getOperatorSymbol = (operator) => {
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

const ThresholdSuggestionContent = ({ data }) => {
  if (!data?.available || !data?.suggestion) {
    return null;
  }

  const { alert_definition: alert, suggestion, firing_analysis: firing, alert_quality: quality } = data;
  const confidenceColor = CONFIDENCE_COLORS[suggestion.confidence] || CONFIDENCE_COLORS.low;
  const operatorLabel = getOperatorSymbol(alert?.operator);

  const percentiles = [
    { label: 'P50', value: suggestion.metric_p50 },
    { label: 'P90', value: suggestion.metric_p90 },
    { label: 'P95', value: suggestion.metric_p95 },
    { label: 'P99', value: suggestion.metric_p99 },
  ].filter((p) => p.value != null);

  const currentThreshold = alert?.current_threshold;
  const suggestedThreshold = suggestion.suggested_threshold;

  const chartData = {
    labels: percentiles.map((p) => p.label),
    datasets: [
      {
        type: 'bar',
        label: 'Metric Value',
        data: percentiles.map((p) => p.value),
        backgroundColor: 'rgba(37, 99, 235, 0.6)',
        borderColor: 'rgba(37, 99, 235, 1)',
        borderWidth: 1,
        borderRadius: 4,
        barPercentage: 0.5,
        order: 2,
      },
      ...(currentThreshold != null
        ? [
            {
              type: 'line',
              label: `Current Threshold (${operatorLabel} ${formatValue(currentThreshold)})`,
              data: percentiles.map(() => currentThreshold),
              borderColor: '#DC2626',
              borderWidth: 2,
              borderDash: [6, 4],
              pointRadius: 0,
              fill: false,
              order: 1,
            },
          ]
        : []),
      ...(suggestedThreshold != null
        ? [
            {
              type: 'line',
              label: `Suggested Threshold (${operatorLabel} ${formatValue(suggestedThreshold)})`,
              data: percentiles.map(() => suggestedThreshold),
              borderColor: '#16A34A',
              borderWidth: 2,
              borderDash: [6, 4],
              pointRadius: 0,
              fill: false,
              order: 1,
            },
          ]
        : []),
    ],
  };

  const chartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { intersect: false, mode: 'index' },
    scales: {
      x: {
        grid: { display: false },
        ticks: { font: { size: 12, weight: 600 } },
      },
      y: {
        beginAtZero: true,
        grid: { color: 'rgba(0,0,0,0.06)' },
        ticks: {
          callback: (val) => formatValue(val),
          font: { size: 11 },
        },
      },
    },
    plugins: {
      legend: {
        position: 'bottom',
        labels: {
          boxWidth: 12,
          boxHeight: 2,
          padding: 16,
          font: { size: 11 },
          usePointStyle: false,
        },
      },
      tooltip: {
        callbacks: {
          label: (ctx) => `${ctx.dataset.label}: ${typeof ctx.raw === 'number' ? ctx.raw.toFixed(2) : ctx.raw}`,
        },
      },
    },
  };

  return (
    <Box sx={{ p: ds.space[4], overflow: 'hidden', maxWidth: '100%' }}>
      {/* Alert info */}
      {alert?.alarm_name && (
        <Typography variant='body2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[1] }}>
          {alert.alarm_name}
        </Typography>
      )}
      {alert?.metric_name && (
        <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[4] }}>
          {alert.metric_namespace ? `${alert.metric_namespace} / ` : ''}
          {alert.metric_name}
          {alert.aggregation ? ` (${alert.aggregation})` : ''}
        </Typography>
      )}

      {/* Recommendation type and confidence */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[4], flexWrap: 'wrap' }}>
        {suggestion.recommendation_type && RECOMMENDATION_LABELS[suggestion.recommendation_type] && (
          <Chip variant='tag' size='sm' hue={RECOMMENDATION_LABELS[suggestion.recommendation_type].hue}>
            {RECOMMENDATION_LABELS[suggestion.recommendation_type].label}
          </Chip>
        )}
        {quality?.classification && CLASSIFICATION_LABELS[quality.classification] && (
          <Chip variant='tag' size='sm' hue={CLASSIFICATION_LABELS[quality.classification].hue}>
            {CLASSIFICATION_LABELS[quality.classification].label}
          </Chip>
        )}
        {suggestion.confidence && (
          <Chip variant='tag' size='sm' hue={confidenceColor.hue} sx={{ textTransform: 'capitalize' }}>
            {`${suggestion.confidence} confidence`}
          </Chip>
        )}
        {suggestion.estimated_reduction > 0 && (
          <Chip variant='tag' size='sm' hue='blue'>
            {`~${Math.round(suggestion.estimated_reduction)}% noise reduction`}
          </Chip>
        )}
      </Box>

      {/* Duration recommendation */}
      {suggestion.suggested_duration > 0 && (
        <Box
          sx={{ mb: ds.space[4], p: ds.space[3], backgroundColor: ds.amber[100], borderRadius: ds.radius.sm, border: `1px solid ${ds.amber[200]}` }}
        >
          <Typography variant='body2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[1] }}>
            Suggested evaluation window: {suggestion.suggested_duration} minutes
          </Typography>
          {suggestion.duration_reason && (
            <Typography variant='caption' sx={{ color: ds.gray[600] }}>
              {suggestion.duration_reason}
            </Typography>
          )}
        </Box>
      )}

      {/* Chart */}
      {percentiles.length > 0 && (
        <Box sx={{ position: 'relative', width: '100%', height: 240, mb: ds.space[4] }}>
          <Bar data={chartData} options={chartOptions} />
        </Box>
      )}

      {/* Reason */}
      {suggestion.reason && (
        <Typography variant='body2' sx={{ mb: ds.space[4], color: ds.gray[600], wordBreak: 'break-word', overflowWrap: 'break-word' }}>
          {suggestion.reason}
        </Typography>
      )}

      {/* Firing analysis */}
      {firing && (
        <>
          <Divider sx={{ mb: ds.space[3] }} />
          <Typography variant='body2' sx={{ color: ds.gray[600] }}>
            {firing.total_occurrences} firings in {firing.time_range_days} days
            {firing.avg_firings_per_day > 0 ? ` (~${firing.avg_firings_per_day.toFixed(1)}/day)` : ''}
          </Typography>
        </>
      )}

      {/* Alert quality stats */}
      {quality && (
        <>
          <Divider sx={{ my: ds.space[3] }} />
          <Box sx={{ display: 'flex', gap: ds.space[4], flexWrap: 'wrap' }}>
            {quality.resolution_rate != null && (
              <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                Resolution: {Math.round(quality.resolution_rate * 100)}%
              </Typography>
            )}
            {quality.transient_rate > 0 && (
              <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                Transient: {Math.round(quality.transient_rate * 100)}%
              </Typography>
            )}
            {quality.engagement_rate != null && (
              <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                Engagement: {Math.round(quality.engagement_rate * 100)}%
              </Typography>
            )}
          </Box>
        </>
      )}
    </Box>
  );
};

ThresholdSuggestionContent.propTypes = {
  data: PropTypes.shape({
    available: PropTypes.bool,
    alert_definition: PropTypes.shape({
      alarm_name: PropTypes.string,
      metric_name: PropTypes.string,
      metric_namespace: PropTypes.string,
      current_threshold: PropTypes.number,
      operator: PropTypes.string,
      aggregation: PropTypes.string,
    }),
    suggestion: PropTypes.shape({
      confidence: PropTypes.string,
      estimated_reduction: PropTypes.number,
      recommendation_type: PropTypes.string,
      suggested_threshold: PropTypes.number,
      suggested_duration: PropTypes.number,
      duration_reason: PropTypes.string,
      reason: PropTypes.string,
      metric_p50: PropTypes.number,
      metric_p90: PropTypes.number,
      metric_p95: PropTypes.number,
      metric_p99: PropTypes.number,
    }),
    firing_analysis: PropTypes.shape({
      total_occurrences: PropTypes.number,
      time_range_days: PropTypes.number,
      avg_firings_per_day: PropTypes.number,
    }),
    alert_quality: PropTypes.shape({
      classification: PropTypes.string,
      resolution_rate: PropTypes.number,
      transient_rate: PropTypes.number,
      engagement_rate: PropTypes.number,
    }),
  }).isRequired,
};

export default ThresholdSuggestionContent;
